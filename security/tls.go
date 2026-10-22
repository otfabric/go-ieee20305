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
// The server's chain is always verified against roots, for server
// authentication, by Verify in VerifyConnection. The configuration sets
// InsecureSkipVerify only to turn off the verification of crypto/tls, which
// rejects the critical subject alternative name that holds the
// HardwareModuleName of IEEE 2030.5 server certificates. The server name of
// the connection is checked when the server certificate has DNS names or IP
// addresses. crypto/tls does not report the server name of a server dialed
// by IP address, so such a server is identified by its chain alone.
//
// IEEE 2030.5 requires the cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
// which crypto/tls does not implement. Connections use the ECDHE-ECDSA
// suites crypto/tls offers, which servers commonly accept as well.
func ClientTLSConfig(device tls.Certificate, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{device},
		RootCAs:      roots,
		// The chain is verified by VerifyConnection below.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			return Verify(cs.PeerCertificates, VerifyOptions{
				Roots:      roots,
				ServerName: cs.ServerName,
				Usage:      x509.ExtKeyUsageServerAuth,
			})
		},
	}
}

// ServerTLSConfig returns the TLS configuration of an IEEE 2030.5 server:
// TLS 1.2 or later, the server certificate, and a client certificate that
// one of clientCAs must have issued for client authentication. The client
// chain is verified by Verify, which accepts the critical subject
// alternative name that holds the HardwareModuleName of IEEE 2030.5 device
// certificates; ConnectionState.VerifiedChains is therefore empty. See
// ClientTLSConfig about the cipher suite of IEEE 2030.5.
func ServerTLSConfig(server tls.Certificate, clientCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAnyClientCert,
		ClientCAs:    clientCAs,
		VerifyPeerCertificate: VerifyPeerCertificate(VerifyOptions{
			Roots: clientCAs,
			Usage: x509.ExtKeyUsageClientAuth,
		}),
	}
}

// NotificationTLSConfig returns the TLS configuration with which a server
// posts notifications to the listeners of devices: the server certificate
// as its client certificate, and the listener's chain verified against
// deviceCAs. A device may serve its listener with its device certificate,
// whose extended key usage is clientAuth only, so the usage is not checked;
// the listener's name is, when its certificate has DNS names or IP
// addresses. As in ClientTLSConfig, InsecureSkipVerify only turns off the
// verification of crypto/tls, which rejects HardwareModuleName names.
func NotificationTLSConfig(server tls.Certificate, deviceCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		Certificates:       []tls.Certificate{server},
		RootCAs:            deviceCAs,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			return Verify(cs.PeerCertificates, VerifyOptions{
				Roots:      deviceCAs,
				ServerName: cs.ServerName,
				Usage:      x509.ExtKeyUsageAny,
			})
		},
	}
}
