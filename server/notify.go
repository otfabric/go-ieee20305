// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// Status codes of a Notification.
const (
	// NotificationDefault carries the resource as it is now.
	NotificationDefault ieee20305.UInt8 = 0
	// NotificationCancelled tells that the server cancelled the
	// subscription, without a reason.
	NotificationCancelled ieee20305.UInt8 = 1
	// NotificationResourceDeleted tells that the subscribed resource is
	// gone, and with it the subscription.
	NotificationResourceDeleted ieee20305.UInt8 = 4
)

// watchStore is the store of a server: it tells the server of every
// change, for its events and notifications.
type watchStore struct {
	Store
	s *Server
}

func (w *watchStore) Put(ctx context.Context, n *Node) error {
	if err := w.Store.Put(ctx, n); err != nil {
		return err
	}
	w.s.changed(ctx, n.Href, n, false)
	return nil
}

func (w *watchStore) Delete(ctx context.Context, href string) error {
	if err := w.Store.Delete(ctx, href); err != nil {
		return err
	}
	w.s.changed(ctx, href, nil, true)
	return nil
}

func (w *watchStore) AddMember(ctx context.Context, list, member string) error {
	if err := w.Store.AddMember(ctx, list, member); err != nil {
		return err
	}
	w.s.changed(ctx, list, nil, false)
	return nil
}

func (w *watchStore) RemoveMember(ctx context.Context, list, member string) error {
	if err := w.Store.RemoveMember(ctx, list, member); err != nil {
		return err
	}
	w.s.changed(ctx, list, nil, false)
	return nil
}

// notifier delivers the notifications of the subscriptions of the server.
// A subscription is notified of the state of its resource: a change marks
// it dirty, and its worker sends the resource as it is when it sends, so a
// burst of changes yields one notification with the latest state, never an
// older state after a newer one.
type notifier struct {
	s      *Server
	client *http.Client
	sem    chan struct{}
	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	subs map[string]*subscription // by href
}

// subscription is a subscription of the notifier.
type subscription struct {
	href     string
	resource string // the subscribed href
	list     bool
	uri      string
	limit    uint32
	owner    string

	dirty, running bool
	status         ieee20305.UInt8 // a cancellation to send
}

func newNotifier(s *Server) *notifier {
	n := &notifier{s: s, client: s.cfg.NotificationClient, subs: map[string]*subscription{}}
	workers := s.cfg.NotificationWorkers
	if workers <= 0 {
		workers = 16
	}
	n.sem = make(chan struct{}, workers)
	n.ctx, n.stop = context.WithCancel(context.Background())
	return n
}

// close stops the workers and waits for them.
func (n *notifier) close() {
	n.stop()
	n.wg.Wait()
}

