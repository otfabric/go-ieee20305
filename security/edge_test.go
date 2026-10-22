// SPDX-License-Identifier: MIT

package security_test

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"io"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

var oidHMN = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4}

// hmnInner is the DER of the HardwareModuleName testHMN.
func hmnInner(t *testing.T) []byte {
	t.Helper()
	inner, err := asn1.Marshal(struct {
		T asn1.ObjectIdentifier
		S []byte
	}{testHMN.Type, testHMN.SerialNumber})
	if err != nil {
		t.Fatal(err)
	}
	return inner
}

// rawOtherName builds an otherName GeneralName from its raw content.
func rawOtherName(content []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: content}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	der, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestHardwareModuleNameMalformed(t *testing.T) {
	good := hmnSAN(t, testHMN)
	typeID := mustMarshal(t, oidHMN)
	inner := hmnInner(t)
	value := mustMarshal(t, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner})

	tests := map[string][]byte{
		"empty":                      nil,
		"trailing data":              append(append([]byte(nil), good...), 0x05, 0x00),
		"not a sequence":             {0x31, 0x00},
		"primitive sequence":         {0x10, 0x00},
		"universal general name":     {0x30, 0x02, 0x04, 0x00},
		"truncated general name":     {0x30, 0x02, 0x82, 0x05},
		"primitive otherName":        {0x30, 0x02, 0x80, 0x00},
		"otherName type not an OID":  rawSAN(t, rawOtherName([]byte{0x04, 0x00})),
		"otherName without value":    rawSAN(t, rawOtherName(typeID)),
		"otherName value wrong tag":  rawSAN(t, rawOtherName(append(append([]byte(nil), typeID...), mustMarshal(t, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: inner})...))),
		"otherName value primitive":  rawSAN(t, rawOtherName(append(append([]byte(nil), typeID...), 0x80, 0x00))),
		"otherName value universal":  rawSAN(t, rawOtherName(append(append([]byte(nil), typeID...), 0x30, 0x00))),
		"otherName trailing data":    rawSAN(t, rawOtherName(append(append(append([]byte(nil), typeID...), value...), 0x05, 0x00))),
		"HMN not a sequence":         rawSAN(t, otherName(t, oidHMN, []byte{0x05, 0x00})),
		"HMN trailing data":          rawSAN(t, otherName(t, oidHMN, append(append([]byte(nil), inner...), 0x05, 0x00))),
		"HMN after a malformed name": rawSAN(t, rawOtherName([]byte{0x04, 0x00}), hmnName(t, testHMN)),
		"only DNS names":             rawSAN(t, dnsName("a.example")),
		"only an unknown otherName":  rawSAN(t, otherName(t, asn1.ObjectIdentifier{1, 2, 3}, []byte{0x05, 0x00})),
		"registeredID general name":  rawSAN(t, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 8, Bytes: []byte{0x2a, 0x03}}),
		"HMN with wrong field types": rawSAN(t, otherName(t, oidHMN, mustMarshal(t, struct{ A, B int }{1, 2}))),
		"HMN missing its serial":     rawSAN(t, otherName(t, oidHMN, mustMarshal(t, struct{ T asn1.ObjectIdentifier }{testHWType}))),
		"HMN serial wrong type": rawSAN(t, otherName(t, oidHMN, mustMarshal(t, struct {
			T asn1.ObjectIdentifier
			S int
		}{testHWType, 1}))),
		"sequence with indefinite len": {0x30, 0x80, 0x00, 0x00},
	}
	for name, san := range tests {
		t.Run(name, func(t *testing.T) {
			c := &x509.Certificate{Extensions: []pkix.Extension{{Id: oidSAN, Critical: true, Value: san}}}
			if h, ok := security.HardwareModuleName(c); ok {
				t.Errorf("HardwareModuleName = %v, true", h)
			}
		})
	}

	// Names besides the HardwareModuleName do not hide it; the first wins.
	second := security.HMN{Type: testHWType, SerialNumber: []byte("second")}
	c := &x509.Certificate{Extensions: []pkix.Extension{
		{Id: asn1.ObjectIdentifier{1, 2, 3}, Value: []byte{0x05, 0x00}},
		{Id: oidSAN, Value: rawSAN(t, dnsName("a.example"),
			otherName(t, asn1.ObjectIdentifier{1, 2, 3}, []byte{0x05, 0x00}),
			hmnName(t, testHMN), hmnName(t, second))},
	}}
	if h, ok := security.HardwareModuleName(c); !ok || !h.Equal(testHMN) {
		t.Errorf("HardwareModuleName = %v, %v; want %v, true", h, ok, testHMN)
	}
}

