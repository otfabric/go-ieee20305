// SPDX-License-Identifier: MIT

package tlsccm_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// pki is a CA with a server and a device certificate that name only their
// hardware module, as IEEE 2030.5 certificates do.
type pki struct {
	roots          *x509.CertPool
	server, device tls.Certificate
}

func newPKI(t *testing.T) pki {
	t.Helper()
	ca, key, err := security.TestCA("tlsccm test CA")
	if err != nil {
		t.Fatal(err)
	}
	hmn := func(serial string) security.HMN {
		return security.HMN{Type: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1}, SerialNumber: []byte(serial)}
	}
	srv, srvKey, err := security.TestServerCertHMN(ca, key, hmn("server-1"))
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCertHMN(ca, key, hmn("device-1"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return pki{
		roots:  roots,
		server: tls.Certificate{Certificate: [][]byte{srv.Raw}, PrivateKey: srvKey, Leaf: srv},
		device: tls.Certificate{Certificate: [][]byte{dev.Raw}, PrivateKey: devKey, Leaf: dev},
	}
}

// serve serves srv with CCM_8 and returns its URL and the cipher suites of
// its requests.
func serve(t *testing.T, p pki, srv *server.Server, o tlsccm.Options) (string, func() []uint16) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := tlsccm.NewListener(inner, p.server, p.roots, o)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var suites []uint16
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		suites = append(suites, tlsccm.CipherSuite(r))
		mu.Unlock()
		srv.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	tlsccm.ConfigureServer(hs)
	go func() { _ = hs.Serve(l) }()
	t.Cleanup(func() { _ = hs.Close() })
	return "https://" + inner.Addr().String(), func() []uint16 {
		mu.Lock()
		defer mu.Unlock()
		return append([]uint16(nil), suites...)
	}
}

func newServer(t *testing.T) *server.Server {
	t.Helper()
	srv, err := server.New(server.Config{PeerCertificates: tlsccm.PeerCertificates, InBandRegistration: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// A device registers and starts over CCM_8, with certificates that name
// only their hardware module.
func TestAgentOverCCM8(t *testing.T) {
	p := newPKI(t)
	srv := newServer(t)
	url, suites := serve(t, p, srv, tlsccm.Options{})
	c, err := client.New(client.Config{Server: url, HTTPClient: tlsccm.NewHTTPClient(p.device, p.roots, tlsccm.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(agent.Config{Client: c, Identity: client.IdentityOf(p.device.Leaf)})
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	ctx := context.Background()
	if err := ag.Start(ctx); !errors.Is(err, client.ErrNotRegistered) {
		t.Fatalf("Start before registration: %v", err)
	}
	if err := srv.Admin().Register(ctx, security.CertificateLFDI(p.device.Leaf), 1); err != nil {
		t.Fatal(err)
	}
	if err := ag.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if ag.State().EndDevice.GetHref() != "/edev/1" {
		t.Errorf("EndDevice %q", ag.State().EndDevice.GetHref())
	}
	for _, s := range suites() {
		if s != tlsccm.CCM8 {
			t.Fatalf("a request with cipher suite %#x", s)
		}
	}
	if len(suites()) < 5 {
		t.Errorf("%d requests", len(suites()))
	}
}

// crypto/tls has no CCM_8: a server with GCM allowed serves it.
func TestGCMFallback(t *testing.T) {
	p := newPKI(t)
	ctx := context.Background()
	for _, allow := range []bool{false, true} {
		url, suites := serve(t, p, newServer(t), tlsccm.Options{AllowGCM: allow, HandshakeTimeout: 2 * time.Second})
		c, err := client.New(client.Config{Server: url, HTTPClient: client.NewHTTPClient(p.device, p.roots)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.DeviceCapability(ctx)
		if allow && (err != nil || len(suites()) != 1 || suites()[0] != tlsccm.GCM) {
			t.Errorf("with GCM: %v, %#x", err, suites())
		}
		if !allow && err == nil {
			t.Error("crypto/tls talked to a CCM_8-only server")
		}
	}
}

// A client verifies the server: a server of another CA is refused.
func TestServerVerification(t *testing.T) {
	p, other := newPKI(t), newPKI(t)
	url, _ := serve(t, other, newServer(t), tlsccm.Options{})
	c, err := client.New(client.Config{Server: url, HTTPClient: tlsccm.NewHTTPClient(p.device, p.roots, tlsccm.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeviceCapability(context.Background()); err == nil {
		t.Error("a server of another CA")
	}
	// And a server refuses a device of another CA.
	url, _ = serve(t, p, newServer(t), tlsccm.Options{})
	c, err = client.New(client.Config{Server: url, HTTPClient: tlsccm.NewHTTPClient(other.device, other.roots, tlsccm.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeviceCapability(context.Background()); err == nil {
		t.Error("a device of another CA")
	}
}

func TestPeerCertificatesWithoutConnection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/dcap", nil)
	if _, err := tlsccm.NewListener(nil, tls.Certificate{}, nil, tlsccm.Options{}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("NewListener without client CAs: %v", err)
	}
	if _, err := tlsccm.PeerCertificates(r); !errors.Is(err, tlsccm.ErrNoConnection) || !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("PeerCertificates = %v", err)
	}
	if tlsccm.CipherSuite(r) != 0 {
		t.Error("a cipher suite without connection")
	}
	r.TLS = &tls.ConnectionState{CipherSuite: 1, PeerCertificates: []*x509.Certificate{{}}}
	if certs, err := tlsccm.PeerCertificates(r); err != nil || len(certs) != 1 || tlsccm.CipherSuite(r) != 1 {
		t.Errorf("over crypto/tls: %v", err)
	}
	// ConfigureServer keeps a ConnContext it finds.
	called := false
	hs := &http.Server{ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
		called = true
		return ctx
	}}
	tlsccm.ConfigureServer(hs)
	hs.ConnContext(context.Background(), nil)
	if !called {
		t.Error("ConfigureServer dropped the ConnContext")
	}
}
