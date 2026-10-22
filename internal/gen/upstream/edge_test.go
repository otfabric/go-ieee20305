// SPDX-License-Identifier: MIT

package upstream_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
	"github.com/otfabric/go-ieee20305/internal/gen/upstream"
)

// roundTripFunc adapts a function to http.RoundTripper, so that a Client
// can be tested without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func bodyClient(body func() io.Reader) *upstream.Client {
	return &upstream.Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(body()), Request: r}, nil
	})}}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// zeros is an endless reader of zero bytes.
type zeros struct{}

func (zeros) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

func TestNewClient(t *testing.T) {
	c := upstream.NewClient(&manifest.Manifest{UserAgent: "ua"})
	if c.UserAgent != "ua" || c.HTTP == nil || c.HTTP.Timeout == 0 {
		t.Errorf("NewClient = %+v", c)
	}
}

func TestDownloadErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := bodyClient(func() io.Reader { return failingReader{} }).Download(ctx, "https://x/a"); err == nil {
		t.Error("a body read error was not reported")
	}
	if _, err := bodyClient(func() io.Reader { return zeros{} }).Download(ctx, "https://x/a"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized body: %v", err)
	}
	if _, err := bodyClient(func() io.Reader { return zeros{} }).Download(ctx, "https://x/\x7f"); err == nil {
		t.Error("a malformed URL was accepted")
	}
	failing := &upstream.Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no network")
	})}}
	if _, err := failing.Download(ctx, "https://x/a"); err == nil {
		t.Error("a transport error was not reported")
	}
	m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", URL: "https://x/a", Files: []manifest.File{{To: "a"}},
		Watch: &manifest.Watch{Page: "https://x/p", Pattern: "x", Probe: []string{"https://x/probe"}}}}}
	findings := failing.CheckUpdates(ctx, m)
	if len(findings) != 3 {
		t.Errorf("findings = %+v, want three errors", findings)
	}
	for _, f := range findings {
		if f.Kind != "error" {
			t.Errorf("finding %+v, want an error", f)
		}
	}
}

func TestScanSkipsMalformedLinks(t *testing.T) {
	c := bodyClient(func() io.Reader { return strings.NewReader(`<a href="%zz.zip">bad</a><a href="good.zip">ok</a>`) })
	m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", URL: "https://x/a", Files: []manifest.File{{To: "a"}},
		Watch: &manifest.Watch{Page: "https://x/p/", Pattern: `\.zip`}}}}
	var news []string
	for _, f := range c.CheckUpdates(context.Background(), m) {
		if f.Kind == "new-release" {
			news = append(news, f.Detail)
		}
	}
	if len(news) != 1 || !strings.Contains(news[0], "https://x/p/good.zip") {
		t.Errorf("new releases = %q", news)
	}
}

func TestVerifyMissingDirectory(t *testing.T) {
	m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", Files: []manifest.File{{From: "d/", To: "d/"}}}}}
	if err := upstream.Verify(m, t.TempDir()); err == nil {
		t.Error("Verify accepted a missing directory")
	}
}

// zipEntry is one member of a hand-made archive.
type zipEntry struct {
	name    string
	content string
	method  uint16
	size    uint64 // declared uncompressed size when not zero
}

func rawZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, CRC32: crc32.ChecksumIEEE([]byte(e.content)),
			CompressedSize64: uint64(len(e.content)), UncompressedSize64: uint64(len(e.content))}
		if e.size != 0 {
			h.UncompressedSize64 = e.size
		}
		w, err := zw.CreateRaw(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, e.content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchArchiveErrors(t *testing.T) {
	sum := upstream.SHA256([]byte("<x/>"))
	tests := map[string]struct {
		archive []byte
		files   []manifest.File
	}{
		"not a zip":        {[]byte("plain"), []manifest.File{{From: "x", To: "x", SHA256: sum}}},
		"missing member":   {rawZip(t, zipEntry{name: "y", content: "<x/>"}), []manifest.File{{From: "x", To: "x", SHA256: sum}}},
		"oversized member": {rawZip(t, zipEntry{name: "x", content: "<x/>", size: 1 << 40}), []manifest.File{{From: "x", To: "x", SHA256: sum}}},
		"unknown method":   {rawZip(t, zipEntry{name: "x", content: "<x/>", method: 99}), []manifest.File{{From: "x", To: "x", SHA256: sum}}},
		"member changed":   {rawZip(t, zipEntry{name: "x", content: "<y/>"}), []manifest.File{{From: "x", To: "x", SHA256: sum}}},
		"escaping member":  {rawZip(t, zipEntry{name: "d/../../x", content: "<x/>"}), []manifest.File{{From: "d/", To: "d/"}}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := bodyClient(func() io.Reader { return bytes.NewReader(tt.archive) })
			m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", URL: "https://x/a.zip", SHA256: upstream.SHA256(tt.archive), Archive: "zip", Files: tt.files}}}
			if err := c.Fetch(context.Background(), m, t.TempDir(), io.Discard); err == nil {
				t.Error("Fetch succeeded")
			}
		})
	}
}

func TestFetchCannotWrite(t *testing.T) {
	data := []byte("<x/>")
	c := bodyClient(func() io.Reader { return bytes.NewReader(data) })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", URL: "https://x/a", SHA256: upstream.SHA256(data),
		Files: []manifest.File{{To: "blocker/x", SHA256: upstream.SHA256(data)}}}}}
	if err := c.Fetch(context.Background(), m, dir, io.Discard); err == nil {
		t.Error("Fetch wrote below a regular file")
	}
}

func TestSourceCompleteNeedsDirectory(t *testing.T) {
	archive := rawZip(t, zipEntry{name: "d/a", content: "a"})
	c := bodyClient(func() io.Reader { return bytes.NewReader(archive) })
	dir := t.TempDir()
	// d is a file where the source expects a directory, so it is fetched
	// and then cannot be written.
	if err := os.WriteFile(filepath.Join(dir, "d"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Sources: []manifest.Source{{ID: "a", URL: "https://x/a.zip", SHA256: upstream.SHA256(archive), Archive: "zip",
		Files: []manifest.File{{From: "d/", To: "d/"}}}}}
	if err := c.Fetch(context.Background(), m, dir, io.Discard); err == nil {
		t.Error("Fetch succeeded with a file in place of a directory")
	}
	if err := os.Remove(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := c.Fetch(context.Background(), m, dir, &log); err != nil || !strings.Contains(log.String(), "fetched") {
		t.Fatalf("Fetch = %v, %s", err, log.String())
	}
	log.Reset()
	if err := c.Fetch(context.Background(), m, dir, &log); err != nil || !strings.Contains(log.String(), "up to date") {
		t.Errorf("second Fetch = %v, %s", err, log.String())
	}
}
