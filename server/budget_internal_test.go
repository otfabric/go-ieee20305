// SPDX-License-Identifier: MIT

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
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

// TestRequestBodyAllocationBudget: what the server allocates to read the
// body of a request is bounded by Config.MaxRequestBytes, whatever the
// length of the body and the length it declares: a body beyond the limit
// is refused with 413, and one within it that is no document with 400.
func TestRequestBodyAllocationBudget(t *testing.T) {
	const limit = 64 << 10
	// Reading up to the limit grows a buffer to it, and a body at the limit
	// is decoded: the budget leaves room for both, far below the longer
	// bodies.
	const budget = 16*limit + 64<<10
	s, err := New(Config{MaxRequestBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, tt := range []struct {
		name           string
		size, declared int64
		status         int
	}{
		{"empty", 0, 0, http.StatusBadRequest},
		{"one byte", 1, 1, http.StatusBadRequest},
		{"limit-1", limit - 1, limit - 1, http.StatusBadRequest},
		{"the limit", limit, limit, http.StatusBadRequest},
		{"limit+1", limit + 1, limit + 1, http.StatusRequestEntityTooLarge},
		{"64 times the limit", 64 * limit, 64 * limit, http.StatusRequestEntityTooLarge},
		{"2^32 bytes", 1 << 32, 1 << 32, http.StatusRequestEntityTooLarge},
		{"no end, 2^62 bytes declared", 1 << 62, 1 << 62, http.StatusRequestEntityTooLarge},
		{"no end, no length declared", 1 << 62, -1, http.StatusRequestEntityTooLarge},
		{"one byte, 2^62 declared", 1, 1 << 62, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decode := func() error {
				req := httptest.NewRequest(http.MethodPost, "/edev/1/lel", io.LimitReader(filler{}, tt.size))
				req.ContentLength = tt.declared
				return s.decodeBody(httptest.NewRecorder(), &Request{Request: req, Principal: devicePrincipal()})
			}
			if he := asHTTPError(decode()); he.Status != tt.status {
				t.Fatalf("status %d, want %d", he.Status, tt.status)
			}
			got := allocated(5, func() { _ = decode() })
			t.Logf("%d bytes allocated; budget %d", got, budget)
			if got > budget {
				t.Errorf("%d bytes allocated for a body of %d bytes, %d declared; budget %d (MaxRequestBytes %d)",
					got, tt.size, tt.declared, budget, limit)
			}
		})
	}
}

// TestListClaimAllocationBudget: a list document that claims 2^32-1 items
// and holds none is decoded without allocating for the claim.
func TestListClaimAllocationBudget(t *testing.T) {
	const budget = 64 << 10
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	doc := `<MirrorMeterReadingList xmlns="` + ieee20305.Namespace + `" all="4294967295" results="4294967295"/>`
	decode := func() (*Request, error) {
		r := &Request{Request: httptest.NewRequest(http.MethodPost, "/mup/1", strings.NewReader(doc)), Principal: devicePrincipal()}
		return r, s.decodeBody(httptest.NewRecorder(), r)
	}
	r, err := decode()
	if l, ok := r.Doc.(*ieee20305.MirrorMeterReadingList); err != nil || !ok || len(l.MirrorMeterReading) != 0 {
		t.Fatalf("decoded %T, %v", r.Doc, err)
	}
	got := allocated(20, func() { _, _ = decode() })
	t.Logf("%d bytes allocated; budget %d", got, budget)
	if got > budget {
		t.Errorf("%d bytes allocated for a list that claims 2^32-1 items; budget %d", got, budget)
	}
}