func TestHMNEqual(t *testing.T) {
	for _, o := range []security.HMN{
		{Type: asn1.ObjectIdentifier{1, 2, 3}, SerialNumber: testHMN.SerialNumber},
		{Type: testHWType, SerialNumber: []byte("SN-0002")},
		{Type: testHWType},
	} {
		if testHMN.Equal(o) || o.Equal(testHMN) {
			t.Errorf("%v equals %v", testHMN, o)
		}
	}
	if !testHMN.Equal(security.HMN{Type: append(asn1.ObjectIdentifier(nil), testHWType...), SerialNumber: []byte("SN-0001")}) {
		t.Error("equal names differ")
	}
}

func TestMarshalHMNSAN(t *testing.T) {
	for _, h := range []security.HMN{
		{Type: testHWType, SerialNumber: nil},
		{Type: testHWType, SerialNumber: make([]byte, 300)},
	} {
		der, err := security.MarshalHMNSAN(h)
		if err != nil {
			t.Fatalf("MarshalHMNSAN(%v): %v", h, err)
		}
		c := &x509.Certificate{Extensions: []pkix.Extension{{Id: oidSAN, Value: der}}}
		if got, ok := security.HardwareModuleName(c); !ok || !got.Equal(h) {
			t.Errorf("round trip of %v: %v, %v", h, got, ok)
		}
	}
	// An object identifier that DER cannot encode.
	for _, typ := range []asn1.ObjectIdentifier{nil, {1}, {3, 1}} {
		if _, err := security.MarshalHMNSAN(security.HMN{Type: typ, SerialNumber: []byte{1}}); err == nil {
			t.Errorf("MarshalHMNSAN accepted the type %v", typ)
		}
	}
}

