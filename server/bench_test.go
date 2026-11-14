// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// sinkBody keeps the last answer of a benchmark, so that the work that
// makes it is not removed as unused.
var sinkBody []byte

// BenchmarkListGET answers the GET of a page of 255 items of a list of
// 1000 EndDevices: the items are read, sorted, paged and encoded.
func BenchmarkListGET(b *testing.B) {
	h := newHarness(b, server.Config{}, "dev")
	ctx := context.Background()
	if err := h.srv.Admin().Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		ed := &ieee20305.EndDevice{}
		ed.SFDI = ieee20305.SFDIType(1000 - i)
		if _, err := h.srv.Admin().Create(ctx, "/edev", ed, ""); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/edev?s=100&l=255", nil)
		req.Header.Set("X-Test-Device", "dev")
		w := httptest.NewRecorder()
		h.srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("GET: %d", w.Code)
		}
		sinkBody = w.Body.Bytes()
	}
	if len(sinkBody) == 0 {
		b.Fatal("no body")
	}
}
