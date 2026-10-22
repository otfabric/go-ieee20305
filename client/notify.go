// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Subscribe posts a subscription of the resource at resource to the
// SubscriptionList at list: the server posts a Notification to
// notificationURI, an https URL of a NotificationHandler, when the
// resource changes, with at most limit items of a list. It returns the href
// of the subscription, for Unsubscribe.
func (c *Client) Subscribe(ctx context.Context, list, resource, notificationURI string, limit uint32) (string, error) {
	sub := &ieee20305.Subscription{NotificationURI: notificationURI, Limit: ieee20305.UInt32(limit), Level: "+S1"}
	sub.SubscribedResource = resource
	href, err := c.Post(ctx, list, sub)
	if err == nil && href == "" {
		err = fmt.Errorf("%w: the server sent no Location for the subscription", ieee20305.ErrProtocol)
	}
	return href, err
}

// Unsubscribe deletes the subscription at href.
func (c *Client) Unsubscribe(ctx context.Context, href string) error {
	return c.Delete(ctx, href)
}

// NotificationConfig configures a NotificationHandler.
type NotificationConfig struct {
	// Codec decodes the notifications; package ieee20305 when nil.
	Codec Codec
	// Authorize, when set, decides whether to take a request: by its peer
	// certificates, say. An error answers 403.
	Authorize func(r *http.Request) error
	// MaxBytes limits the size of a notification; 1 MiB when zero.
	MaxBytes int64
	// MaxSubscriptions bounds the subscriptions whose last notification the
	// handler remembers, to pass on only new content: 1000 when zero. The
	// least recently notified is forgotten first.
	MaxSubscriptions int
}

// NotificationHandler is the http.Handler of the notifications a server
// posts to a device: serve it over TLS that verifies the server's
// certificate (security.ServerTLSConfig with the CAs of the server).
//
// The server may send a notification more than once; the handler passes on
// a notification only when its content differs from the previous one of the
// same subscription. The notifications of a subscription are handled one at
// a time. It is safe for concurrent use.
type NotificationHandler struct {
	handle func(ctx context.Context, n *ieee20305.Notification) error
	cfg    NotificationConfig

	mu   sync.Mutex
	subs map[string]*notified // by subscription
	tick uint64
}

// notified is what the handler remembers of a subscription.
type notified struct {
	mu    sync.Mutex // held while the subscription's notification is handled
	sum   [sha256.Size]byte
	seen  bool
	used  uint64 // the tick of the last use, for eviction
	users int
}

// NewNotificationHandler returns a NotificationHandler that passes the
// notifications to handle: an error of handle answers 500, so that the
// server sends again.
func NewNotificationHandler(handle func(ctx context.Context, n *ieee20305.Notification) error, cfg NotificationConfig) (*NotificationHandler, error) {
	if handle == nil {
		return nil, fmt.Errorf("%w: a NotificationHandler needs a handle function", ieee20305.ErrConfiguration)
	}
	if cfg.Codec == nil {
		cfg.Codec = ieee20305.StandardCodec
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxResponseBytes
	}
	if cfg.MaxSubscriptions <= 0 {
		cfg.MaxSubscriptions = 1000
	}
	return &NotificationHandler{handle: handle, cfg: cfg, subs: map[string]*notified{}}, nil
}

// ServeHTTP takes a notification.
func (h *NotificationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.cfg.Authorize != nil {
		if err := h.cfg.Authorize(r); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || (mt != MediaType && mt != "application/xml" && mt != "text/xml") {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.cfg.MaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	v, err := h.cfg.Codec.Decode(data)
	n, ok := v.(*ieee20305.Notification)
	if err != nil || !ok {
		http.Error(w, "not a Notification", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(contentOf(n, data))
	sub := h.acquire(n.SubscriptionURI)
	defer h.release(sub)
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if !sub.seen || sub.sum != sum {
		if err := h.handle(r.Context(), n); err != nil {
			// The handler's error is the device's own business.
			http.Error(w, "the notification was not taken", http.StatusInternalServerError)
			return
		}
		sub.sum, sub.seen = sum, true
	}
	w.WriteHeader(http.StatusCreated)
}

// acquire returns what the handler remembers of the subscription, and
// forgets the least recently notified one beyond MaxSubscriptions.
func (h *NotificationHandler) acquire(uri string) *notified {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tick++
	sub := h.subs[uri]
	if sub == nil {
		sub = &notified{}
		h.subs[uri] = sub
		if len(h.subs) > h.cfg.MaxSubscriptions {
			var oldest string
			var at uint64
			for k, s := range h.subs {
				if s.users == 0 && s != sub && (oldest == "" || s.used < at) {
					oldest, at = k, s.used
				}
			}
			if oldest != "" {
				delete(h.subs, oldest)
			}
		}
	}
	sub.used = h.tick
	sub.users++
	return sub
}

func (h *NotificationHandler) release(sub *notified) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub.users--
}

// contentOf returns what identifies the content of a notification: its
// document without createdDateTime, which differs between sends of the same
// content.
func contentOf(n *ieee20305.Notification, data []byte) []byte {
	c := *n
	c.CreatedDateTime = nil
	out, err := ieee20305.Marshal(&c)
	if err != nil {
		return data
	}
	return out
}