func TestVerifyMoreRejects(t *testing.T) {
	p := newPKI(t, "Test Org")
	good, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	srv, _, err := security.TestServerCert(p.ca, p.caKey, []string{"server.example", "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	// A critical SAN marked unhandled whose extension is gone: nothing
	// shows that it holds only names Verify may accept.
	noSAN := *good
	noSAN.Extensions = nil
	// An unknown critical extension and no SAN at all.
	unknownExt, _ := p.issue(t, &x509.Certificate{
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4, 5}, Critical: true, Value: []byte{0x05, 0x00}}},
	})
	// A critical SAN of an HMN and an unknown critical extension.
	both, _ := p.issue(t, &x509.Certificate{
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		ExtraExtensions: []pkix.Extension{
			{Id: asn1.ObjectIdentifier{1, 2, 3, 4, 5}, Critical: true, Value: []byte{0x05, 0x00}},
			{Id: oidSAN, Critical: true, Value: hmnSAN(t, testHMN)},
		},
	})
	lfdi := security.CertificateLFDI(srv)

	base := security.VerifyOptions{Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth}
	tests := []struct {
		name  string
		chain []*x509.Certificate
		o     func(o *security.VerifyOptions)
	}{
		{"not yet valid", []*x509.Certificate{good}, func(o *security.VerifyOptions) {
			o.Now = func() time.Time { return good.NotBefore.Add(-time.Second) }
		}},
		{"CA expired", []*x509.Certificate{good}, func(o *security.VerifyOptions) {
			o.Now = func() time.Time { return p.ca.NotAfter.Add(time.Hour) }
		}},
		{"code signing usage", []*x509.Certificate{good}, func(o *security.VerifyOptions) { o.Usage = x509.ExtKeyUsageCodeSigning }},
		{"client usage of a server", []*x509.Certificate{srv}, nil},
		{"wrong server name", []*x509.Certificate{srv}, func(o *security.VerifyOptions) {
			o.Usage, o.ServerName = x509.ExtKeyUsageServerAuth, "other.example"
		}},
		{"wrong server IP", []*x509.Certificate{srv}, func(o *security.VerifyOptions) {
			o.Usage, o.ServerName = x509.ExtKeyUsageServerAuth, "10.0.0.2"
		}},
		{"pinned LFDI of another leaf", []*x509.Certificate{good}, func(o *security.VerifyOptions) { o.PinnedLFDI = &lfdi }},
		{"SAN extension missing", []*x509.Certificate{&noSAN}, nil},
		{"unknown critical extension", []*x509.Certificate{unknownExt}, nil},
		{"HMN and unknown critical extension", []*x509.Certificate{both}, nil},
		{"leaf as its own intermediate only", []*x509.Certificate{good, good}, func(o *security.VerifyOptions) {
			o.Roots = x509.NewCertPool()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			if tt.o != nil {
				tt.o(&o)
			}
			err := security.Verify(tt.chain, o)
			if !errors.Is(err, ieee20305.ErrCertificate) {
				t.Errorf("Verify = %v, want ErrCertificate", err)
			}
		})
	}

	// The x509 error behind the failure stays visible.
	o := base
	o.Now = func() time.Time { return good.NotAfter.Add(time.Second) }
	var invalid x509.CertificateInvalidError
	if err := security.Verify([]*x509.Certificate{good}, o); !errors.As(err, &invalid) || invalid.Reason != x509.Expired {
		t.Errorf("expired: %v", err)
	}
	o = base
	o.Usage = x509.ExtKeyUsageServerAuth
	o.ServerName = "other.example"
	var host x509.HostnameError
	if err := security.Verify([]*x509.Certificate{srv}, o); !errors.As(err, &host) {
		t.Errorf("wrong server name: %v", err)
	}

	// What does pass: the right IP, a pinned LFDI that matches, a server
	// name that a certificate without DNS or IP names ignores, and a time
	// within the validity.
	pin := security.CertificateLFDI(srv)
	if err := security.Verify([]*x509.Certificate{srv}, security.VerifyOptions{
		Roots: p.pool, Usage: x509.ExtKeyUsageServerAuth, ServerName: "10.0.0.1", PinnedLFDI: &pin,
		Now: func() time.Time { return srv.NotBefore.Add(time.Minute) },
	}); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if err := security.Verify([]*x509.Certificate{good}, security.VerifyOptions{
		Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth, ServerName: "anything.example",
	}); err != nil {
		t.Errorf("Verify of an HMN certificate with a server name: %v", err)
	}
}

