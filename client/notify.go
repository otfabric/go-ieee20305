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

// NotificationHandler is the http.Handler of the notifications a server
// posts to a device: serve it over TLS that verifies the server's
// certificate (security.ServerTLSConfig with the CAs of the server).
//
// The server may send a notification more than once; the handler passes on
// a notification only when its content differs from the previous one of the
// same subscription.
type NotificationHandler struct {
	// Handle receives the notifications. An error answers 500, so that the
	// server sends again. Required.
	Handle func(ctx context.Context, n *ieee20305.Notification) error
	// Codec decodes the notifications; package ieee20305 when nil.
	Codec Codec
	// Authorize, when set, decides whether to take a request: by its peer
	// certificates, say. An error answers 403.
	Authorize func(r *http.Request) error
	// MaxBytes limits the size of a notification; 1 MiB when zero.
	MaxBytes int64

	mu   sync.Mutex
	last map[string][sha256.Size]byte // by subscription
}

// ServeHTTP takes a notification.
func (h *NotificationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.Authorize != nil {
		if err := h.Authorize(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || (mt != MediaType && mt != "application/xml" && mt != "text/xml") {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
	}
	limit := h.MaxBytes
	if limit <= 0 {
		limit = defaultMaxResponseBytes
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	codec := h.Codec
	if codec == nil {
		codec = ieee20305.StandardCodec
	}
	v, err := codec.Decode(data)
	n, ok := v.(*ieee20305.Notification)
	if err != nil || !ok {
		http.Error(w, "not a Notification", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(contentOf(n, data))
	h.mu.Lock()
	if h.last == nil {
		h.last = map[string][sha256.Size]byte{}
	}
	dup := h.last[n.SubscriptionURI] == sum
	h.mu.Unlock()
	if !dup {
		if err := h.Handle(r.Context(), n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.mu.Lock()
		h.last[n.SubscriptionURI] = sum
		h.mu.Unlock()
	}
	w.WriteHeader(http.StatusCreated)
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
