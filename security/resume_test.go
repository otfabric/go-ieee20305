// SPDX-License-Identifier: MIT

package security_test

import (
	"crypto/tls"
	"errors"
	"sync"
	"testing"

	"github.com/otfabric/go-ieee20305/security"
)

// The server verifies the client chain on resumed sessions too: a check
// in VerifyPeerCertificate, which crypto/tls skips on resumption, would let
// a client in on its ticket after its certificate stopped being accepted.
func TestServerTLSConfigResumption(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tls.VersionName(version), func(t *testing.T) {
			p := newPKI(t, "Test Org")
			srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"resume.test"})
			if err != nil {
				t.Fatal(err)
			}
			dev, devKey, err := security.TestDeviceCert(p.ca, p.caKey, "device")
			if err != nil {
				t.Fatal(err)
			}
			server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
			server.MaxVersion = version
			verify := server.VerifyConnection
			var (
				mu      sync.Mutex
				resumed []bool
				refuse  bool
			)
			errRefused := errors.New("refused")
			server.VerifyConnection = func(cs tls.ConnectionState) error {
				mu.Lock()
				resumed = append(resumed, cs.DidResume)
				r := refuse
				mu.Unlock()
				if r {
					return errRefused
				}
				return verify(cs)
			}
			client := security.ClientTLSConfig(tlsCert(dev, devKey), p.pool)
			client.ClientSessionCache = tls.NewLRUClientSessionCache(4)
			// The session cache is keyed by the server name (by the address,
			// which changes with every handshake here, without one).
			client.ServerName = "resume.test"

			if cerr, serr := handshake(t, client, server); cerr != nil || serr != nil {
				t.Fatalf("first handshake: client %v, server %v", cerr, serr)
			}
			if cerr, serr := handshake(t, client, server); cerr != nil || serr != nil {
				t.Fatalf("second handshake: client %v, server %v", cerr, serr)
			}
			mu.Lock()
			refuse = true
			mu.Unlock()
			if _, serr := handshake(t, client, server); !errors.Is(serr, errRefused) {
				t.Errorf("resumed handshake after the client stopped being accepted: server %v, want refused", serr)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(resumed) != 3 || resumed[0] || !resumed[1] || !resumed[2] {
				t.Errorf("VerifyConnection calls (DidResume) = %v, want [false true true]", resumed)
			}
		})
	}
}

func TestServerTLSConfigVerifiesChain(t *testing.T) {
	p := newPKI(t, "Test Org")
	other := newPKI(t, "Other Org")
	srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, foreignKey, err := security.TestDeviceCert(other.ca, other.caKey, "device")
	if err != nil {
		t.Fatal(err)
	}
	server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
	client := security.ClientTLSConfig(tlsCert(foreign, foreignKey), p.pool)
	if _, serr := handshake(t, client, server); serr == nil {
		t.Error("server accepted a client of another CA")
	}
}
