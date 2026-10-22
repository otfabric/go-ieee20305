// SPDX-License-Identifier: MIT

package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net"
	"time"
)

// TestCA generates a self-signed ECDSA P-256 CA certificate and key for
// testing. The CA is valid for 10 years.
func TestCA(org string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{org},
			CommonName:   org + " CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// TestServerCert generates a server certificate signed by the given CA.
// Each name becomes a DNS or, for an IP address literal, an IP subject
// alternative name; the first is also the common name. The certificate is
// valid for 1 year.
func TestServerCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if len(names) == 0 {
		return nil, nil, errors.New("a server certificate needs at least one name")
	}
	var dnsNames []string
	var ips []net.IP
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
		} else {
			dnsNames = append(dnsNames, n)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{ca.Subject.Organization[0]},
			CommonName:   names[0],
		},
		DNSNames:              dnsNames,
		IPAddresses:           ips,
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// TestDeviceCert generates a device (client) certificate signed by the
// given CA. The certificate is suitable for IEEE 2030.5 mTLS: ECDSA P-256,
// client auth EKU, 1 year validity.
func TestDeviceCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{ca.Subject.Organization[0]},
			CommonName:   commonName,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// TestDeviceCertHMN generates a device (client) certificate signed by the
// given CA whose only subject alternative name is the critical
// HardwareModuleName h, as in IEEE 2030.5 device certificates.
func TestDeviceCertHMN(ca *x509.Certificate, caKey *ecdsa.PrivateKey, h HMN) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	return testCertHMN(ca, caKey, h, x509.ExtKeyUsageClientAuth)
}

// TestServerCertHMN generates a server certificate signed by the given CA
// whose only subject alternative name is the critical HardwareModuleName h,
// as in IEEE 2030.5 server certificates: it names no host.
func TestServerCertHMN(ca *x509.Certificate, caKey *ecdsa.PrivateKey, h HMN) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	return testCertHMN(ca, caKey, h, x509.ExtKeyUsageServerAuth)
}

func testCertHMN(ca *x509.Certificate, caKey *ecdsa.PrivateKey, h HMN, usage x509.ExtKeyUsage) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	san, err := MarshalHMNSAN(h)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
		ExtraExtensions:       []pkix.Extension{{Id: oidSubjectAltName, Critical: true, Value: san}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// MarshalHMNSAN returns the DER of a subject alternative name extension
// value that holds only the HardwareModuleName h.
func MarshalHMNSAN(h HMN) ([]byte, error) {
	inner, err := asn1.Marshal(hmnASN1{HWType: h.Type, HWSerialNum: h.SerialNumber})
	if err != nil {
		return nil, err
	}
	typeID, err := asn1.Marshal(oidHardwareModuleName)
	if err != nil {
		return nil, err
	}
	value, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner})
	if err != nil {
		return nil, err
	}
	otherName := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tagOtherName, IsCompound: true, Bytes: append(typeID, value...)}
	return asn1.Marshal([]asn1.RawValue{otherName})
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
