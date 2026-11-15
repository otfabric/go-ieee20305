// SPDX-License-Identifier: MIT

package security_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// A configuration without a pool of roots fails every handshake with a
// configuration error, never falling back on the system's roots.
func TestTLSConfigsNilPool(t *testing.T) {
	p := newPKI(t, "Test Org")
	srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"server.example"})
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCert(p.ca, p.caKey, "device")
	if err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		cfg  *tls.Config
		peer *x509.Certificate
	}{
		"client":       {security.ClientTLSConfig(tlsCert(dev, devKey), nil), srv},
		"server":       {security.ServerTLSConfig(tlsCert(srv, srvKey), nil), dev},
		"notification": {security.NotificationTLSConfig(tlsCert(srv, srvKey), nil), dev},
	} {
		err := tt.cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{tt.peer}})
		if !errors.Is(err, ieee20305.ErrConfiguration) || !errors.Is(err, ieee20305.ErrCertificate) {
			t.Errorf("%s with a nil pool: %v, want ErrConfiguration and ErrCertificate", name, err)
		}
	}
	// A real handshake fails on the side without a pool.
	server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
	if cerr, _ := handshake(t, security.ClientTLSConfig(tlsCert(dev, devKey), nil), server); !errors.Is(cerr, ieee20305.ErrConfiguration) {
		t.Errorf("handshake of a client without roots: %v", cerr)
	}
}
