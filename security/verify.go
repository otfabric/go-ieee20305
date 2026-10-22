// SPDX-License-Identifier: MIT

package security

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

var (
	// oidSubjectAltName is the subjectAltName extension (RFC 5280 4.2.1.6).
	oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	// oidHardwareModuleName is the HardwareModuleName otherName (RFC 4108 5).
	oidHardwareModuleName = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4}
)

// GeneralName tags (RFC 5280 4.2.1.6).
const (
	tagOtherName = 0
	tagDNSName   = 2
	tagIPAddress = 7
)

// HMN is an RFC 4108 HardwareModuleName: the type of a hardware module and
// its serial number. IEEE 2030.5 device and server certificates carry one
// as an otherName of their subject alternative name.
type HMN struct {
	Type         asn1.ObjectIdentifier
	SerialNumber []byte
}

// hmnASN1 is the DER form of a HardwareModuleName.
type hmnASN1 struct {
	HWType      asn1.ObjectIdentifier
	HWSerialNum []byte
}

// sanInfo is what a subject alternative name holds.
type sanInfo struct {
	hmn   []HMN
	names bool // DNS names or IP addresses
	other bool // any other GeneralName or otherName
}

// parseSAN parses the DER of a subjectAltName extension. It fails on any
// malformed or trailing data.
func parseSAN(der []byte) (sanInfo, error) {
	var info sanInfo
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(der, &seq)
	if err != nil {
		return info, err
	}
	if len(rest) > 0 {
		return info, errors.New("trailing data after subject alternative name")
	}
	if seq.Class != asn1.ClassUniversal || seq.Tag != asn1.TagSequence || !seq.IsCompound {
		return info, errors.New("subject alternative name is not a sequence")
	}
	for in := seq.Bytes; len(in) > 0; {
		var gn asn1.RawValue
		if in, err = asn1.Unmarshal(in, &gn); err != nil {
			return info, err
		}
		if gn.Class != asn1.ClassContextSpecific {
			return info, errors.New("general name is not context-specific")
		}
		switch gn.Tag {
		case tagOtherName:
			h, ok, err := parseOtherName(gn)
			if err != nil {
				return info, err
			}
			if ok {
				info.hmn = append(info.hmn, h)
			} else {
				info.other = true
			}
		case tagDNSName, tagIPAddress:
			info.names = true
		default:
			info.other = true
		}
	}
	return info, nil
}

// parseOtherName parses an otherName GeneralName and reports whether it is
// a HardwareModuleName.
func parseOtherName(gn asn1.RawValue) (HMN, bool, error) {
	if !gn.IsCompound {
		return HMN{}, false, errors.New("otherName is not constructed")
	}
	var typeID asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(gn.Bytes, &typeID)
	if err != nil {
		return HMN{}, false, err
	}
	var value asn1.RawValue
	if rest, err = asn1.Unmarshal(rest, &value); err != nil {
		return HMN{}, false, err
	}
	if len(rest) > 0 || value.Class != asn1.ClassContextSpecific || value.Tag != 0 || !value.IsCompound {
		return HMN{}, false, errors.New("malformed otherName value")
	}
	if !typeID.Equal(oidHardwareModuleName) {
		return HMN{}, false, nil
	}
	var h hmnASN1
	if rest, err = asn1.Unmarshal(value.Bytes, &h); err != nil {
		return HMN{}, false, err
	}
	if len(rest) > 0 {
		return HMN{}, false, errors.New("trailing data after HardwareModuleName")
	}
	return HMN{Type: h.HWType, SerialNumber: h.HWSerialNum}, true, nil
}

// HardwareModuleName returns the first RFC 4108 HardwareModuleName in the
// subject alternative name of c, and whether c has one.
func HardwareModuleName(c *x509.Certificate) (HMN, bool) {
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		info, err := parseSAN(ext.Value)
		if err != nil || len(info.hmn) == 0 {
			return HMN{}, false
		}
		return info.hmn[0], true
	}
	return HMN{}, false
}

// VerifyOptions are the options of Verify.
type VerifyOptions struct {
	// Roots are the trusted CAs: the CAs of the IEEE 2030.5 PKI. Required:
	// a nil pool fails verification rather than falling back to the
	// system roots, which would let any publicly trusted certificate in.
	Roots *x509.CertPool
	// Intermediates are CAs that may complete the chain besides the
	// certificates that follow the leaf in the chain itself.
	Intermediates *x509.CertPool
	// ServerName is the expected host name or IP address of the peer. It
	// is checked only when the leaf has DNS or IP subject alternative
	// names; certificates that name only a hardware module are identified
	// by their chain and PinnedLFDI.
	ServerName string
	// PinnedLFDI, when set, is the LFDI the leaf must have.
	PinnedLFDI *LFDI
	// Usage is the extended key usage the chain must allow. Zero
	// (x509.ExtKeyUsageAny) accepts any usage.
	Usage x509.ExtKeyUsage
	// Now returns the time to verify at. Nil uses time.Now.
	Now func() time.Time
}

