// SPDX-License-Identifier: MIT

package gotls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// Tests of the changes of later Go releases ported to the copy (UPSTREAM.md).

// portsPeers returns a client and a server configuration as tlsccm uses
// them: TLS 1.2, CCM_8, P-256, ECDSA P-256 certificates.
func portsPeers(t *testing.T) (client, server *Config) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey := newKey()
	caT := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caT, caT, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(serial int64) Certificate {
		k := newKey()
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			DNSNames: []string{"server.test"},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return Certificate{Certificate: [][]byte{der}, PrivateKey: k}
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	common := func(c *Config) *Config {
		c.MinVersion, c.MaxVersion = VersionTLS12, VersionTLS12
		c.CipherSuites = []uint16{TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8}
		c.CurvePreferences = []CurveID{CurveP256}
		return c
	}
	client = common(&Config{Certificates: []Certificate{leaf(2)}, RootCAs: pool, ServerName: "server.test"})
	server = common(&Config{Certificates: []Certificate{leaf(3)}, ClientCAs: pool, ClientAuth: RequireAndVerifyClientCert, SessionTicketsDisabled: true})
	return client, server
}

// portsHandshake runs a handshake over a loopback connection and returns
// the errors of both sides.
func portsHandshake(t *testing.T, client, server *Config) (clientErr, serverErr error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		s, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		_ = s.SetDeadline(time.Now().Add(time.Minute))
		srv := Server(s, server)
		err = srv.Handshake()
		_ = srv.Close()
		done <- err
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(time.Minute))
	cl := Client(c, client)
	clientErr = cl.Handshake()
	_ = cl.Close()
	return clientErr, <-done
}

// Go 1.25: a client refuses a ServerKeyExchange on a curve it did not
// offer. The server here picks its own curve whatever the client offered.
func TestClientRefusesUnofferedCurve(t *testing.T) {
	for _, curve := range []CurveID{CurveP256, X25519, CurveP384, CurveP521} {
		client, server := portsPeers(t)
		server.CurvePreferences = []CurveID{curve}
		server.GetConfigForClient = func(chi *ClientHelloInfo) (*Config, error) {
			for i := range chi.SupportedCurves {
				chi.SupportedCurves[i] = curve
			}
			return nil, nil
		}
		clientErr, _ := portsHandshake(t, client, server)
		if curve == CurveP256 {
			if clientErr != nil {
				t.Errorf("P-256: %v", clientErr)
			}
			continue
		}
		if clientErr == nil || !strings.Contains(clientErr.Error(), "unoffered curve") {
			t.Errorf("%v: client error %v, want an unoffered curve", curve, clientErr)
		}
	}
}

// Go 1.25 (RFC 9155): SHA-1 signatures are neither offered nor used in
// TLS 1.2, and a TLS 1.2 peer must send signature_algorithms.
func TestNoSHA1Signatures(t *testing.T) {
	for _, s := range supportedSignatureAlgorithms() {
		if s == PKCS1WithSHA1 || s == ECDSAWithSHA1 {
			t.Errorf("%v offered", s)
		}
	}
	defer func() { testingOnlyForceClientHelloSignatureAlgorithms = nil }()
	for _, tt := range []struct {
		offered []SignatureScheme
		want    string
	}{
		{[]SignatureScheme{ECDSAWithSHA1, PKCS1WithSHA1}, "doesn't support any of the certificate's signature algorithms"},
		{[]SignatureScheme{}, "missing signature_algorithms"},
		{[]SignatureScheme{ECDSAWithSHA1, ECDSAWithP256AndSHA256}, ""},
	} {
		testingOnlyForceClientHelloSignatureAlgorithms = tt.offered
		client, server := portsPeers(t)
		clientErr, serverErr := portsHandshake(t, client, server)
		if tt.want == "" {
			if clientErr != nil || serverErr != nil {
				t.Errorf("%v: %v, %v", tt.offered, clientErr, serverErr)
			}
			continue
		}
		if serverErr == nil || !strings.Contains(serverErr.Error(), tt.want) {
			t.Errorf("%v: server error %v, want %q", tt.offered, serverErr, tt.want)
		}
	}
	// The client signs its CertificateVerify with SHA-256 even when the
	// server prefers SHA-1.
	cert, _ := portsPeers(t)
	alg, err := selectSignatureScheme(VersionTLS12, &cert.Certificates[0], []SignatureScheme{ECDSAWithSHA1, ECDSAWithP256AndSHA256})
	if err != nil || alg != ECDSAWithP256AndSHA256 {
		t.Errorf("selectSignatureScheme = %v, %v", alg, err)
	}
	if _, err := selectSignatureScheme(VersionTLS12, &cert.Certificates[0], []SignatureScheme{ECDSAWithSHA1}); err == nil {
		t.Error("selectSignatureScheme chose SHA-1")
	}
	if slices.Contains(defaultSupportedSignatureAlgorithms, ECDSAWithSHA1) {
		t.Error("ECDSA with SHA-1 is a default")
	}
}
