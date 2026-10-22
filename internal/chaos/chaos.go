// SPDX-License-Identifier: MIT

// Package chaos injects faults between IEEE 2030.5 clients and servers, for
// tests: a client transport, a server middleware and a TCP proxy that fail
// at random, from a seed, so that a failing run can be repeated.
package chaos

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"
)

// Faults are the probabilities (0 to 1) of the faults to inject, and the
// largest added latency.
type Faults struct {
	// Seed seeds the random choices.
	Seed int64
	// Latency is the largest delay added to a request or connection.
	Latency time.Duration
	// Reset fails a request without sending it, as a reset connection
	// does.
	Reset float64
	// Lost sends a request and fails it without its response, so that a
	// client cannot tell whether it took effect.
	Lost float64
	// Unavailable answers 503 without sending the request (a transport),
	// or instead of the handler (a middleware).
	Unavailable float64
	// Truncate cuts the body of a response in half.
	Truncate float64
	// Corrupt flips a byte of the body of a response.
	Corrupt float64
	// Replay sends a POST or PUT twice, as a retry after a lost response
	// does.
	Replay float64
	// Abort aborts the handler of a request after half of its response (a
	// middleware).
	Abort float64
}

// Counts are the faults injected so far, by kind.
type Counts map[string]int

// dice draws the random choices of a fault injector.
type dice struct {
	mu     sync.Mutex
	rng    *rand.Rand
	counts Counts
}

func newDice(seed int64) *dice {
	return &dice{rng: rand.New(rand.NewSource(seed)), counts: Counts{}}
}

// roll reports whether the fault kind of probability p happens, and counts
// it.
func (d *dice) roll(kind string, p float64) bool {
	if p <= 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rng.Float64() < p {
		d.counts[kind]++
		return true
	}
	return false
}

// delay returns a random delay up to max.
func (d *dice) delay(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Duration(d.rng.Int63n(int64(max)))
}

func (d *dice) intn(n int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rng.Intn(n)
}

func (d *dice) snapshot() Counts {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := Counts{}
	for k, v := range d.counts {
		c[k] = v
	}
	return c
}

// ErrInjected is the error of an injected transport fault.
var ErrInjected = errors.New("chaos: injected fault")

// Transport is an http.RoundTripper that injects faults into the requests
// of a client and their responses.
type Transport struct {
	// Base sends the requests that get through.
	Base http.RoundTripper
	Faults
	once sync.Once
	d    *dice
}

func (t *Transport) dice() *dice {
	t.once.Do(func() { t.d = newDice(t.Seed) })
	return t.d
}

// Counts returns the faults injected so far.
func (t *Transport) Counts() Counts { return t.dice().snapshot() }

// RoundTrip sends req, or fails it.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	d := t.dice()
	if wait := d.delay(t.Latency); wait > 0 {
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
	}
	if d.roll("reset", t.Reset) {
		return nil, ErrInjected
	}
	if d.roll("unavailable", t.Unavailable) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
			Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req, ProtoMajor: 1, ProtoMinor: 1}, nil
	}
	if (req.Method == http.MethodPost || req.Method == http.MethodPut) && req.GetBody != nil && d.roll("replay", t.Replay) {
		first := req.Clone(req.Context())
		if body, err := req.GetBody(); err == nil {
			first.Body = body
			if resp, err := t.Base.RoundTrip(first); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
		if body, err := req.GetBody(); err == nil {
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if d.roll("lost", t.Lost) {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, ErrInjected
	}
	truncate, corrupt := d.roll("truncate", t.Truncate), d.roll("corrupt", t.Corrupt)
	if !truncate && !corrupt {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if truncate {
		body = body[:len(body)/2]
	}
	if corrupt && len(body) > 0 {
		body[d.intn(len(body))] ^= 0x5A
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Del("Content-Length")
	return resp, nil
}

// Handler is a middleware that injects faults into the answers of a
// server.
type Handler struct {
	// Next answers the requests that get through.
	Next http.Handler
	Faults
	once sync.Once
	d    *dice
}

func (h *Handler) dice() *dice {
	h.once.Do(func() { h.d = newDice(h.Seed) })
	return h.d
}

// Counts returns the faults injected so far.
func (h *Handler) Counts() Counts { return h.dice().snapshot() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d := h.dice()
	if wait := d.delay(h.Latency); wait > 0 {
		time.Sleep(wait)
	}
	if d.roll("unavailable", h.Unavailable) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if d.roll("abort", h.Abort) {
		rec := &halfWriter{ResponseWriter: w}
		h.Next.ServeHTTP(rec, r)
		panic(http.ErrAbortHandler)
	}
	h.Next.ServeHTTP(w, r)
}

// halfWriter passes on the head of a response and half of its body.
type halfWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *halfWriter) Write(b []byte) (int, error) {
	if w.wrote {
		return len(b), nil
	}
	w.wrote = true
	_, err := w.ResponseWriter.Write(b[:len(b)/2])
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return len(b), err
}

// Proxy is a TCP proxy that resets connections at random: right away,
// during the TLS handshake, or later.
type Proxy struct {
	// Target is the address the proxy forwards to.
	Target string
	// Reset is the probability that a connection is reset after a random
	// number of bytes up to ResetWithin (4096 when zero), in either
	// direction.
	Reset       float64
	ResetWithin int
	// Latency is the largest delay added before a connection is
	// forwarded.
	Latency time.Duration
	// Seed seeds the random choices.
	Seed int64

	l    net.Listener
	d    *dice
	wg   sync.WaitGroup
	mu   sync.Mutex
	conn map[net.Conn]bool
}

// Start listens on a loopback port and returns the address to dial.
func (p *Proxy) Start() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	p.l, p.d, p.conn = l, newDice(p.Seed), map[net.Conn]bool{}
	p.wg.Add(1)
	go p.accept()
	return l.Addr().String(), nil
}

// Counts returns the resets injected so far.
func (p *Proxy) Counts() Counts { return p.d.snapshot() }

// Close stops the proxy and closes its connections.
func (p *Proxy) Close() error {
	err := p.l.Close()
	p.mu.Lock()
	for c := range p.conn {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
	return err
}

func (p *Proxy) track(c net.Conn, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.conn[c] = true
	} else {
		delete(p.conn, c)
	}
}

func (p *Proxy) accept() {
	defer p.wg.Done()
	for {
		c, err := p.l.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go p.serve(c)
	}
}

func (p *Proxy) serve(in net.Conn) {
	defer p.wg.Done()
	p.track(in, true)
	defer p.track(in, false)
	defer func() { _ = in.Close() }()
	if wait := p.d.delay(p.Latency); wait > 0 {
		time.Sleep(wait)
	}
	out, err := net.Dial("tcp", p.Target)
	if err != nil {
		return
	}
	p.track(out, true)
	defer p.track(out, false)
	defer func() { _ = out.Close() }()
	limit := int64(-1)
	if p.d.roll("reset", p.Reset) {
		within := p.ResetWithin
		if within <= 0 {
			within = 4096
		}
		limit = int64(p.d.intn(within))
	}
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		defer func() { done <- struct{}{} }()
		if limit >= 0 {
			_, _ = io.CopyN(dst, src, limit)
			_ = in.Close()
			_ = out.Close()
			return
		}
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}
	go pipe(out, in)
	go pipe(in, out)
	<-done
	<-done
}
