// SPDX-License-Identifier: MIT

// Package security implements the certificate handling of IEEE 2030.5:
// device identifiers, chain verification for certificates with a
// HardwareModuleName, and the TLS configurations of clients, servers and
// notifiers.
//
// IEEE 2030.5 devices and servers authenticate each other with ECDSA P-256
// certificates over mutual TLS 1.2. A device is known by identifiers
// derived from the SHA-256 fingerprint of its certificate: the Long-Form
// Device Identifier (LFDI) is its leftmost 160 bits (CertificateLFDI), and
// the Short-Form Device Identifier (SFDI) its leftmost 36 bits as a decimal
// number followed by a check digit (LFDI.SFDI, SFDI.Valid).
//
// Device certificates of IEEE 2030.5 identify their hardware module in a
// critical subjectAltName (HardwareModuleName, RFC 4108), which crypto/x509
// refuses to verify. Verify verifies such chains, and ClientTLSConfig,
// ServerTLSConfig and NotificationTLSConfig use it: they never fall back on
// the system's roots, and a nil pool of roots is ErrConfiguration.
//
//	tlsConfig := security.ServerTLSConfig(serverCert, deviceCAs)
//	lfdi := security.CertificateLFDI(r.TLS.PeerCertificates[0])
//
// The cipher suite IEEE 2030.5 mandates, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
// is not in crypto/tls: the configurations of this package offer the
// ECDHE-ECDSA AES-GCM suites, and the module
// github.com/otfabric/go-ieee20305/tlsccm adds CCM_8. TestCA,
// TestServerCert and TestDeviceCert make throwaway certificates for tests
// and examples.
package security
