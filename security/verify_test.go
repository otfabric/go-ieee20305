// SPDX-License-Identifier: MIT

package security_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

var (
	oidSAN      = asn1.ObjectIdentifier{2, 5, 29, 17}
	testHWType  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1}
	testHMN     = security.HMN{Type: testHWType, SerialNumber: []byte("SN-0001")}
	errExpected = errors.New("expected an error")
)

type pki struct {
	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
	pool  *x509.CertPool
}

func newPKI(t *testing.T, org string) pki {
	t.Helper()
	ca, key, err := security.TestCA(org)
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pki{ca, key, pool}
}

// issue signs a leaf for template with the CA of p.
func (p pki) issue(t *testing.T, template *x509.Certificate) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(p.caKey.Curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if template.SerialNumber == nil {
		template.SerialNumber = big.NewInt(time.Now().UnixNano())
	}
	if template.NotBefore.IsZero() {
		template.NotBefore = time.Now().Add(-time.Hour)
		template.NotAfter = time.Now().Add(time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return c, key
}

func hmnSAN(t *testing.T, h security.HMN) []byte {
	t.Helper()
	der, err := security.MarshalHMNSAN(h)
	if err != nil {
		t.Fatalf("MarshalHMNSAN: %v", err)
	}
	return der
}

// rawSAN builds a subject alternative name from DER-encoded GeneralNames.
func rawSAN(t *testing.T, names ...asn1.RawValue) []byte {
	t.Helper()
	der, err := asn1.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// otherName builds an otherName GeneralName of type id holding inner.
func otherName(t *testing.T, id asn1.ObjectIdentifier, inner []byte) asn1.RawValue {
	t.Helper()
	typeID, err := asn1.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	value, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner})
	if err != nil {
		t.Fatal(err)
	}
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: append(typeID, value...)}
}

func hmnName(t *testing.T, h security.HMN) asn1.RawValue {
	t.Helper()
	inner, err := asn1.Marshal(struct {
		T asn1.ObjectIdentifier
		S []byte
	}{h.Type, h.SerialNumber})
	if err != nil {
		t.Fatal(err)
	}
	return otherName(t, asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4}, inner)
}

func dnsName(name string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(name)}
}

func criticalSAN(der []byte) []pkix.Extension {
	return []pkix.Extension{{Id: oidSAN, Critical: true, Value: der}}
}

func TestHMNCertificatesNeedVerify(t *testing.T) {
	p := newPKI(t, "Test Org")
	dev, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatalf("TestDeviceCertHMN: %v", err)
	}
	// The reason Verify exists: crypto/x509 rejects these certificates.
	_, err = dev.Verify(x509.VerifyOptions{Roots: p.pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	var uce x509.UnhandledCriticalExtension
	if !errors.As(err, &uce) {
		t.Fatalf("x509 Verify error = %v, want UnhandledCriticalExtension", err)
	}
}

func TestVerifyHMN(t *testing.T) {
	p := newPKI(t, "Test Org")
	dev, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatalf("TestDeviceCertHMN: %v", err)
	}
	srv, _, err := security.TestServerCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatalf("TestServerCertHMN: %v", err)
	}
	lfdi := security.CertificateLFDI(dev)
	unhandled := len(dev.UnhandledCriticalExtensions)

	if err := security.Verify([]*x509.Certificate{dev}, security.VerifyOptions{
		Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth, PinnedLFDI: &lfdi, ServerName: "ignored.example",
	}); err != nil {
		t.Errorf("Verify device: %v", err)
	}
	if err := security.Verify([]*x509.Certificate{srv}, security.VerifyOptions{
		Roots: p.pool, Usage: x509.ExtKeyUsageServerAuth, ServerName: "10.0.0.1",
	}); err != nil {
		t.Errorf("Verify server: %v", err)
	}
	if err := security.Verify([]*x509.Certificate{srv}, security.VerifyOptions{Roots: p.pool}); err != nil {
		t.Errorf("Verify with any usage: %v", err)
	}
	if len(dev.UnhandledCriticalExtensions) != unhandled {
		t.Error("Verify changed the certificate passed in")
	}

	h, ok := security.HardwareModuleName(dev)
	if !ok || !h.Equal(testHMN) {
		t.Errorf("HardwareModuleName = %v, %v; want %v, true", h, ok, testHMN)
	}
	if _, ok := security.HardwareModuleName(p.ca); ok {
		t.Error("HardwareModuleName of a certificate without SAN reports one")
	}
}