// changed is told of a change of the node at href (n, when it is put), or
// of its deletion.
func (n *notifier) changed(ctx context.Context, href string, node *Node, deleted bool) {
	if node != nil && node.Resource == "Subscription" {
		n.index(ctx, node)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if deleted {
		for h, sub := range n.subs {
			switch {
			case within(h, href):
				delete(n.subs, h)
			case within(sub.resource, href):
				sub.status = NotificationResourceDeleted
				n.schedule(sub)
			}
		}
	}
	for _, sub := range n.subs {
		if sub.status == NotificationDefault && n.matches(ctx, sub, href) {
			sub.dirty = true
			n.schedule(sub)
		}
	}
}

// within reports whether href is base or below it.
func within(href, base string) bool {
	return href == base || strings.HasPrefix(href, strings.TrimSuffix(base, "/")+"/")
}

// matches reports whether a change of href is a change of the resource of
// sub: the resource itself, or an item of the list it is. The caller holds
// n.mu.
func (n *notifier) matches(ctx context.Context, sub *subscription, href string) bool {
	if sub.resource == href {
		return true
	}
	if !sub.list {
		return false
	}
	if path.Dir(href) == sub.resource {
		return true
	}
	members, err := n.s.store.Members(ctx, sub.resource)
	if err != nil {
		return false
	}
	for _, m := range members {
		if m.Href == href {
			return true
		}
	}
	return false
}

// index adds or updates the subscription of the node.
func (n *notifier) index(ctx context.Context, node *Node) {
	doc, ok := node.Value.(*ieee20305.Subscription)
	if !ok {
		return
	}
	target, err := n.s.store.Get(ctx, doc.SubscribedResource)
	list := err == nil && isList(target.Value)
	n.mu.Lock()
	defer n.mu.Unlock()
	sub := n.subs[node.Href]
	if sub == nil {
		sub = &subscription{href: node.Href}
		n.subs[node.Href] = sub
	}
	sub.resource, sub.list, sub.uri, sub.limit, sub.owner = doc.SubscribedResource, list, doc.NotificationURI, uint32(doc.Limit), node.Owner
}

// schedule starts the worker of sub if it has none. The caller holds n.mu.
func (n *notifier) schedule(sub *subscription) {
	if sub.running || n.ctx.Err() != nil {
		return
	}
	sub.running = true
	n.wg.Add(1)
	go n.work(sub)
}

// work sends the notifications of sub until it has none to send.
func (n *notifier) work(sub *subscription) {
	defer n.wg.Done()
	for {
		n.mu.Lock()
		status := sub.status
		if (!sub.dirty && status == NotificationDefault) || n.ctx.Err() != nil {
			sub.running = false
			n.mu.Unlock()
			return
		}
		sub.dirty = false
		n.mu.Unlock()
		err := n.deliver(sub, status)
		if status != NotificationDefault || err != nil {
			if err != nil && !errors.Is(err, errCancelled) && n.ctx.Err() == nil {
				n.s.log.Warn("notification failed; cancelling the subscription", "subscription", sub.href, "err", err)
			}
			n.cancel(sub)
			return
		}
	}
}

// cancel drops sub, and deletes its resource.
func (n *notifier) cancel(sub *subscription) {
	n.mu.Lock()
	sub.running = false
	if n.subs[sub.href] == sub {
		delete(n.subs, sub.href)
	}
	n.mu.Unlock()
	if n.ctx.Err() == nil {
		_ = n.s.store.Delete(n.ctx, sub.href)
	}
}

// deliver sends the notification of sub, retrying with backoff; the
// resource is read again for each attempt.
func (n *notifier) deliver(sub *subscription, status ieee20305.UInt8) error {
	attempts := n.s.cfg.NotificationAttempts
	if attempts <= 0 {
		attempts = 5
	}
	delay := n.s.cfg.NotificationRetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			t := time.NewTimer(delay)
			select {
			case <-n.ctx.Done():
				t.Stop()
				return n.ctx.Err()
			case <-t.C:
			}
			delay *= 2
		}
		var body []byte
		if body, status, err = n.build(sub, status); err != nil {
			return err
		}
		if err = n.post(sub.uri, body); err == nil {
			if status != NotificationDefault {
				return errCancelled
			}
			return nil
		}
	}
	return err
}

// errCancelled ends a subscription whose cancellation was sent.
var errCancelled = errors.New("server: subscription cancelled")

// build returns the notification of sub: the resource as the owner of the
// subscription reads it now, or the cancellation status, which build sets
// when the owner may no longer read the resource.
func (n *notifier) build(sub *subscription, status ieee20305.UInt8) ([]byte, ieee20305.UInt8, error) {
	ctx := n.ctx
	now := ieee20305.TimeType(n.s.clock().Unix())
	note := &ieee20305.Notification{SubscriptionURI: sub.href, CreatedDateTime: &now}
	note.SubscribedResource = sub.resource
	p := n.principal(sub.owner)
	if status == NotificationDefault {
		doc, err := n.read(ctx, p, sub)
		switch {
		case errors.Is(err, ErrNoNode):
			status = NotificationResourceDeleted
		case err != nil:
			status = NotificationCancelled
		default:
			if h, ok := doc.(ieee20305.Hrefer); !ok || h.GetHref() == "" {
				setHref(doc, sub.resource)
			}
			n.s.dressLinks(ctx, p, doc)
			note.Resource = &ieee20305.AnyResource{Value: doc}
		}
	}
	note.Status = status
	out, err := n.s.profile.Codec(p.Edition).Marshal(note)
	return out, status, err
}