func TestVerifyPeerCertificateErrors(t *testing.T) {
	p := newPKI(t, "Test Org")
	dev, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	verify := security.VerifyPeerCertificate(security.VerifyOptions{Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth})
	for name, raw := range map[string][][]byte{
		"empty chain":             {},
		"empty certificate":       {{}},
		"garbage intermediate":    {dev.Raw, {0x30, 0x00}},
		"trailing data":           {append(append([]byte(nil), dev.Raw...), 0x00)},
		"certificate of other CA": {newCertRaw(t)},
	} {
		if err := verify(raw, nil); !errors.Is(err, ieee20305.ErrCertificate) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The verified chains of crypto/tls are ignored.
	if err := verify([][]byte{dev.Raw, p.ca.Raw}, [][]*x509.Certificate{{dev}}); err != nil {
		t.Errorf("chain with the root: %v", err)
	}
}

func newCertRaw(t *testing.T) []byte {
	t.Helper()
	other := newPKI(t, "Other Org")
	c, _, err := security.TestDeviceCertHMN(other.ca, other.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	return c.Raw
}

// listenerConfig is the TLS configuration of the notification listener of
// a device, which serves cert.
func listenerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
}

func TestNotificationTLSConfig(t *testing.T) {
	p := newPKI(t, "Device Org")
	other := newPKI(t, "Other Org")
	srv, srvKey, err := security.TestServerCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	// A listener serving the device certificate, whose usage is clientAuth.
	dev, devKey, err := security.TestDeviceCertHMN(p.ca, p.caKey, security.HMN{Type: testHWType, SerialNumber: []byte("dev")})
	if err != nil {
		t.Fatal(err)
	}
	named, namedKey, err := security.TestServerCert(p.ca, p.caKey, []string{"listener.example"})
	if err != nil {
		t.Fatal(err)
	}

	notify := security.NotificationTLSConfig(tlsCert(srv, srvKey), p.pool)
	if notify.MinVersion != tls.VersionTLS12 || len(notify.Certificates) != 1 || notify.RootCAs != p.pool {
		t.Errorf("config = %+v", notify)
	}
	notify.ServerName = "listener.example"
	if cerr, serr := handshake(t, notify, listenerConfig(tlsCert(dev, devKey))); cerr != nil || serr != nil {
		t.Errorf("handshake with a device certificate: client %v, server %v", cerr, serr)
	}
	if cerr, serr := handshake(t, notify, listenerConfig(tlsCert(named, namedKey))); cerr != nil || serr != nil {
		t.Errorf("handshake with a named listener: client %v, server %v", cerr, serr)
	}

	// The listener presents its server certificate as a client certificate
	// when the listener asks for one.
	mtls := listenerConfig(tlsCert(dev, devKey))
	mtls.ClientAuth = tls.RequireAnyClientCert
	var got [][]byte
	mtls.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
		got = raw
		return nil
	}
	if cerr, serr := handshake(t, notify, mtls); cerr != nil || serr != nil {
		t.Errorf("mutual handshake: client %v, server %v", cerr, serr)
	} else if len(got) != 1 || string(got[0]) != string(srv.Raw) {
		t.Error("the listener did not receive the server certificate")
	}

	wrongName := security.NotificationTLSConfig(tlsCert(srv, srvKey), p.pool)
	wrongName.ServerName = "other.example"
	if cerr, _ := handshake(t, wrongName, listenerConfig(tlsCert(named, namedKey))); !errors.Is(cerr, ieee20305.ErrCertificate) {
		t.Errorf("listener with another name: %v", cerr)
	}
	otherCA := security.NotificationTLSConfig(tlsCert(srv, srvKey), other.pool)
	if cerr, _ := handshake(t, otherCA, listenerConfig(tlsCert(dev, devKey))); !errors.Is(cerr, ieee20305.ErrCertificate) {
		t.Errorf("listener of another CA: %v", cerr)
	}
	noCAs := security.NotificationTLSConfig(tlsCert(srv, srvKey), nil)
	if cerr, _ := handshake(t, noCAs, listenerConfig(tlsCert(dev, devKey))); !errors.Is(cerr, ieee20305.ErrCertificate) {
		t.Errorf("no device CAs: %v", cerr)
	}

	// The callback on its own.
	if err := notify.VerifyConnection(tls.ConnectionState{}); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("no peer certificates: %v", err)
	}
	if err := notify.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{dev}}); err != nil {
		t.Errorf("device certificate: %v", err)
	}
}

func TestTLSConfigCallbacks(t *testing.T) {
	p := newPKI(t, "Test Org")
	other := newPKI(t, "Other Org")
	srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"server.example"})
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := security.TestServerCertHMN(other.ca, other.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}

	client := security.ClientTLSConfig(tlsCert(dev, devKey), p.pool)
	if client.MinVersion != tls.VersionTLS12 || !client.InsecureSkipVerify || client.RootCAs != p.pool {
		t.Errorf("client config = %+v", client)
	}
	for name, tt := range map[string]struct {
		cs tls.ConnectionState
		ok bool
	}{
		"server":          {tls.ConnectionState{PeerCertificates: []*x509.Certificate{srv}, ServerName: "server.example"}, true},
		"server by IP":    {tls.ConnectionState{PeerCertificates: []*x509.Certificate{srv}}, true},
		"wrong name":      {tls.ConnectionState{PeerCertificates: []*x509.Certificate{srv}, ServerName: "x.example"}, false},
		"device":          {tls.ConnectionState{PeerCertificates: []*x509.Certificate{dev}}, false},
		"other CA":        {tls.ConnectionState{PeerCertificates: []*x509.Certificate{foreign}}, false},
		"no certificates": {tls.ConnectionState{}, false},
		"nil certificate": {tls.ConnectionState{PeerCertificates: []*x509.Certificate{nil}}, false},
	} {
		err := client.VerifyConnection(tt.cs)
		if tt.ok && err != nil {
			t.Errorf("client %s: %v", name, err)
		}
		if !tt.ok && !errors.Is(err, ieee20305.ErrCertificate) {
			t.Errorf("client %s: %v, want ErrCertificate", name, err)
		}
	}

	server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
	if server.ClientAuth != tls.RequireAnyClientCert || server.ClientCAs != p.pool || server.MinVersion != tls.VersionTLS12 {
		t.Errorf("server config = %+v", server)
	}
	if err := server.VerifyPeerCertificate([][]byte{dev.Raw}, nil); err != nil {
		t.Errorf("server device: %v", err)
	}
	if err := server.VerifyPeerCertificate([][]byte{srv.Raw}, nil); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("server with a server-auth client: %v", err)
	}
	if err := server.VerifyPeerCertificate([][]byte{foreign.Raw}, nil); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("server other CA: %v", err)
	}
	if err := server.VerifyPeerCertificate(nil, nil); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("server no certificates: %v", err)
	}

	// A real handshake of a client that a device of the server's CA cannot
	// complete: the server's chain is verified with the client's roots.
	if cerr, _ := handshake(t, security.ClientTLSConfig(tlsCert(dev, devKey), other.pool), server); !errors.Is(cerr, ieee20305.ErrCertificate) {
		t.Errorf("client of another CA: %v", cerr)
	}
}