func TestVerifyHMNWithDNS(t *testing.T) {
	p := newPKI(t, "Test Org")
	srv, _ := p.issue(t, &x509.Certificate{
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		ExtraExtensions: criticalSAN(rawSAN(t, hmnName(t, testHMN), dnsName("server.example"))),
	})
	if len(srv.DNSNames) != 1 {
		t.Fatalf("DNSNames = %v", srv.DNSNames)
	}
	o := security.VerifyOptions{Roots: p.pool, Usage: x509.ExtKeyUsageServerAuth, ServerName: "server.example"}
	if err := security.Verify([]*x509.Certificate{srv}, o); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if h, ok := security.HardwareModuleName(srv); !ok || !h.Equal(testHMN) {
		t.Errorf("HardwareModuleName = %v, %v", h, ok)
	}
	o.ServerName = "other.example"
	if err := security.Verify([]*x509.Certificate{srv}, o); err == nil {
		t.Error("Verify accepted a server name the certificate does not hold")
	}
	o.ServerName = ""
	if err := security.Verify([]*x509.Certificate{srv}, o); err != nil {
		t.Errorf("Verify without server name: %v", err)
	}
}

func TestVerifyIntermediate(t *testing.T) {
	root := newPKI(t, "Root Org")
	interKey, err := ecdsa.GenerateKey(root.caKey.Curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}, root.ca, &interKey.PublicKey, root.caKey)
	if err != nil {
		t.Fatal(err)
	}
	inter, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := security.TestDeviceCertHMN(inter, interKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	o := security.VerifyOptions{Roots: root.pool, Usage: x509.ExtKeyUsageClientAuth}
	if err := security.Verify([]*x509.Certificate{dev, inter}, o); err != nil {
		t.Errorf("Verify with the intermediate in the chain: %v", err)
	}
	o.Intermediates = x509.NewCertPool()
	o.Intermediates.AddCert(inter)
	if err := security.Verify([]*x509.Certificate{dev}, o); err != nil {
		t.Errorf("Verify with the intermediate in the pool: %v", err)
	}
	o.Intermediates = nil
	if err := security.Verify([]*x509.Certificate{dev}, o); err == nil {
		t.Error("Verify accepted a chain without its intermediate")
	}
}

func TestVerifyRejects(t *testing.T) {
	p := newPKI(t, "Test Org")
	other := newPKI(t, "Other Org")
	good, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	wrongCA, _, err := security.TestDeviceCertHMN(other.ca, other.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	unknownOther, _ := p.issue(t, &x509.Certificate{
		ExtraExtensions: criticalSAN(rawSAN(t, hmnName(t, testHMN),
			otherName(t, asn1.ObjectIdentifier{1, 2, 3, 4}, []byte{0x04, 0x01, 0x00}))),
	})
	registeredID, _ := p.issue(t, &x509.Certificate{
		ExtraExtensions: criticalSAN(rawSAN(t, hmnName(t, testHMN),
			asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 8, Bytes: []byte{0x2a, 0x03}})),
	})
	onlyUnknown, _ := p.issue(t, &x509.Certificate{
		ExtraExtensions: criticalSAN(rawSAN(t, otherName(t, asn1.ObjectIdentifier{1, 2, 3, 4}, []byte{0x05, 0x00}))),
	})
	unknownExt, _ := p.issue(t, &x509.Certificate{
		ExtraExtensions: append(criticalSAN(hmnSAN(t, testHMN)),
			pkix.Extension{Id: asn1.ObjectIdentifier{1, 2, 3, 4, 5}, Critical: true, Value: []byte{0x05, 0x00}}),
	})
	// A certificate whose SAN no longer parses; crypto/x509 would refuse
	// to parse it, so the parsed form is altered instead.
	truncated := *good
	truncated.Extensions = append([]pkix.Extension(nil), good.Extensions...)
	for i, e := range truncated.Extensions {
		if e.Id.Equal(oidSAN) {
			truncated.Extensions[i].Value = e.Value[:len(e.Value)-3]
		}
	}
	wrongLFDI := security.CertificateLFDI(wrongCA)

	base := security.VerifyOptions{Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth}
	tests := []struct {
		name  string
		chain []*x509.Certificate
		o     func(o *security.VerifyOptions)
	}{
		{"empty chain", nil, nil},
		{"nil leaf", []*x509.Certificate{nil}, nil},
		{"nil intermediate", []*x509.Certificate{good, nil}, nil},
		{"wrong CA", []*x509.Certificate{wrongCA}, nil},
		{"expired", []*x509.Certificate{good}, func(o *security.VerifyOptions) {
			o.Now = func() time.Time { return good.NotAfter.Add(time.Second) }
		}},
		{"wrong usage", []*x509.Certificate{good}, func(o *security.VerifyOptions) { o.Usage = x509.ExtKeyUsageServerAuth }},
		{"unknown otherName", []*x509.Certificate{unknownOther}, nil},
		{"registeredID in SAN", []*x509.Certificate{registeredID}, nil},
		{"only unknown otherName", []*x509.Certificate{onlyUnknown}, nil},
		{"unknown critical extension", []*x509.Certificate{unknownExt}, nil},
		{"truncated SAN", []*x509.Certificate{&truncated}, nil},
		{"pinned LFDI mismatch", []*x509.Certificate{good}, func(o *security.VerifyOptions) { o.PinnedLFDI = &wrongLFDI }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			if tt.o != nil {
				tt.o(&o)
			}
			if err := security.Verify(tt.chain, o); err == nil {
				t.Error(errExpected)
			}
		})
	}
	if _, ok := security.HardwareModuleName(&truncated); ok {
		t.Error("HardwareModuleName parsed a truncated SAN")
	}
	if _, ok := security.HardwareModuleName(onlyUnknown); ok {
		t.Error("HardwareModuleName reported an unknown otherName")
	}
}

