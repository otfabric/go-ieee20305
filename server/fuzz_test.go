// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// FuzzHandler sends arbitrary requests to a server with every resource: it
// never panics, and never answers 5xx.
func FuzzHandler(f *testing.F) {
	for _, s := range []string{"/edev", "/dcap", "/tm", "/derp/1/derc", "/mup/1"} {
		f.Add("GET", s+"?s=0&l=3", []byte(nil))
	}
	ed, _ := ieee20305.Marshal(examples.EndDevice(1))
	f.Add("POST", "/edev", ed)
	f.Add("PUT", "/edev/1", ed)
	f.Add("PUT", "/edev/1", []byte("<EndDevice xmlns=\"urn:ieee:std:2030.5:ns\"><sFDI>x</sFDI></EndDevice>"))
	f.Add("DELETE", "/edev/1", []byte(nil))
	h := newHarness(f, server.Config{Logger: ieee20305.NewSlogLogger(slog.NewTextHandler(&bytes.Buffer{}, nil))}, "dev")
	seedAll(f, h, h.lfdi("dev"))
	f.Fuzz(func(t *testing.T, method, path string, body []byte) {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \r\n#") {
			return
		}
		req := httptest.NewRequest(http.MethodGet, "/", bytes.NewReader(body))
		req.Method = method
		req.URL.Path = path
		req.Header.Set("X-Test-Device", "dev")
		w := httptest.NewRecorder()
		h.srv.ServeHTTP(w, req)
		if w.Code >= 500 {
			t.Errorf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	})
}
