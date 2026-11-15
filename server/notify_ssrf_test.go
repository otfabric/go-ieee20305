// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// A listener's redirect is not followed: a 307 or 308 would send the POST,
// with the server's certificate, to a URL NotificationURIAllowed never
// checked. The 3xx fails the notification, which ends the subscription.
func TestNotificationRedirectNotFollowed(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	sink := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/n" {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer sink.Close()
	h := newHarness(t, server.Config{NotificationClient: sink.Client(), NotificationURIAllowed: allowAll,
		NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 1}}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	_, hdr, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", sink.URL+"/n")))
	sub := hdr.Get("Location")
	if sub == "" {
		t.Fatal("no subscription")
	}
	if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := h.srv.Admin().Get(ctx, sub); return err != nil })
	mu.Lock()
	defer mu.Unlock()
	if hits["/n"] == 0 || hits["/elsewhere"] != 0 {
		t.Errorf("requests by path = %v, want /n only", hits)
	}
}

// The configuration's NotificationClient is not changed by the server.
func TestNotificationClientUnchanged(t *testing.T) {
	c := &http.Client{}
	h := newHarness(t, server.Config{NotificationClient: c, NotificationURIAllowed: allowAll}, "dev")
	_ = h
	if c.CheckRedirect != nil {
		t.Error("the server set CheckRedirect of the configured client")
	}
}

// The notificationURI is checked again when a notification is sent: a
// persistent store may hold subscriptions that an earlier configuration
// allowed. The refused one is cancelled without a request.
func TestNotificationURIRecheckedAfterRestart(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	sink := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer sink.Close()
	store := server.NewMemStore(nil)
	cfg := server.Config{Store: store, NotificationClient: sink.Client(), NotificationURIAllowed: allowAll,
		NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 1}}
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
	_ = h.srv.Close()

	cfg.NotificationURIAllowed = func(*url.URL) bool { return false }
	h2 := newHarness(t, cfg)
	if _, err := h2.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := h2.srv.Admin().Get(ctx, sub); return err != nil })
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Errorf("%d notifications sent to a refused notificationURI", calls)
	}
}

// Names whose last label is a number, names with an empty label, names of
// loopback, and IPv6 literals that embed a refused IPv4 address are
// refused; ordinary names, and routable literals with or without a zone,
// are not.
func TestDefaultNotificationURIAllowedSpellings(t *testing.T) {
	for host, want := range map[string]bool{
		"listener.example":     true,
		"10.0.0.7":             true,
		"[2001:db8::1]":        true,
		"1e100.net":            true,
		"host.0x":              false,
		"a.0x1f":               false,
		"10.1":                 false,
		"[::1%25lo]":           false,
		"[ff02::1%25eth0]":     false,
		"[::ffff:127.0.0.1]":   false,
		"[::ffff:169.254.1.1]": false,
		// IPv6 forms that embed an IPv4 address.
		"[::7f00:1]":           false,
		"[::ffff:0:7f00:1]":    false,
		"[64:ff9b::7f00:1]":    false,
		"[64:ff9b::a9fe:a9fe]": false,
		"[2002:7f00:1::]":      false,
		"[2002:a9fe:a9fe::1]":  false,
		"[64:ff9b::a00:7]":     true,
		"[2002:a00:7::1]":      true,
		"[::a00:7]":            true,
		"[::ffff:0:a00:7]":     true,
		// Empty labels and trailing dots.
		"localhost..":        false,
		"127.0.0.1..":        false,
		"listener.example..": true,
		"listener..example":  false,
		".listener.example":  false,
		// Names of loopback in hosts files.
		"ip6-localhost":           false,
		"ip6-loopback":            false,
		"localhost.localdomain":   false,
		"localhost6":              false,
		"localhost6.localdomain6": false,
	} {
		u, err := url.Parse("https://" + host + "/n")
		if err != nil {
			t.Fatal(err)
		}
		if got := server.DefaultNotificationURIAllowed(u); got != want {
			t.Errorf("%s: allowed %v, want %v", host, got, want)
		}
	}
}
