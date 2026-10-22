// SPDX-License-Identifier: MIT

package chaos

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// echo answers every request with body and counts the requests.
type echo struct {
	mu    sync.Mutex
	calls int
	body  string
}

func (e *echo) RoundTrip(r *http.Request) (*http.Response, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": {"4"}},
		Body: io.NopCloser(strings.NewReader(e.body)), Request: r}, nil
}

func newRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://example.invalid/x", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestTransportFaults(t *testing.T) {
	tests := []struct {
		name   string
		faults Faults
		check  func(t *testing.T, resp *http.Response, err error, base *echo)
	}{
		{"none", Faults{}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Errorf("= %v, %v", resp, err)
			}
		}},
		{"reset", Faults{Reset: 1}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			if !errors.Is(err, ErrInjected) || base.calls != 0 {
				t.Errorf("= %v, %d calls", err, base.calls)
			}
		}},
		{"unavailable", Faults{Unavailable: 1}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			if err != nil || resp.StatusCode != http.StatusServiceUnavailable || base.calls != 0 {
				t.Errorf("= %v, %v", resp, err)
			}
		}},
		{"lost", Faults{Lost: 1}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			if !errors.Is(err, ErrInjected) || base.calls != 1 {
				t.Errorf("= %v, %d calls", err, base.calls)
			}
		}},
		{"replay", Faults{Replay: 1}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			if err != nil || base.calls != 2 {
				t.Errorf("= %v, %d calls", err, base.calls)
			}
		}},
		{"truncate", Faults{Truncate: 1}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			b, _ := io.ReadAll(resp.Body)
			if err != nil || string(b) != "ab" || resp.ContentLength != 2 || resp.Header.Get("Content-Length") != "" {
				t.Errorf("= %q, %v", b, err)
			}
		}},
		{"corrupt", Faults{Corrupt: 1, Latency: time.Millisecond}, func(t *testing.T, resp *http.Response, err error, base *echo) {
			b, _ := io.ReadAll(resp.Body)
			if err != nil || len(b) != 4 || string(b) == "abcd" {
				t.Errorf("= %q, %v", b, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := &echo{body: "abcd"}
			tr := &Transport{Base: base, Faults: tt.faults}
			resp, err := tr.RoundTrip(newRequest(t, http.MethodPost))
			tt.check(t, resp, err, base)
			if tt.name != "none" && tt.name != "corrupt" && tr.Counts()[tt.name] != 1 {
				t.Errorf("counts = %v", tr.Counts())
			}
		})
	}
}

func TestTransportCanceledDuringLatency(t *testing.T) {
	tr := &Transport{Base: &echo{}, Faults: Faults{Latency: time.Hour, Seed: 1}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := newRequest(t, http.MethodGet).WithContext(ctx)
	if _, err := tr.RoundTrip(req); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestTransportBaseErrors(t *testing.T) {
	boom := errors.New("boom")
	tr := &Transport{Base: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, boom })}
	if _, err := tr.RoundTrip(newRequest(t, http.MethodGet)); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	failing := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(&errReader{}), Request: r}, nil
	})
	tr = &Transport{Base: failing, Faults: Faults{Truncate: 1}}
	if _, err := tr.RoundTrip(newRequest(t, http.MethodGet)); err == nil {
		t.Error("a body read error was not passed on")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestHandlerFaults(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "abcdef")
		_, _ = io.WriteString(w, "ghi")
	})

	h := &Handler{Next: next, Faults: Faults{Unavailable: 1, Latency: time.Millisecond}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" || h.Counts()["unavailable"] != 1 {
		t.Errorf("unavailable: %d %v", rec.Code, h.Counts())
	}

	h = &Handler{Next: next, Faults: Faults{Abort: 1}}
	rec = httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r != http.ErrAbortHandler { //nolint:errorlint // panic value
				t.Errorf("recovered %v", r)
			}
		}()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if rec.Body.String() != "abc" || !rec.Flushed {
		t.Errorf("abort wrote %q (flushed %v)", rec.Body.String(), rec.Flushed)
	}

	h = &Handler{Next: next}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != "abcdefghi" {
		t.Errorf("pass-through wrote %q", rec.Body.String())
	}
}

// echoServer echoes the bytes of each connection back.
func echoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return l.Addr().String()
}

func TestProxyForwards(t *testing.T) {
	p := &Proxy{Target: echoServer(t), Latency: time.Millisecond}
	addr, err := p.Start()
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = c.(*net.TCPConn).CloseWrite()
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "hello" {
		t.Errorf("echo = %q, %v", b, err)
	}
	_ = c.Close()
	if err := p.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestProxyResets(t *testing.T) {
	p := &Proxy{Target: echoServer(t), Reset: 1}
	addr, err := p.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = c.Write(bytes.Repeat([]byte("x"), 8192))
	b, _ := io.ReadAll(c)
	if len(b) >= 4096 {
		t.Errorf("read %d bytes through a reset connection", len(b))
	}
	if p.Counts()["reset"] != 1 {
		t.Errorf("counts = %v", p.Counts())
	}
}

func TestProxyUnreachableTarget(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := l.Addr().String()
	_ = l.Close()
	p := &Proxy{Target: target}
	addr, err := p.Start()
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if n, _ := c.Read(make([]byte, 1)); n != 0 {
		t.Error("read from a connection to nowhere")
	}
	_ = c.Close()
	_ = p.Close()
}
