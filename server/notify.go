// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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

// errClosed is the error of a change of the store of a closed server.
var errClosed = fmt.Errorf("%w: server", ieee20305.ErrClosed)

// watchStore is the store of a server: it tells the server of every
// change, for its events and notifications.
type watchStore struct {
	Store
	s *Server
}

func (w *watchStore) Put(ctx context.Context, n *Node) error {
	if w.s.isClosed() {
		return errClosed
	}
	if err := w.Store.Put(ctx, n); err != nil {
		return err
	}
	w.s.noteHref(n.Href)
	w.s.changed(ctx, n.Href, n, false)
	return nil
}

func (w *watchStore) Delete(ctx context.Context, href string) error {
	if w.s.isClosed() {
		return errClosed
	}
	if err := w.Store.Delete(ctx, href); err != nil {
		return err
	}
	w.s.changed(ctx, href, nil, true)
	return nil
}

func (w *watchStore) AddMember(ctx context.Context, list, member string) error {
	if w.s.isClosed() {
		return errClosed
	}
	if err := w.Store.AddMember(ctx, list, member); err != nil {
		return err
	}
	w.s.changed(ctx, list, nil, false)
	return nil
}

func (w *watchStore) RemoveMember(ctx context.Context, list, member string) error {
	if w.s.isClosed() {
		return errClosed
	}
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
	kind     string // its WADL resource
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

// close stops the workers and waits for them. It stops under n.mu, so that
// schedule, which checks n.ctx under n.mu, starts no worker after it.
func (n *notifier) close() {
	n.mu.Lock()
	n.stop()
	n.mu.Unlock()
	n.wg.Wait()
}

// changed is told of a change of the node at href (n, when it is put), or
// of its deletion.
func (n *notifier) changed(ctx context.Context, href string, node *Node, deleted bool) {
	if node != nil && node.Resource == "Subscription" {
		n.index(ctx, node)
	}
	type candidate struct {
		sub      *subscription
		resource string
		list     bool
	}
	var candidates []candidate
	n.mu.Lock()
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
		if sub.status == NotificationDefault {
			candidates = append(candidates, candidate{sub, sub.resource, sub.list})
		}
	}
	n.mu.Unlock()
	// Matching reads the store: not under n.mu.
	var matched []*subscription
	for _, c := range candidates {
		if n.matches(ctx, c.resource, c.list, href) {
			matched = append(matched, c.sub)
		}
	}
	if len(matched) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, sub := range matched {
		if n.subs[sub.href] == sub && sub.status == NotificationDefault {
			sub.dirty = true
			n.schedule(sub)
		}
	}
}

// within reports whether href is base or below it.
func within(href, base string) bool {
	return href == base || strings.HasPrefix(href, strings.TrimSuffix(base, "/")+"/")
}

// matches reports whether a change of href is a change of the subscribed
// resource: the resource itself, or an item of the list it is.
func (n *notifier) matches(ctx context.Context, resource string, list bool, href string) bool {
	if resource == href {
		return true
	}
	if !list {
		return false
	}
	if path.Dir(href) == resource {
		return true
	}
	members, err := n.s.store.Members(ctx, resource)
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
	kind := ""
	if err == nil {
		kind = target.Resource
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	sub := n.subs[node.Href]
	if sub == nil {
		sub = &subscription{href: node.Href}
		n.subs[node.Href] = sub
	}
	sub.resource, sub.kind, sub.list, sub.uri, sub.limit, sub.owner = doc.SubscribedResource, kind, list, doc.NotificationURI, uint32(doc.Limit), node.Owner
}

// schedule starts the worker of sub if it has none. The caller holds n.mu.
// A server without a NotificationClient (whose store may hold
// subscriptions of an earlier one) sends nothing.
func (n *notifier) schedule(sub *subscription) {
	if sub.running || n.ctx.Err() != nil || n.client == nil {
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
		start := time.Now()
		err := n.deliver(sub, status)
		if nm, ok := n.s.metrics.(NotificationMetrics); ok && !errors.Is(err, context.Canceled) {
			delivered := err
			if errors.Is(err, errCancelled) {
				delivered = nil
			}
			nm.OnNotification(sub.kind, time.Since(start), delivered)
			if status != NotificationDefault || err != nil {
				cancelled := status
				if cancelled == NotificationDefault {
					cancelled = NotificationCancelled
				}
				nm.OnSubscriptionCancelled(sub.kind, uint8(cancelled))
			}
		}
		if status != NotificationDefault || err != nil {
			if err != nil && !errors.Is(err, errCancelled) && n.ctx.Err() == nil {
				n.s.log.Warnf("cancelling subscription %s: its notifications fail: %v", sub.href, err)
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
	policy := n.s.cfg.NotificationRetry
	if policy == nil {
		policy = ieee20305.ExponentialBackoff{Initial: time.Second, Max: 5 * time.Minute, Attempts: -1}
	}
	cancelAfter := n.s.cfg.NotificationCancelAfter
	if cancelAfter <= 0 {
		cancelAfter = 24 * time.Hour
	}
	var first time.Time
	for attempt := 0; ; attempt++ {
		body, st, err := n.build(sub, status)
		if err != nil {
			return err
		}
		status = st
		err = n.post(sub.uri, body)
		if err == nil {
			if status != NotificationDefault {
				return errCancelled
			}
			return nil
		}
		if first.IsZero() {
			first = time.Now()
		}
		retry, wait := policy.ShouldRetry(attempt, err)
		if !retry || time.Since(first)+wait > cancelAfter {
			return err
		}
		n.s.log.Warnf("notification %d of subscription %s to %s failed, again in %v: %v", attempt+1, sub.href, sub.uri, wait, err)
		t := time.NewTimer(wait)
		select {
		case <-n.ctx.Done():
			t.Stop()
			return n.ctx.Err()
		case <-t.C:
		}
	}
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
		return &ieee20305.StatusError{Method: http.MethodPost, Path: uri, StatusCode: resp.StatusCode}
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
	allowed := s.cfg.NotificationURIAllowed
	if allowed == nil {
		allowed = DefaultNotificationURIAllowed
	}
	if !allowed(u) {
		return httpError(http.StatusForbidden, "notificationURI %q is not allowed", sub.NotificationURI)
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
	href, err := s.createDefault(ctx, r)
	if err == nil {
		sub := r.Doc.(*ieee20305.Subscription)
		s.log.Infof("subscription %s of %s to %s, notified at %s", href, r.Principal.LFDI, sub.SubscribedResource, sub.NotificationURI)
	}
	return href, err
}

// updateSubscription is the PUT of a Subscription.
func (s *Server) updateSubscription(ctx context.Context, r *Request) error {
	if err := s.checkSubscription(ctx, r); err != nil {
		return err
	}
	return s.replace(ctx, r)
}

// DefaultNotificationURIAllowed is the NotificationURIAllowed of a server
// without one: it refuses the hosts a device must not make the server
// connect to with its certificate: localhost, and literal loopback,
// unspecified, link-local and multicast addresses (cloud metadata services
// are link-local). A host name is not resolved: what the names of a network
// reach is for the operator's own NotificationURIAllowed, or its network,
// to decide.
func DefaultNotificationURIAllowed(u *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsInterfaceLocalMulticast()
}
