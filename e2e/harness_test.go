// SPDX-License-Identifier: MIT

package e2e_test

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"testing"

	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// pki is a CA with the certificates of a test run.
type pki struct {
	ca    *x509.Certificate
	key   *ecdsa.PrivateKey
	roots *x509.CertPool
}

func newPKI(t testing.TB) *pki {
	t.Helper()
	ca, key, err := security.TestCA("e2e test CA")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &pki{ca: ca, key: key, roots: roots}
}

// device returns the certificate of a device, with its key.
func (p *pki) device(t testing.TB, name string) tls.Certificate {
	t.Helper()
	cert, key, err := security.TestDeviceCert(p.ca, p.key, name)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
}

// env is a server of this module on loopback, with mutual TLS.
type env struct {
	pki *pki
	srv *server.Server
	url string
}

func newEnv(t testing.TB, cfg server.Config) *env {
	t.Helper()
	p := newPKI(t)
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := security.TestServerCert(p.ca, p.key, []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv)
	ts.TLS = security.ServerTLSConfig(tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, p.roots)
	ts.StartTLS()
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
	})
	return &env{pki: p, srv: srv, url: ts.URL}
}

// client returns a client of the server with the certificate of device.
func (e *env) client(t testing.TB, device tls.Certificate, codec client.Codec) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Server: e.url, HTTPClient: client.NewHTTPClient(device, e.pki.roots), Codec: codec})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