func TestVerifyPeerCertificate(t *testing.T) {
	p := newPKI(t, "Test Org")
	dev, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	verify := security.VerifyPeerCertificate(security.VerifyOptions{Roots: p.pool, Usage: x509.ExtKeyUsageClientAuth})
	if err := verify([][]byte{dev.Raw}, nil); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := verify(nil, nil); err == nil {
		t.Error("verify accepted no certificates")
	}
	if err := verify([][]byte{dev.Raw[:10]}, nil); err == nil {
		t.Error("verify accepted a malformed certificate")
	}
}

// handshake runs a TLS handshake between client and server configurations
// over loopback TCP, and returns the client's and the server's error. TCP
// buffers what an end writes, so that an end that fails mid-handshake
// cannot leave the other blocked writing to it, as net.Pipe can; the
// deadline bounds the handshake all the same.
func handshake(t *testing.T, client, server *tls.Config) (cerr, serr error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	deadline := time.Now().Add(10 * time.Second)
	done := make(chan error, 1)
	go func() {
		s, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = s.Close() }()
		_ = s.SetDeadline(deadline)
		srv := tls.Server(s, server)
		err = srv.Handshake()
		if err == nil {
			// With TLS 1.3 the server learns of a rejected client
			// certificate only when the client's alert arrives: read it.
			_, _ = srv.Read(make([]byte, 1))
		}
		done <- err
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(deadline)
	cl := tls.Client(c, client)
	cerr = cl.Handshake()
	if cerr == nil {
		// Read what the server sends after the client's last flight: with
		// TLS 1.3 its session tickets or the alert that rejects the client.
		_ = cl.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = cl.Read(make([]byte, 1))
	}
	_ = c.Close()
	return cerr, <-done
}

func tlsCert(c *x509.Certificate, k *ecdsa.PrivateKey) tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{c.Raw}, PrivateKey: k, Leaf: c}
}

func TestTLSConfigsHMN(t *testing.T) {
	p := newPKI(t, "Test Org")
	other := newPKI(t, "Other Org")
	srv, srvKey, err := security.TestServerCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCertHMN(p.ca, p.caKey, security.HMN{Type: testHWType, SerialNumber: []byte("dev")})
	if err != nil {
		t.Fatal(err)
	}
	server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
	client := security.ClientTLSConfig(tlsCert(dev, devKey), p.pool)
	client.ServerName = "server.example"
	if cerr, serr := handshake(t, client, server); cerr != nil || serr != nil {
		t.Fatalf("handshake: client %v, server %v", cerr, serr)
	}

	// The client rejects a server that the roots did not issue.
	if cerr, _ := handshake(t, security.ClientTLSConfig(tlsCert(dev, devKey), other.pool), server); cerr == nil {
		t.Error("client accepted a server of another CA")
	}
	// The server rejects a device that its client CAs did not issue.
	if _, serr := handshake(t, client, security.ServerTLSConfig(tlsCert(srv, srvKey), other.pool)); serr == nil {
		t.Error("server accepted a device of another CA")
	}
	// The server requires a client certificate.
	noCert := security.ClientTLSConfig(tls.Certificate{}, p.pool)
	if _, serr := handshake(t, noCert, server); serr == nil {
		t.Error("server accepted a client without certificate")
	}
}

