// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// The query parameters at and beyond their limits: out of range is 400, a
// huge l is capped, a huge s is an empty page.
func TestHostileQueryParameters(t *testing.T) {
	h := newHarness(t, server.Config{MaxLimit: 2}, "dev")
	devices(t, h, "", 1, 2, 3)
	for _, c := range []struct {
		query   string
		status  int
		results int
		all     uint32
	}{
		{"?s=-1", http.StatusBadRequest, 0, 3},
		{"?s=4294967296", http.StatusBadRequest, 0, 3},
		{"?s=99999999999999999999999", http.StatusBadRequest, 0, 3},
		{"?l=-1", http.StatusBadRequest, 0, 3},
		{"?l=4294967296", http.StatusBadRequest, 0, 3},
		{"?l=4294967295", http.StatusOK, 2, 3},
		{"?s=4294967295&l=4294967295", http.StatusOK, 0, 3},
		{"?s=2&l=4294967295", http.StatusOK, 1, 3},
		{"?l=0", http.StatusOK, 0, 3},
		{"?s=1&s=2", http.StatusBadRequest, 0, 3},
		{"?s=+1", http.StatusBadRequest, 0, 3},
		{"?s=0x1", http.StatusBadRequest, 0, 3},
		{"?s=%00", http.StatusBadRequest, 0, 3},
		{"?sFDI=18446744073709551616", http.StatusBadRequest, 0, 3},
		{"?sFDI=18446744073709551615&l=5", http.StatusOK, 0, 0},
		{"?" + strings.Repeat("x=1&", 5000) + "l=2", http.StatusOK, 2, 3},
	} {
		status, _, data := h.do(t, "dev", http.MethodGet, "/edev"+c.query, nil)
		if status != c.status {
			t.Errorf("%.40s: %d, want %d", c.query, status, c.status)
			continue
		}
		if status == http.StatusOK {
			l := decode[ieee20305.EndDeviceList](t, data)
			if len(l.EndDevice) != c.results || uint32(l.All) != c.all {
				t.Errorf("%.40s: %d items of %d, want %d of %d", c.query, len(l.EndDevice), l.All, c.results, c.all)
			}
		}
	}
}

// The a parameter of an event list at the ends of int64.
func TestHostileAfterParameter(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.NewList(ctx, "DERControlList", prog+"/derc", prog, "DERControlListLink", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), ""); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{
		"?a=-9223372036854775808&l=5": http.StatusOK,
		"?a=9223372036854775807&l=5":  http.StatusOK,
		"?a=9223372036854775808":      http.StatusBadRequest,
		"?a=1e3":                      http.StatusBadRequest,
	} {
		if status, _, _ := h.do(t, "dev", http.MethodGet, prog+"/derc"+q, nil); status != want {
			t.Errorf("%s: %d, want %d", q, status, want)
		}
	}
}

// Bodies that are too large, malformed or deeply nested are refused, and
// the server goes on serving.
func TestHostileBodies(t *testing.T) {
	h := newHarness(t, server.Config{MaxRequestBytes: 64 << 10, NotificationClient: &http.Client{}, NotificationURIAllowed: allowAll}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	ns := `<Subscription xmlns="urn:ieee:std:2030.5:ns">`
	for name, c := range map[string]struct {
		body   string
		status []int
	}{
		"oversized":   {ns + strings.Repeat(" ", 128<<10) + "</Subscription>", []int{http.StatusRequestEntityTooLarge}},
		"truncated":   {ns + "<notificationURI>https://x", []int{http.StatusBadRequest}},
		"not xml":     {"\x00\xff\xfe", []int{http.StatusBadRequest}},
		"empty":       {"", []int{http.StatusBadRequest}},
		"wrong root":  {`<EndDevice xmlns="urn:ieee:std:2030.5:ns"/>`, []int{http.StatusBadRequest}},
		"nested":      {ns + strings.Repeat("<x>", 5000) + strings.Repeat("</x>", 5000) + "</Subscription>", []int{http.StatusBadRequest}},
		"nested deep": {strings.Repeat(ns, 15000), []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge}},
		"entity":      {`<!DOCTYPE s [<!ENTITY a "aaaaaaaaaa">]>` + ns + "<notificationURI>&a;</notificationURI></Subscription>", []int{http.StatusBadRequest}},
	} {
		status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", []byte(c.body))
		ok := false
		for _, s := range c.status {
			ok = ok || status == s
		}
		if !ok {
			t.Errorf("%s: %d %.100s, want %v", name, status, data, c.status)
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		if status, _, _ := h.do(t, "dev", method, ed, []byte(strings.Repeat("<", 200<<10))); status != http.StatusRequestEntityTooLarge && status != http.StatusMethodNotAllowed {
			t.Errorf("%s of an oversized EndDevice: %d", method, status)
		}
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, ed, nil); status != http.StatusOK {
		t.Errorf("GET after the hostile bodies: %d", status)
	}
}

// A device cannot grow its SubscriptionList without end: the oldest
// subscriptions beyond MaxListItems go.
func TestHostileSubscriptionFlood(t *testing.T) {
	snk := newSink(t, http.StatusCreated)
	cfg := notifyConfig(snk)
	cfg.MaxListItems = 5
	h := newHarness(t, cfg, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, snk.URL+"/n"))); status != http.StatusCreated {
			t.Fatalf("POST: %d %s", status, data)
		}
	}
	_, _, data := h.do(t, "dev", http.MethodGet, ed+"/sub?l=255", nil)
	if l := decode[ieee20305.SubscriptionList](t, data); l.All != 5 {
		t.Errorf("%d subscriptions after 20 with MaxListItems 5", l.All)
	}
}

