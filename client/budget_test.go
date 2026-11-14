// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// filler is a body without end.
type filler struct{}

func (filler) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// allocated returns the bytes f allocates, averaged over runs calls.
func allocated(runs int, f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
}

// answers is a transport that answers every request 200 with a body of
// size bytes that declares declared bytes, or with the document doc.
type answers struct {
	size, declared int64
	doc            string
}

func (a answers) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {client.MediaType}},
		ContentLength: a.declared, Request: req, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	if a.doc != "" {
		resp.Body, resp.ContentLength = io.NopCloser(strings.NewReader(a.doc)), int64(len(a.doc))
	} else {
		resp.Body = io.NopCloser(io.LimitReader(filler{}, a.size))
	}
	return resp, nil
}

// TestResponseBodyAllocationBudget: what the client allocates to read a
// response is bounded by Config.MaxResponseBytes, whatever the length of
// the body and the length it declares: a body beyond the limit, and one
// within it that is no document, fail with ErrProtocol.
func TestResponseBodyAllocationBudget(t *testing.T) {
	const limit = 64 << 10
	// Reading up to the limit grows a buffer to it, and a body at the limit
	// is decoded: the budget leaves room for both, far below the longer
	// bodies.
	const budget = 16*limit + 64<<10
	for _, tt := range []struct {
		name           string
		size, declared int64
	}{
		{"empty", 0, 0},
		{"one byte", 1, 1},
		{"limit-1", limit - 1, limit - 1},
		{"the limit", limit, limit},
		{"limit+1", limit + 1, limit + 1},
		{"64 times the limit", 64 * limit, 64 * limit},
		{"2^32 bytes", 1 << 32, 1 << 32},
		{"no end, 2^62 bytes declared", 1 << 62, 1 << 62},
		{"no end, no length declared", 1 << 62, -1},
		{"one byte, 2^62 declared", 1, 1 << 62},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, err := client.New(client.Config{Server: "https://server.example", MaxResponseBytes: limit,
				HTTPClient: &http.Client{Transport: answers{size: tt.size, declared: tt.declared}}})
			if err != nil {
				t.Fatal(err)
			}
			get := func() error { return c.Get(context.Background(), "/tm", &ieee20305.Time{}) }
			if err := get(); !errors.Is(err, ieee20305.ErrProtocol) {
				t.Fatalf("Get: %v, want ErrProtocol", err)
			}
			got := allocated(5, func() { _ = get() })
			t.Logf("%d bytes allocated; budget %d", got, budget)
			if got > budget {
				t.Errorf("%d bytes allocated for a response of %d bytes, %d declared; budget %d (MaxResponseBytes %d)",
					got, tt.size, tt.declared, budget, limit)
			}
		})
	}
}

// TestListClaimAllocationBudget: a list that claims 2^32-1 items and holds
// none is read without allocating for the claim, and ends.
func TestListClaimAllocationBudget(t *testing.T) {
	const budget = 64 << 10
	doc := `<EndDeviceList xmlns="` + ieee20305.Namespace + `" href="/edev" all="4294967295" results="4294967295"/>`
	c, err := client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: answers{doc: doc}}})
	if err != nil {
		t.Fatal(err)
	}
	items := 0
	iterate := func() error {
		return c.Iterate(context.Background(), "/edev", 0, func(any) error { items++; return nil })
	}
	if err := iterate(); items != 0 || (err != nil && !errors.Is(err, ieee20305.ErrProtocol)) {
		t.Fatalf("Iterate: %d items, %v", items, err)
	}
	got := allocated(20, func() { _ = iterate() })
	t.Logf("%d bytes allocated; budget %d", got, budget)
	if got > budget {
		t.Errorf("%d bytes allocated for a list that claims 2^32-1 items; budget %d", got, budget)
	}
}

// TestNotificationAllocationBudget: what a NotificationHandler allocates to
// read a notification is bounded by NotificationConfig.MaxBytes: a body
// beyond it is refused with 413, and one within it that is no Notification
// with 400.
func TestNotificationAllocationBudget(t *testing.T) {
	const limit = 64 << 10
	const budget = 16*limit + 64<<10
	h, err := client.NewNotificationHandler(func(context.Context, *ieee20305.Notification) error {
		t.Error("a body that is no Notification was handled")
		return nil
	}, client.NotificationConfig{MaxBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name           string
		size, declared int64
		status         int
	}{
		{"empty", 0, 0, http.StatusBadRequest},
		{"limit-1", limit - 1, limit - 1, http.StatusBadRequest},
		{"the limit", limit, limit, http.StatusBadRequest},
		{"limit+1", limit + 1, limit + 1, http.StatusRequestEntityTooLarge},
		{"64 times the limit", 64 * limit, 64 * limit, http.StatusRequestEntityTooLarge},
		{"no end, 2^62 bytes declared", 1 << 62, 1 << 62, http.StatusRequestEntityTooLarge},
		{"no end, no length declared", 1 << 62, -1, http.StatusRequestEntityTooLarge},
		{"one byte, 2^62 declared", 1, 1 << 62, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			serve := func() int {
				req := httptest.NewRequest(http.MethodPost, "/ntfy", io.LimitReader(filler{}, tt.size))
				req.ContentLength = tt.declared
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				return w.Code
			}
			if status := serve(); status != tt.status {
				t.Fatalf("status %d, want %d", status, tt.status)
			}
			got := allocated(5, func() { serve() })
			t.Logf("%d bytes allocated; budget %d", got, budget)
			if got > budget {
				t.Errorf("%d bytes allocated for a notification of %d bytes, %d declared; budget %d (MaxBytes %d)",
					got, tt.size, tt.declared, budget, limit)
			}
		})
	}
}