// failingReader fails after n bytes.
type failingReader struct{ n int }

func (r *failingReader) Read(b []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("no randomness")
	}
	if len(b) > r.n {
		b = b[:r.n]
	}
	r.n -= len(b)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return len(b), nil
}

// withReader runs f with crypto/rand.Reader replaced by r. The tests of this
// package do not run in parallel, so nothing else reads it meanwhile.
func withReader(r io.Reader, f func()) {
	saved := rand.Reader
	rand.Reader = r
	defer func() { rand.Reader = saved }()
	f()
}

func TestTestCertErrors(t *testing.T) {
	p := newPKI(t, "Test Org")
	other := newPKI(t, "Other Org")

	// A CA key that does not match the CA certificate.
	if _, _, err := security.TestServerCert(p.ca, other.caKey, []string{"server.example"}); err == nil {
		t.Error("TestServerCert signed with a mismatched CA key")
	}
	if _, _, err := security.TestDeviceCert(p.ca, other.caKey, "device"); err == nil {
		t.Error("TestDeviceCert signed with a mismatched CA key")
	}
	if _, _, err := security.TestDeviceCertHMN(p.ca, other.caKey, testHMN); err == nil {
		t.Error("TestDeviceCertHMN signed with a mismatched CA key")
	}
	if _, _, err := security.TestServerCertHMN(p.ca, other.caKey, testHMN); err == nil {
		t.Error("TestServerCertHMN signed with a mismatched CA key")
	}
	// A HardwareModuleName that cannot be encoded.
	if _, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, security.HMN{Type: asn1.ObjectIdentifier{1}}); err == nil {
		t.Error("TestDeviceCertHMN accepted an invalid hardware type")
	}

	// Randomness that runs out at every point of each helper. Whether the
	// key generation reads it depends on the Go version; the serial number
	// always does.
	gens := map[string]func() error{
		"TestCA": func() error {
			_, _, err := security.TestCA("Test")
			return err
		},
		"TestServerCert": func() error {
			_, _, err := security.TestServerCert(p.ca, p.caKey, []string{"server.example"})
			return err
		},
		"TestDeviceCert": func() error {
			_, _, err := security.TestDeviceCert(p.ca, p.caKey, "device")
			return err
		},
		"TestDeviceCertHMN": func() error {
			_, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
			return err
		},
	}
	for name, gen := range gens {
		t.Run(name, func(t *testing.T) {
			var err error
			withReader(&failingReader{}, func() { err = gen() })
			if err == nil {
				t.Fatal("succeeded without randomness")
			}
			for n := 1; n <= 512; n++ {
				withReader(&failingReader{n: n}, func() { err = gen() })
				if err == nil {
					return
				}
			}
			// Enough randomness for everything.
			withReader(io.MultiReader(&failingReader{n: 1 << 20}), func() { err = gen() })
			if err != nil {
				t.Errorf("with randomness: %v", err)
			}
		})
	}
}
