// SPDX-License-Identifier: MIT

package tlsccm_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// lines is a Logger that keeps what it is given.
type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) add(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, fmt.Sprintf(f, a...))
}
func (l *lines) Debugf(f string, a ...any) { l.add("DEBUG "+f, a...) }
func (l *lines) Infof(f string, a ...any)  { l.add("INFO "+f, a...) }
func (l *lines) Warnf(f string, a ...any)  { l.add("WARN "+f, a...) }
func (l *lines) Errorf(f string, a ...any) { l.add("ERROR "+f, a...) }
func (l *lines) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, g := range l.got {
		if strings.Contains(g, s) {
			return true
		}
	}
	return false
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func listen(t *testing.T, p pki, o tlsccm.Options) (net.Listener, string) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := tlsccm.NewListener(inner, p.server, p.roots, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, inner.Addr().String()
}

func TestListenerConfiguration(t *testing.T) {
	p := newPKI(t)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inner.Close() }()
	if _, err := tlsccm.NewListener(inner, p.server, nil, tlsccm.Options{}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("no client CAs: %v", err)
	}
	if _, err := tlsccm.NewListener(inner, p.server, p.roots, tlsccm.Options{HandshakeTimeout: -time.Second}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("a negative handshake timeout: %v", err)
	}
}

// A peer that opens TCP and never speaks TLS is dropped after the
// handshake timeout; a peer whose certificate the server refuses (over
// GCM, which crypto/tls has) is logged; neither reaches Accept, and the
// device over CCM_8 does.
func TestListenerHandshakes(t *testing.T) {
	p := newPKI(t)
	log := &lines{}
	l, addr := listen(t, p, tlsccm.Options{HandshakeTimeout: 200 * time.Millisecond, Logger: log, AllowGCM: true})
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()
	eventually(t, "log of the silent peer", func() bool { return log.has("WARN TLS handshake error from " + silent.LocalAddr().String()) })
	_ = silent.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := silent.Read(make([]byte, 1)); err == nil {
		t.Error("the silent peer was not dropped")
	}

	// A device of another CA.
	other := newPKI(t)
	c, err := tls.Dial("tcp", addr, gcmClient(other))
	if err == nil {
		// TLS 1.2 refuses within the handshake; read in case it lands late.
		_, err = c.Read(make([]byte, 1))
		_ = c.Close()
	}
	if err == nil {
		t.Error("the server accepted a device of another CA")
	}
	eventually(t, "log of the refused device", func() bool { return log.has("certificate") })

	select {
	case c := <-accepted:
		t.Errorf("accepted %v", c.RemoteAddr())
	default:
	}

	// The device over CCM_8 is accepted.
	dial := tlsccm.NewHTTPClient(p.device, p.roots, tlsccm.Options{}).Transport.(*http.Transport).DialTLSContext
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(10 * time.Second):
		t.Error("the device was not accepted")
	}
}

// gcmClient is a crypto/tls client of the device, which has no CCM_8.
// crypto/tls cannot verify certificates with a HardwareModuleName, so it
// skips verification: the server's refusal is under test.
func gcmClient(p pki) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{p.device}, InsecureSkipVerify: true, //nolint:gosec // see above
		CipherSuites: []uint16{tlsccm.GCM}, MaxVersion: tls.VersionTLS12}
}

// Close cancels the handshakes in flight and ends Accept.
func TestListenerClose(t *testing.T) {
	p := newPKI(t)
	l, addr := listen(t, p, tlsccm.Options{HandshakeTimeout: time.Minute})
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()
	errs := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		errs <- err
	}()
	time.Sleep(50 * time.Millisecond) // the handshake of silent is in flight
	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close waited for the handshake in flight")
	}
	if err := <-errs; !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after Close: %v", err)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept again: %v", err)
	}
}

// flaky is a listener whose Accept fails with a temporary error first.
type flaky struct {
	net.Listener
	once sync.Once
}

type temporaryError struct{}

func (temporaryError) Error() string   { return "too many open files" }
func (temporaryError) Temporary() bool { return true }
func (temporaryError) Timeout() bool   { return false }

func (f *flaky) Accept() (net.Conn, error) {
	var first bool
	f.once.Do(func() { first = true })
	if first {
		return nil, temporaryError{}
	}
	return f.Listener.Accept()
}

// A temporary Accept error goes to the caller, and accepting goes on: what
// net/http retries.
func TestListenerTemporaryError(t *testing.T) {
	p := newPKI(t)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := tlsccm.NewListener(&flaky{Listener: inner}, p.server, p.roots, tlsccm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, err := l.Accept(); !errors.As(err, new(temporaryError)) {
		t.Fatalf("first Accept: %v", err)
	}
	dial := tlsccm.NewHTTPClient(p.device, p.roots, tlsccm.Options{}).Transport.(*http.Transport).DialTLSContext
	go func() {
		if c, err := dial(context.Background(), "tcp", inner.Addr().String()); err == nil {
			defer func() { _ = c.Close() }()
			time.Sleep(time.Second)
		}
	}()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}
