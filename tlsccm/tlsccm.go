// SPDX-License-Identifier: MIT

// Package tlsccm gives IEEE 2030.5 clients and servers of go-ieee20305 the
// cipher suite the standard requires, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
// which crypto/tls does not implement (golang/go#27484).
//
// It is a module of its own, so that the library stays free of
// dependencies. Its TLS is internal/gotls: crypto/tls of Go 1.22.0 with
// CCM_8 added (internal/gotls/UPSTREAM.md), kept
// as close to upstream as can be and checked against Go's security
// releases (internal/gotls/SECURITY.md). Connections
// are TLS 1.2 on P-256, as IEEE 2030.5 specifies, and offer CCM_8 only
// unless Options.AllowGCM also offers
// TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, for peers without CCM_8.
// Certificates are verified by package security, which accepts the
// HardwareModuleName subject alternative names of IEEE 2030.5 certificates.
//
// A client takes NewHTTPClient as its client.Config.HTTPClient. A server
// listens with NewListener, serves with an http.Server that ConfigureServer
// prepared, and takes PeerCertificates as its server.Config.PeerCertificates:
// net/http sets Request.TLS only for crypto/tls connections. HTTP/2 is not
// spoken.
package tlsccm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls"
)

// CCM8 is the cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8.
const CCM8 = gotls.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8

// GCM is the cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, which
// AllowGCM adds.
const GCM = gotls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256

// Options are the options of the configurations of this package.
type Options struct {
	// AllowGCM offers TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 after CCM_8,
	// for peers whose TLS has no CCM_8 (such as crypto/tls).
	AllowGCM bool
	// HandshakeTimeout bounds a handshake: 10s when zero; negative is
	// ErrConfiguration.
	HandshakeTimeout time.Duration
	// Logger receives the handshake failures of a listener, at Warn (see
	// ieee20305.Logger); none when nil.
	Logger ieee20305.Logger
}

func (o Options) suites() []uint16 {
	if o.AllowGCM {
		return []uint16{CCM8, GCM}
	}
	return []uint16{CCM8}
}

func (o Options) timeout() time.Duration {
	if o.HandshakeTimeout > 0 {
		return o.HandshakeTimeout
	}
	return defaultHandshakeTimeout
}

// certificate converts a certificate of crypto/tls to one of gotls.
func certificate(c tls.Certificate) gotls.Certificate {
	return gotls.Certificate{Certificate: c.Certificate, PrivateKey: c.PrivateKey, Leaf: c.Leaf}
}

// clientConfig returns the TLS configuration of an IEEE 2030.5 client: the
// device certificate, and the server's chain verified against roots by
// security.Verify (with the server name when the certificate has one).
// The verification of gotls is off (InsecureSkipVerify) only because it
// rejects the HardwareModuleName names; VerifyConnection always runs.
// TLS 1.2 only: the TLS 1.3 code of gotls is never reached.
func clientConfig(device tls.Certificate, roots *x509.CertPool, o Options) *gotls.Config {
	return &gotls.Config{
		Certificates:       []gotls.Certificate{certificate(device)},
		RootCAs:            roots,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs gotls.ConnectionState) error {
			return security.Verify(cs.PeerCertificates, security.VerifyOptions{
				Roots:      roots,
				ServerName: cs.ServerName,
				Usage:      x509.ExtKeyUsageServerAuth,
			})
		},
		MinVersion:       gotls.VersionTLS12,
		MaxVersion:       gotls.VersionTLS12,
		CipherSuites:     o.suites(),
		CurvePreferences: []gotls.CurveID{gotls.CurveP256},
	}
}

