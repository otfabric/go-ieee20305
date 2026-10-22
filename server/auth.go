// SPDX-License-Identifier: MIT

package server

import (
	"crypto/x509"
	"net/http"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// Role is what a client certificate may do on the server.
type Role int

const (
	// RoleDevice is a device: it reads its own resources and those of every
	// client, and changes its own.
	RoleDevice Role = iota
	// RoleAggregator is an aggregator: a device that also reads and changes
	// the resources of the devices it manages, and registers them.
	RoleAggregator
	// RoleOperator is a head-end of the utility: it reads and changes every
	// resource, as Admin does, within the conformance of the methods.
	RoleOperator
)

func (r Role) String() string {
	switch r {
	case RoleDevice:
		return "device"
	case RoleAggregator:
		return "aggregator"
	case RoleOperator:
		return "operator"
	}
	return "Role(?)"
}

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
	// Role is what it may do.
	Role Role
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
		p.EndDevice, p.Edition, p.Role = d.EndDevice, d.Edition, d.Role
	}
	s.mu.Unlock()
	return p, nil
}

// acts reports whether p acts for the device owner: it is the device, the
// aggregator that manages it, or an operator.
func (s *Server) acts(p Principal, owner string) bool {
	if p.Role == RoleOperator || owner == p.lfdi() {
		return true
	}
	if p.Role != RoleAggregator || owner == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[owner].ManagedBy == p.lfdi()
}

// canRead reports whether p may read the node: one it acts for, or one
// every client may read.
func (s *Server) canRead(p Principal, n *Node) bool {
	return n.Owner == "" || s.acts(p, n.Owner)
}

// canChange reports whether p may change the node with PUT or DELETE: one
// it acts for; a resource of every client only for an operator.
func (s *Server) canChange(p Principal, n *Node) bool {
	if n.Owner == "" {
		return p.Role == RoleOperator
	}
	return s.acts(p, n.Owner)
}

// clientLists are the lists of every client that clients post to: a
// MirrorUsagePoint, a Response. (A POST to EndDeviceList registers a
// device; see createEndDevice.)
var clientLists = map[string]bool{
	"MirrorUsagePointList": true,
	"ResponseList":         true,
	"EndDeviceList":        true,
}

// canPost reports whether p may create an item in the list node: one it
// acts for, or a list of every client that clients post to.
func (s *Server) canPost(p Principal, list *Node) bool {
	if list.Owner == "" {
		return p.Role == RoleOperator || clientLists[list.Resource] || s.cfg.ClientLists[list.Resource]
	}
	return s.acts(p, list.Owner)
}
