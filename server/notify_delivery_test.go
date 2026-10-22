// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// sink is a receiver of notifications that answers with status.
type sink struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	bodies []string
}

func newSink(t *testing.T, status int) *sink {
	s := &sink{status: status}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(data))
		st := s.status
		s.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sink) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func notifyConfig(s *sink) server.Config {
	return server.Config{NotificationClient: s.Client(), NotificationURIAllowed: allowAll,
		NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 1, NoJitter: true}}
}

// gone reports whether the node at href is gone.
func gone(h *harness, href string) func() bool {
	return func() bool { _, err := h.srv.Admin().Get(context.Background(), href); return err != nil }
}

// A subscribed resource whose value is gone ends its subscription with
// status 4; a hook answers the resource of a notification, which gets the
// href of the subscribed resource.
func TestNotificationOfResourceWithoutValue(t *testing.T) {
	snk := newSink(t, http.StatusCreated)
	cfg := notifyConfig(snk)
	cfg.Hooks = map[string]server.Hooks{"EndDevice": {Get: func(context.Context, *server.Request) (any, error) {
		return &ieee20305.EndDevice{}, nil
	}}}
	h := newHarness(t, cfg, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	status, hdr, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, snk.URL)))
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, data)
	}
	sub := hdr.Get("Location")
	// A PUT of the subscription the server refuses.
	bad := subscription(ed, snk.URL)
	bad.Encoding = 1
	if status, _, _ := h.do(t, "dev", http.MethodPut, sub, marshal(t, bad)); status != http.StatusBadRequest {
		t.Errorf("PUT of a subscription with encoding 1: %d", status)
	}
	n, err := a.Get(ctx, ed)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(snk.received()) >= 1 })
	if b := snk.received()[0]; !strings.Contains(b, `href="`+ed+`"`) {
		t.Errorf("a notification of a hooked resource: %s", b)
	}
	// The EndDevice loses its value.
	if err := a.Put(ctx, &server.Node{Href: ed, Resource: "EndDevice", Owner: h.lfdi("dev")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, gone(h, sub))
	got := snk.received()
	if !strings.Contains(got[len(got)-1], "<status>4</status>") {
		t.Errorf("notifications %q", got)
	}
	// A Subscription node that holds no Subscription is not indexed.
	if err := a.Put(ctx, &server.Node{Href: "/odd/sub", Resource: "Subscription", Value: &ieee20305.Time{}}); err != nil {
		t.Fatal(err)
	}
}

// A change of an item of a list that is no part of it, but a member,
// notifies the subscriptions of the list.
func TestNotificationOfMember(t *testing.T) {
	snk := newSink(t, http.StatusCreated)
	h := newHarness(t, notifyConfig(snk), "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, &server.Node{Href: "/fsa", Resource: "FunctionSetAssignmentsList", Value: &ieee20305.FunctionSetAssignmentsList{}}); err != nil {
		t.Fatal(err)
	}
	fsa, err := a.Create(ctx, "/fsa", examples.FunctionSetAssignments(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", snk.URL))); status != http.StatusCreated {
		t.Fatalf("POST: %d", status)
	}
	if err := a.AddMember(ctx, ed+"/fsa", fsa); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(snk.received()) >= 1 })
	n, err := a.Get(ctx, fsa)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(snk.received()) >= 2 })
}

// A subscription whose notificationURI makes no request is cancelled.
func TestNotificationToBadURI(t *testing.T) {
	snk := newSink(t, http.StatusCreated)
	h := newHarness(t, notifyConfig(snk), "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := a.Create(ctx, ed+"/sub", subscription(ed, "https://bad\x7f/n"), h.lfdi("dev"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.Get(ctx, ed)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	waitFor(t, gone(h, sub))
}

// Close stops a delivery that waits to be tried again.
func TestCloseDuringRetry(t *testing.T) {
	snk := newSink(t, http.StatusServiceUnavailable)
	cfg := notifyConfig(snk)
	cfg.NotificationRetry = ieee20305.ExponentialBackoff{Initial: time.Hour, Max: time.Hour, Attempts: -1, NoJitter: true}
	cfg.Metrics = server.NopMetrics{}
	h := newHarness(t, cfg, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, snk.URL))); status != http.StatusCreated {
		t.Fatalf("POST: %d", status)
	}
	n, err := a.Get(ctx, ed)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(snk.received()) >= 1 })
	done := make(chan struct{})
	go func() {
		_ = h.srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close waits for a retry")
	}
}
