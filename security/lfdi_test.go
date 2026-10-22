// SPDX-License-Identifier: MIT

package security_test

import (
	"testing"

	"github.com/otfabric/go-ieee20305/security"
)

func TestCertificateLFDI(t *testing.T) {
	ca, _, err := security.TestCA("Test Org")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	lfdi := security.CertificateLFDI(ca)

	// LFDI should be 40 hex characters.
	s := lfdi.String()
	if len(s) != 40 {
		t.Fatalf("LFDI string length = %d, want 40", len(s))
	}

	// Should be uppercase hex.
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			t.Fatalf("LFDI contains non-hex char %c", c)
		}
	}
}

func TestLFDIDeterministic(t *testing.T) {
	ca, _, err := security.TestCA("Test Org")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	lfdi1 := security.CertificateLFDI(ca)
	lfdi2 := security.CertificateLFDI(ca)

	if lfdi1 != lfdi2 {
		t.Error("LFDI should be deterministic for the same certificate")
	}
}

func TestDERBytesLFDI(t *testing.T) {
	ca, _, err := security.TestCA("Test Org")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	fromCert := security.CertificateLFDI(ca)
	fromDER := security.DERBytesLFDI(ca.Raw)

	if fromCert != fromDER {
		t.Error("CertificateLFDI and DERBytesLFDI should produce the same LFDI")
	}
}

func TestSFDI(t *testing.T) {
	ca, _, err := security.TestCA("Test Org")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	lfdi := security.CertificateLFDI(ca)
	sfdi := lfdi.SFDI()

	if !sfdi.Valid() {
		t.Errorf("SFDI %s failed Luhn check", sfdi)
	}

	// SFDI should be a positive number.
	if sfdi == 0 {
		t.Error("SFDI should not be zero")
	}
}

func TestSFDILuhn(t *testing.T) {
	// Generate several certs and check that all SFDIs pass Luhn.
	for i := 0; i < 5; i++ {
		ca, caKey, err := security.TestCA("Test Org")
		if err != nil {
			t.Fatalf("TestCA: %v", err)
		}
		dev, _, err := security.TestDeviceCert(ca, caKey, "device")
		if err != nil {
			t.Fatalf("TestDeviceCert: %v", err)
		}

		sfdi := security.CertificateLFDI(dev).SFDI()
		if !sfdi.Valid() {
			t.Errorf("iteration %d: SFDI %s failed Luhn check", i, sfdi)
		}
	}
}

func TestParseLFDI(t *testing.T) {
	ca, _, err := security.TestCA("Test Org")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	original := security.CertificateLFDI(ca)
	parsed, err := security.ParseLFDI(original.String())
	if err != nil {
		t.Fatalf("ParseLFDI: %v", err)
	}
	if parsed != original {
		t.Error("round-trip LFDI mismatch")
	}
}

func TestParseLFDIErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"too short", "ABCD"},
		{"too long", "0123456789ABCDEF0123456789ABCDEF01234567890"},
		{"invalid hex", "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := security.ParseLFDI(tt.input)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestTestCA(t *testing.T) {
	ca, key, err := security.TestCA("Test")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}
	if !ca.IsCA {
		t.Error("CA cert should have IsCA=true")
	}
	if key == nil {
		t.Error("CA key should not be nil")
	}
}

func TestTestServerCert(t *testing.T) {
	ca, caKey, err := security.TestCA("Test")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	cert, key, err := security.TestServerCert(ca, caKey, []string{"localhost"})
	if err != nil {
		t.Fatalf("TestServerCert: %v", err)
	}
	if cert.IsCA {
		t.Error("server cert should not be a CA")
	}
	if key == nil {
		t.Error("server key should not be nil")
	}
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "localhost" {
		t.Error("server cert should have localhost SAN")
	}
}

func TestTestDeviceCert(t *testing.T) {
	ca, caKey, err := security.TestCA("Test")
	if err != nil {
		t.Fatalf("TestCA: %v", err)
	}

	cert, key, err := security.TestDeviceCert(ca, caKey, "device-01")
	if err != nil {
		t.Fatalf("TestDeviceCert: %v", err)
	}
	if cert.IsCA {
		t.Error("device cert should not be a CA")
	}
	if key == nil {
		t.Error("device key should not be nil")
	}
	if cert.Subject.CommonName != "device-01" {
		t.Errorf("CN = %q, want device-01", cert.Subject.CommonName)
	}
}
