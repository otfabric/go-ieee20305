// SPDX-License-Identifier: MIT

package server

import (
	"crypto/x509"
	"net/http"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// Principal is the client of a request: the device of its certificate.
type Principal struct {
	// LFDI and SFDI identify the device by its certificate.
	LFDI security.LFDI
	SFDI security.SFDI
	// EndDevice is the href of its EndDevice, or "" for a device the server
	// does not know.
	EndDevice string
	// Edition is the edition of IEEE 2030.5 the server speaks to it.
	Edition ieee20305.Edition
}

// lfdi returns the LFDI as the Owner of the nodes of the device.
func (p Principal) lfdi() string { return p.LFDI.String() }

// principal identifies the client of r by its certificate.
func (s *Server) principal(r *http.Request) (Principal, error) {
	var chain []*x509.Certificate
	if s.cfg.PeerCertificates != nil {
		certs, err := s.cfg.PeerCertificates(r)
		if err != nil {
			return Principal{}, &HTTPError{Status: http.StatusForbidden, Err: err}
		}
		chain = certs
	} else if r.TLS != nil {
		chain = r.TLS.PeerCertificates
	}
	if len(chain) == 0 {
		return Principal{}, httpError(http.StatusForbidden, "no client certificate")
	}
	lfdi := security.CertificateLFDI(chain[0])
	p := Principal{LFDI: lfdi, SFDI: lfdi.SFDI(), Edition: s.cfg.DefaultEdition}
	s.mu.Lock()
	if d, ok := s.devices[lfdi.String()]; ok {
		p.EndDevice, p.Edition = d.EndDevice, d.Edition
	}
	s.mu.Unlock()
	return p, nil
}

// canRead reports whether p may read the node: one of its own, or one
// every client may read.
func canRead(p Principal, n *Node) bool {
	return n.Owner == "" || n.Owner == p.lfdi()
}

// canChange reports whether p may change the node with PUT or DELETE: one
// of its own.
func canChange(p Principal, n *Node) bool {
	return n.Owner != "" && n.Owner == p.lfdi()
}
