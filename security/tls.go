// SPDX-License-Identifier: MIT

package security

import (
	"crypto/tls"
	"crypto/x509"
)

// ClientTLSConfig returns the TLS configuration of an IEEE 2030.5 client:
// TLS 1.2 or later, the device certificate for mutual TLS, and the CAs that
// verify the server.
//
// IEEE 2030.5 requires the cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
// which crypto/tls does not implement. Connections use the ECDHE-ECDSA
// suites crypto/tls offers, which servers commonly accept as well.
func ClientTLSConfig(device tls.Certificate, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{device},
		RootCAs:      roots,
	}
}

// ServerTLSConfig returns the TLS configuration of an IEEE 2030.5 server:
// TLS 1.2 or later, the server certificate, and a client certificate that
// one of clientCAs must have issued. See ClientTLSConfig about the cipher
// suite of IEEE 2030.5.
func ServerTLSConfig(server tls.Certificate, clientCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
}