// serverConfig returns the TLS configuration of an IEEE 2030.5 server: the
// server certificate, and a client certificate that one of clientCAs
// issued, verified by security.Verify. Session tickets are off, so that
// every connection verifies its client; TLS 1.2 only.
func serverConfig(server tls.Certificate, clientCAs *x509.CertPool, o Options) *gotls.Config {
	return &gotls.Config{
		Certificates: []gotls.Certificate{certificate(server)},
		ClientAuth:   gotls.RequireAnyClientCert,
		ClientCAs:    clientCAs,
		VerifyPeerCertificate: security.VerifyPeerCertificate(security.VerifyOptions{
			Roots: clientCAs,
			Usage: x509.ExtKeyUsageClientAuth,
		}),
		MinVersion:             gotls.VersionTLS12,
		MaxVersion:             gotls.VersionTLS12,
		CipherSuites:           o.suites(),
		CurvePreferences:       []gotls.CurveID{gotls.CurveP256},
		SessionTicketsDisabled: true,
	}
}

// NewHTTPClient returns the HTTP client of an IEEE 2030.5 client with the
// device certificate, for client.Config.HTTPClient: it dials with CCM_8.
// It sets no timeout: bound each call with its context.
func NewHTTPClient(device tls.Certificate, roots *x509.CertPool, o Options) *http.Client {
	cfg := clientConfig(device, roots, o)
	dialer := &gotls.Dialer{NetDialer: &net.Dialer{Timeout: o.timeout()}, Config: cfg}
	return &http.Client{Transport: &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, o.timeout())
			defer cancel()
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2: false,
	}}
}

// NewListener returns a listener of TLS connections with CCM_8 on inner,
// for a server with the server certificate whose clients one of clientCAs
// issued. A connection is accepted once its handshake succeeds, within
// Options.HandshakeTimeout; a failure goes to Options.Logger at Warn.
// clientCAs is required.
func NewListener(inner net.Listener, server tls.Certificate, clientCAs *x509.CertPool, o Options) (net.Listener, error) {
	if clientCAs == nil {
		return nil, fmt.Errorf("%w: tlsccm: a listener needs the CAs of its clients", ieee20305.ErrConfiguration)
	}
	if o.HandshakeTimeout < 0 {
		return nil, fmt.Errorf("%w: tlsccm: a negative handshake timeout %v", ieee20305.ErrConfiguration, o.HandshakeTimeout)
	}
	tl := gotls.NewListener(inner, serverConfig(server, clientCAs, o))
	return newListener(tl, ieee20305.ComponentLogger(o.Logger, "tlsccm"), o.timeout()), nil
}

type connKey struct{}

// ConfigureServer prepares srv to serve the connections of a listener of
// NewListener: it keeps each connection for PeerCertificates.
func ConfigureServer(srv *http.Server) {
	next := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if next != nil {
			ctx = next(ctx, c)
		}
		if gc, ok := c.(*gotls.Conn); ok {
			return context.WithValue(ctx, connKey{}, gc)
		}
		return ctx
	}
}

// ErrNoConnection is returned by PeerCertificates for a request that did
// not come over a connection of NewListener (served by a server that
// ConfigureServer prepared).
var ErrNoConnection = fmt.Errorf("%w: tlsccm: the request came over no CCM_8 connection", ieee20305.ErrCertificate)

// PeerCertificates returns the certificate chain of the client of a
// request: for server.Config.PeerCertificates. A request over crypto/tls
// has its own.
func PeerCertificates(r *http.Request) ([]*x509.Certificate, error) {
	if r.TLS != nil {
		return r.TLS.PeerCertificates, nil
	}
	gc, ok := r.Context().Value(connKey{}).(*gotls.Conn)
	if !ok {
		return nil, ErrNoConnection
	}
	return gc.ConnectionState().PeerCertificates, nil
}

// CipherSuite returns the cipher suite of the connection of a request, or
// 0.
func CipherSuite(r *http.Request) uint16 {
	if r.TLS != nil {
		return r.TLS.CipherSuite
	}
	if gc, ok := r.Context().Value(connKey{}).(*gotls.Conn); ok {
		return gc.ConnectionState().CipherSuite
	}
	return 0
}
