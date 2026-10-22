// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

func subscription(resource, uri string) *ieee20305.Subscription {
	s := &ieee20305.Subscription{NotificationURI: uri, Limit: 1, Level: "+S1"}
	s.SubscribedResource = resource
	return s
}

func TestNoNotificationClient(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, "https://127.0.0.1/n"))); status != http.StatusMethodNotAllowed {
		t.Errorf("POST of a Subscription without notifications: %d", status)
	}
}

func TestNotSubscribable(t *testing.T) {
	h := newHarness(t, server.Config{NotificationClient: &http.Client{}, NotificationURIAllowed: allowAll}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	n, err := h.srv.Admin().Get(ctx, ed)
	if err != nil {
		t.Fatal(err)
	}
	none := ieee20305.SubscribableType(0)
	n.Value.(*ieee20305.EndDevice).Subscribable = &none
	if err := h.srv.Admin().Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, "https://127.0.0.1/n"))); status != http.StatusBadRequest {
		t.Errorf("subscription of a resource with subscribable 0: %d", status)
	}
}

// After a restart with a persistent store the subscriptions notify again;
// a subscription whose device may no longer read the resource is
// cancelled with status 1.
func TestSubscriptionsAfterRestart(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	sink := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer sink.Close()
	store := server.NewMemStore(nil)
	cfg := server.Config{Store: store, NotificationClient: sink.Client(), NotificationRetryDelay: time.Millisecond, NotificationURIAllowed: allowAll}
	h := newHarness(t, cfg, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	status, hdr, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", sink.URL)))
	if status != http.StatusCreated {
		t.Fatalf("POST: %d", status)
	}
	sub := hdr.Get("Location")
	if status, _, _ := h.do(t, "dev", http.MethodPut, sub, marshal(t, subscription(ed+"/fsa", sink.URL+"/2"))); status != http.StatusNoContent {
		t.Fatalf("PUT: %d", status)
	}
	_ = h.srv.Close()
	h2 := newHarness(t, cfg)
	a := h2.srv.Admin()
	n, err := a.Get(ctx, ed+"/fsa")
	if err != nil {
		t.Fatal(err)
	}
	// The list now belongs to someone else.
	n.Owner = "00"
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := a.Get(ctx, sub); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the subscription was not cancelled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 || !strings.Contains(bodies[len(bodies)-1], "<status>1</status>") {
		t.Errorf("notifications %q", bodies)
	}
}

func TestDefaultNotificationURIAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://device.example:8443/ntfy": true,
		"https://192.168.1.20/ntfy":        true,
		"https://[2001:db8::1]/ntfy":       true,
		"https://localhost/ntfy":           false,
		"https://a.localhost./ntfy":        false,
		"https://127.0.0.1/ntfy":           false,
		"https://[::1]/ntfy":               false,
		"https://0.0.0.0/ntfy":             false,
		"https://169.254.169.254/latest":   false,
		"https://[fe80::1]/ntfy":           false,
		"https://224.0.0.251/ntfy":         false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := server.DefaultNotificationURIAllowed(u); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
	// A server with the default refuses a subscription to loopback.
	h := newHarness(t, server.Config{NotificationClient: &http.Client{}}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", "https://127.0.0.1/n"))); status != http.StatusForbidden {
		t.Errorf("a subscription to loopback: %d", status)
	}
}