// Verify verifies the certificate chain of a peer, leaf first, against the
// roots of o. Unlike x509.Certificate.Verify, it accepts the critical
// subject alternative name of IEEE 2030.5 certificates when that name holds
// only RFC 4108 HardwareModuleName otherNames besides DNS names and IP
// addresses. Every other unhandled critical extension fails verification.
//
// Its errors match ieee20305.ErrCertificate.
func Verify(chain []*x509.Certificate, o VerifyOptions) error {
	if len(chain) == 0 || chain[0] == nil {
		return fmt.Errorf("%w: no peer certificate", ieee20305.ErrCertificate)
	}
	if o.Roots == nil {
		return fmt.Errorf("%w: no trusted CAs to verify the peer with", ieee20305.ErrCertificate)
	}
	var inter *x509.CertPool
	if o.Intermediates != nil {
		inter = o.Intermediates.Clone()
	} else {
		inter = x509.NewCertPool()
	}
	certs := make([]*x509.Certificate, len(chain))
	for i, c := range chain {
		if c == nil {
			return fmt.Errorf("%w: nil certificate at position %d of the chain", ieee20305.ErrCertificate, i)
		}
		certs[i] = tolerateHMN(c)
		if i > 0 {
			inter.AddCert(certs[i])
		}
	}
	leaf := certs[0]

	opts := x509.VerifyOptions{
		Roots:         o.Roots,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{o.Usage},
	}
	if o.Now != nil {
		opts.CurrentTime = o.Now()
	}
	if _, err := leaf.Verify(opts); err != nil {
		return certError(err)
	}
	if o.ServerName != "" && (len(leaf.DNSNames) > 0 || len(leaf.IPAddresses) > 0) {
		if err := leaf.VerifyHostname(o.ServerName); err != nil {
			return certError(err)
		}
	}
	if o.PinnedLFDI != nil {
		if got := CertificateLFDI(leaf); got != *o.PinnedLFDI {
			return fmt.Errorf("%w: certificate LFDI %s, want %s", ieee20305.ErrCertificate, got, o.PinnedLFDI)
		}
	}
	return nil
}

// tolerateHMN returns c, or a copy of c whose critical subject alternative
// name no longer counts as unhandled when it holds HardwareModuleNames and
// nothing else but DNS names and IP addresses, which crypto/x509 checks.
func tolerateHMN(c *x509.Certificate) *x509.Certificate {
	i := indexOID(c.UnhandledCriticalExtensions, oidSubjectAltName)
	if i < 0 {
		return c
	}
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		info, err := parseSAN(ext.Value)
		if err != nil || info.other || len(info.hmn) == 0 {
			return c
		}
		cp := *c
		cp.UnhandledCriticalExtensions = make([]asn1.ObjectIdentifier, 0, len(c.UnhandledCriticalExtensions)-1)
		cp.UnhandledCriticalExtensions = append(cp.UnhandledCriticalExtensions, c.UnhandledCriticalExtensions[:i]...)
		cp.UnhandledCriticalExtensions = append(cp.UnhandledCriticalExtensions, c.UnhandledCriticalExtensions[i+1:]...)
		return &cp
	}
	return c
}

func indexOID(oids []asn1.ObjectIdentifier, oid asn1.ObjectIdentifier) int {
	for i, o := range oids {
		if o.Equal(oid) {
			return i
		}
	}
	return -1
}

// VerifyPeerCertificate returns a tls.Config.VerifyPeerCertificate callback
// that parses the peer's certificates and verifies them with Verify.
func VerifyPeerCertificate(o VerifyOptions) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("%w: no peer certificate", ieee20305.ErrCertificate)
		}
		chain := make([]*x509.Certificate, len(rawCerts))
		for i, raw := range rawCerts {
			c, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("%w: peer certificate %d: %w", ieee20305.ErrCertificate, i, err)
			}
			chain[i] = c
		}
		return Verify(chain, o)
	}
}

// Equal reports whether h and o name the same hardware module.
func (h HMN) Equal(o HMN) bool {
	return h.Type.Equal(o.Type) && bytes.Equal(h.SerialNumber, o.SerialNumber)
}

// certError wraps an error of crypto/x509 so that it matches both
// ieee20305.ErrCertificate and what crypto/x509 returned (such as an
// x509.UnknownAuthorityError).
func certError(err error) error {
	return fmt.Errorf("%w: %w", ieee20305.ErrCertificate, err)
}