// The default policy refuses every spelling of a loopback or link-local
// address that the dialer reaches.
func TestHostileNotificationURISpellings(t *testing.T) {
	for _, s := range []string{
		"https://[fe80::1%25en0]/n",
		"https://[fe80::a9fe:a9fe%25eth0]/n",
		"https://127.1/n",
		"https://2130706433/n",
		"https://0x7f000001/n",
		"https://0/n",
	} {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		if server.DefaultNotificationURIAllowed(u) {
			t.Errorf("%s is allowed", s)
		}
	}
}

// A receiver of notifications must not redirect the server's POST, which
// carries the server's certificate, to a host the policy refuses.
func TestHostileNotificationRedirect(t *testing.T) {
	var internal atomic.Int64
	inner := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internal.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(inner.Close)
	var redirected atomic.Int64
	outer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		http.Redirect(w, r, inner.URL+"/metadata", http.StatusTemporaryRedirect)
	}))
	outer.TLS = inner.TLS.Clone()
	outer.StartTLS()
	t.Cleanup(outer.Close)
	outerHost := strings.TrimPrefix(outer.URL, "https://")
	h := newHarness(t, server.Config{NotificationClient: inner.Client(),
		NotificationURIAllowed: func(u *url.URL) bool { return u.Host == outerHost },
		NotificationRetry:      ieee20305.NoRetry()}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	status, hdr, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", outer.URL+"/n")))
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, data)
	}
	sub := hdr.Get("Location")
	if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	// The notification ends the subscription either way: delivered, or
	// refused as a redirect.
	waitFor(t, func() bool { return redirected.Load() > 0 })
	deadline := time.Now().Add(5 * time.Second)
	for internal.Load() == 0 && time.Now().Before(deadline) {
		if _, err := h.srv.Admin().Get(ctx, sub); err != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if n := internal.Load(); n > 0 {
		t.Errorf("the notification was redirected to a host the policy refuses (%d POSTs)", n)
	}
}

// Close ends the notifications in flight to a receiver that never answers,
// or answers with a body that never ends.
func TestHostileReceiverAtClose(t *testing.T) {
	for _, mode := range []string{"stall", "endless"} {
		t.Run(mode, func(t *testing.T) {
			got := make(chan struct{}, 1)
			release := make(chan struct{})
			snk := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case got <- struct{}{}:
				default:
				}
				if mode == "endless" {
					w.WriteHeader(http.StatusCreated)
					chunk := []byte(strings.Repeat("x", 4096))
					for {
						if _, err := w.Write(chunk); err != nil {
							return
						}
						select {
						case <-release:
							return
						default:
						}
					}
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(snk.Close)
			t.Cleanup(func() { close(release) })
			cfg := server.Config{NotificationClient: snk.Client(), NotificationURIAllowed: allowAll,
				NotificationRetry: ieee20305.NoRetry()}
			h := newHarness(t, cfg, "dev")
			ctx := context.Background()
			ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
			if err != nil {
				t.Fatal(err)
			}
			if status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", snk.URL+"/n"))); status != http.StatusCreated {
				t.Fatalf("POST: %d %s", status, data)
			}
			if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("no notification")
			}
			done := make(chan error, 1)
			go func() { done <- h.srv.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Close: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close waits for a receiver that never answers")
			}
		})
	}
}