// read returns the resource of sub as p reads it.
func (n *notifier) read(ctx context.Context, p Principal, sub *subscription) (any, error) {
	node, err := n.s.store.Get(ctx, sub.resource)
	if err != nil {
		return nil, err
	}
	if node.Value == nil {
		return nil, ErrNoNode
	}
	if !n.s.canRead(p, node) {
		return nil, httpError(http.StatusForbidden, "%s", sub.resource)
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, sub.resource, nil)
	req := &Request{Request: r, Principal: p, Node: node, Spec: n.s.resources[node.Resource],
		Query: Query{Limit: sub.limit, HasLimit: true}}
	if h := n.s.hooks[node.Resource].Get; h != nil {
		return h(ctx, req)
	}
	if isList(node.Value) {
		return n.s.list(ctx, req)
	}
	return node.Value, nil
}

// principal returns the principal of the device lfdi.
func (n *notifier) principal(lfdi string) Principal {
	p := Principal{Edition: n.s.cfg.DefaultEdition}
	if l, err := security.ParseLFDI(lfdi); err == nil {
		p.LFDI, p.SFDI = l, l.SFDI()
	}
	if d, ok := n.s.knownDevice(lfdi); ok {
		p.EndDevice, p.Edition, p.Role = d.EndDevice, d.Edition, d.Role
	}
	return p
}

// post sends a notification to uri.
func (n *notifier) post(uri string, body []byte) error {
	n.sem <- struct{}{}
	defer func() { <-n.sem }()
	req, err := http.NewRequestWithContext(n.ctx, http.MethodPost, uri, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", MediaType)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("POST %s: %s", uri, resp.Status)
	}
	return nil
}

// loadSubscriptions indexes the subscriptions of the store, after a
// restart with a persistent store.
func (n *notifier) loadSubscriptions(ctx context.Context) error {
	hrefs, err := n.s.store.Hrefs(ctx)
	if err != nil {
		return err
	}
	for _, h := range hrefs {
		if node, err := n.s.store.Get(ctx, h); err == nil && node.Resource == "Subscription" {
			n.index(ctx, node)
		}
	}
	return nil
}

// checkSubscription checks the Subscription a client posts or puts: XML,
// a notificationURI of https, and a resource the client reads that takes
// subscriptions.
func (s *Server) checkSubscription(ctx context.Context, r *Request) error {
	if s.cfg.NotificationClient == nil {
		return httpError(http.StatusMethodNotAllowed, "the server sends no notifications")
	}
	sub, ok := r.Doc.(*ieee20305.Subscription)
	if !ok {
		return nil
	}
	bad := func(reason ieee20305.UInt16, format string, a ...any) error {
		return &HTTPError{Status: http.StatusBadRequest, Reason: reason, Err: fmt.Errorf(format, a...)}
	}
	if sub.Encoding != 0 {
		return bad(ReasonUnsupported, "encoding %d: the server sends XML (0)", sub.Encoding)
	}
	u, err := url.Parse(sub.NotificationURI)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return bad(ReasonInvalidRequest, "notificationURI %q is no https URL", sub.NotificationURI)
	}
	target, err := s.store.Get(ctx, sub.SubscribedResource)
	if err != nil || target.Value == nil || !s.canRead(r.Principal, target) {
		return bad(ReasonInvalidRequest, "subscribedResource %q is no resource of the client", sub.SubscribedResource)
	}
	if !subscribable(target.Value) {
		return bad(ReasonUnsupported, "%s takes no subscriptions", sub.SubscribedResource)
	}
	return nil
}

// subscribable reports whether a document takes subscriptions: its type
// has the subscribable attribute, which is not 0 (none).
func subscribable(v any) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	f := rv.Elem().FieldByName("Subscribable")
	if !f.IsValid() {
		return false
	}
	return f.IsNil() || f.Elem().Uint() != 0
}

// createSubscription is the POST of a Subscription.
func (s *Server) createSubscription(ctx context.Context, r *Request) (string, error) {
	if err := s.checkSubscription(ctx, r); err != nil {
		return "", err
	}
	return s.createDefault(ctx, r)
}

// updateSubscription is the PUT of a Subscription.
func (s *Server) updateSubscription(ctx context.Context, r *Request) error {
	if err := s.checkSubscription(ctx, r); err != nil {
		return err
	}
	return s.replace(ctx, r)
}
