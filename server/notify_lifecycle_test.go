// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// touch puts the node at href again, which notifies its subscriptions.
func touch(t *testing.T, h *harness, href string) {
	t.Helper()
	ctx := context.Background()
	n, err := h.srv.Admin().Get(ctx, href)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.Admin().Put(ctx, n); err != nil {
		t.Fatal(err)
	}
}

// subscribe posts a subscription of the device name to resource and
// returns its href.
func subscribe(t *testing.T, h *harness, name, list, resource, uri string) string {
	t.Helper()
	status, hdr, data := h.do(t, name, http.MethodPost, list, marshal(t, subscription(resource, uri)))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: %d %s", list, status, data)
	}
	return hdr.Get("Location")
}

// The notifier bounds each attempt itself: a listener that never answers
// holds a worker only for the attempt timeout, even with a
// NotificationClient without Timeout, so the notifications of other
// devices still go out.
func TestNotificationAttemptTimeout(t *testing.T) {
	defer server.SetNotificationAttemptTimeout(100 * time.Millisecond)()
	var held, released, ok atomic.Int32
	lst := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasPrefix(r.URL.Path, "/stall") {
			held.Add(1)
			<-r.Context().Done()
			released.Add(1)
			return
		}
		ok.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer lst.Close()
	c := lst.Client()
	c.Timeout = 0
	cfg := server.Config{NotificationClient: c, NotificationURIAllowed: allowAll, NotificationWorkers: 1,
		NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 1, NoJitter: true}}
	h := newHarness(t, cfg, "a", "b")
	ctx := context.Background()
	edA, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("a")})
	if err != nil {
		t.Fatal(err)
	}
	edB, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("b")})
	if err != nil {
		t.Fatal(err)
	}
	subA := subscribe(t, h, "a", edA+"/sub", edA, lst.URL+"/stall")
	subscribe(t, h, "b", edB+"/sub", edB, lst.URL+"/ok")
	touch(t, h, edA)
	waitFor(t, func() bool { return held.Load() >= 1 })
	touch(t, h, edB)
	waitFor(t, func() bool { return ok.Load() >= 1 })
	waitFor(t, func() bool { return released.Load() >= 1 })
	// The stalled attempt failed: the subscription is cancelled.
	waitFor(t, gone(h, subA))
}

// A deleted subscription is notified no more: its worker stops, also while
// it waits to send again.
func TestDeletedSubscriptionStopsNotifying(t *testing.T) {
	var calls atomic.Int32
	lst := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer lst.Close()
	for _, wait := range []time.Duration{2 * time.Millisecond, time.Hour} {
		t.Run(fmt.Sprint(wait), func(t *testing.T) {
			calls.Store(0)
			h := newHarness(t, server.Config{NotificationClient: lst.Client(), NotificationURIAllowed: allowAll,
				NotificationRetry: ieee20305.ExponentialBackoff{Initial: wait, Max: wait, Attempts: -1, NoJitter: true}}, "dev")
			ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
			if err != nil {
				t.Fatal(err)
			}
			sub := subscribe(t, h, "dev", ed+"/sub", ed, lst.URL+"/n")
			touch(t, h, ed)
			waitFor(t, func() bool { return calls.Load() >= 2 || wait == time.Hour && calls.Load() >= 1 })
			if status, _, _ := h.do(t, "dev", http.MethodDelete, sub, nil); status != http.StatusNoContent {
				t.Fatalf("DELETE: %d", status)
			}
			// At most the attempt under way when the subscription was
			// deleted is still sent.
			atDelete := calls.Load()
			time.Sleep(60 * time.Millisecond)
			if got := calls.Load(); got > atDelete+1 {
				t.Errorf("%d notifications after the subscription was deleted", got-atDelete)
			}
			// The worker ended: closing does not wait for it.
			done := make(chan struct{})
			go func() { _ = h.srv.Close(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Close waits for the worker of a deleted subscription")
			}
		})
	}
}

// A device that puts its subscription again while it is being notified
// changes where the next attempt goes; the worker reads a copy of the
// subscription (run with -race).
func TestSubscriptionPutDuringDelivery(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]bool{}
	lst := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		paths[r.URL.Path] = true
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer lst.Close()
	h := newHarness(t, server.Config{NotificationClient: lst.Client(), NotificationURIAllowed: allowAll,
		NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Max: time.Millisecond, Attempts: -1, NoJitter: true}}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	sub := subscribe(t, h, "dev", ed+"/sub", ed, lst.URL+"/n")
	touch(t, h, ed)
	const puts = 20
	for i := range puts {
		if status, _, data := h.do(t, "dev", http.MethodPut, sub, marshal(t, subscription(ed, fmt.Sprintf("%s/n%d", lst.URL, i)))); status/100 != 2 {
			t.Fatalf("PUT %d: %d %s", i, status, data)
		}
	}
	last := fmt.Sprintf("/n%d", puts-1)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return paths[last] })
}