func TestTLSConfigsServerName(t *testing.T) {
	p := newPKI(t, "Test Org")
	srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"server.example"})
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCert(p.ca, p.caKey, "device")
	if err != nil {
		t.Fatal(err)
	}
	server := security.ServerTLSConfig(tlsCert(srv, srvKey), p.pool)
	client := security.ClientTLSConfig(tlsCert(dev, devKey), p.pool)
	client.ServerName = "server.example"
	if cerr, serr := handshake(t, client, server); cerr != nil || serr != nil {
		t.Fatalf("handshake: client %v, server %v", cerr, serr)
	}
	client.ServerName = "other.example"
	if cerr, _ := handshake(t, client, server); cerr == nil {
		t.Error("client accepted a server certificate for another name")
	}
	// A device certificate is not a server certificate.
	if cerr, _ := handshake(t, security.ClientTLSConfig(tlsCert(dev, devKey), p.pool),
		security.ServerTLSConfig(tlsCert(dev, devKey), p.pool)); cerr == nil {
		t.Error("client accepted a server with a client-auth certificate")
	}
}

// TestInsecureSkipVerifyConfined makes sure that ClientTLSConfig, which
// verifies the chain itself, is the only code that turns off the
// verification of crypto/tls; and ClientConfig of the tlsccm module, which
// does the same for the TLS of CCM_8 (its tests check that it refuses a
// server of another CA).
func TestInsecureSkipVerifyConfined(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		filepath.Join(root, "security", "tls.go"):  true,
		filepath.Join(root, "tlsccm", "tlsccm.go"): true,
	}
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[path] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte("InsecureSkipVerify")) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("InsecureSkipVerify outside security/tls.go and tlsccm/tlsccm.go: %v", found)
	}
}

func FuzzHardwareModuleName(f *testing.F) {
	good, err := security.MarshalHMNSAN(testHMN)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add(good[:len(good)-2])
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{0x30, 0x03, 0x82, 0x01, 0x61})
	f.Fuzz(func(t *testing.T, san []byte) {
		c := &x509.Certificate{Extensions: []pkix.Extension{{Id: oidSAN, Critical: true, Value: san}}}
		h, ok := security.HardwareModuleName(c)
		if !ok {
			return
		}
		// What parses must round-trip to a name that parses the same.
		der, err := security.MarshalHMNSAN(h)
		if err != nil {
			t.Fatalf("MarshalHMNSAN(%v): %v", h, err)
		}
		c.Extensions[0].Value = der
		h2, ok := security.HardwareModuleName(c)
		if !ok || !h2.Equal(h) {
			t.Fatalf("round trip: %v, %v; want %v", h2, ok, h)
		}
	})
}

// Without trusted CAs, verification fails rather than trusting the system
// roots; every failure matches ieee20305.ErrCertificate, and those of
// crypto/x509 its error as well.
func TestVerifyFailsClosed(t *testing.T) {
	p, other := newPKI(t, "Test Org"), newPKI(t, "Other Org")
	dev, _, err := security.TestDeviceCertHMN(p.ca, p.caKey, testHMN)
	if err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]security.VerifyOptions{
		"no roots":    {Usage: x509.ExtKeyUsageClientAuth},
		"other roots": {Roots: other.pool, Usage: x509.ExtKeyUsageClientAuth},
	} {
		err := security.Verify([]*x509.Certificate{dev}, o)
		if !errors.Is(err, ieee20305.ErrCertificate) {
			t.Errorf("%s: %v", name, err)
		}
	}
	err = security.Verify([]*x509.Certificate{dev}, security.VerifyOptions{Roots: other.pool})
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Errorf("the error of crypto/x509 is lost: %v", err)
	}
	if err := security.Verify(nil, security.VerifyOptions{Roots: p.pool}); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("no chain: %v", err)
	}
	if err := security.VerifyPeerCertificate(security.VerifyOptions{Roots: p.pool})([][]byte{{1}}, nil); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := security.ParseLFDI("zz"); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("ParseLFDI: %v", err)
	}
	if _, err := security.ParseLFDI("00"); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("ParseLFDI of 1 byte: %v", err)
	}
	// A server without client CAs refuses every client.
	srv, srvKey, err := security.TestServerCert(p.ca, p.caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := security.ServerTLSConfig(tlsCert(srv, srvKey), nil)
	if err := cfg.VerifyPeerCertificate([][]byte{dev.Raw}, nil); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("a server without client CAs: %v", err)
	}
}
