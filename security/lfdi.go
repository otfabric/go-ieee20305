// SPDX-License-Identifier: MIT

package security

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

// LFDI is a Long-Form Device Identifier: the leftmost 160 bits (20 bytes)
// of the SHA-256 fingerprint of a DER-encoded certificate, represented as
// 40 uppercase hex characters.
type LFDI [20]byte

// CertificateLFDI derives the LFDI from a parsed certificate.
func CertificateLFDI(cert *x509.Certificate) LFDI {
	return DERBytesLFDI(cert.Raw)
}

// DERBytesLFDI derives the LFDI from DER-encoded certificate bytes.
func DERBytesLFDI(der []byte) LFDI {
	hash := sha256.Sum256(der)
	var lfdi LFDI
	copy(lfdi[:], hash[:20])
	return lfdi
}

// String returns the LFDI as a 40-character uppercase hex string.
func (l LFDI) String() string {
	return strings.ToUpper(hex.EncodeToString(l[:]))
}

// SFDI derives the Short-Form Device Identifier from this LFDI.
func (l LFDI) SFDI() SFDI {
	// The leftmost 36 bits of the fingerprint as a big-endian integer,
	// followed by a check digit.
	val := uint64(l[0])<<28 | uint64(l[1])<<20 | uint64(l[2])<<12 | uint64(l[3])<<4 | uint64(l[4])>>4
	return SFDI(val*10 + uint64(checkDigit(val)))
}

// ParseLFDI parses a 40-character hex string into an LFDI.
func ParseLFDI(s string) (LFDI, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return LFDI{}, fmt.Errorf("invalid LFDI hex: %w", err)
	}
	if len(b) != 20 {
		return LFDI{}, fmt.Errorf("LFDI must be 20 bytes (40 hex chars), got %d", len(b))
	}
	var lfdi LFDI
	copy(lfdi[:], b)
	return lfdi, nil
}

// SFDI is a Short-Form Device Identifier: the leftmost 36 bits of the
// SHA-256 fingerprint as a decimal number, followed by a check digit that
// makes the sum of all its digits a multiple of 10 (at most 12 digits).
type SFDI uint64

// String returns the SFDI as a decimal string.
func (s SFDI) String() string {
	return fmt.Sprintf("%d", s)
}

// Valid reports whether the SFDI's check digit is correct.
func (s SFDI) Valid() bool {
	return checkDigit(uint64(s)/10) == uint8(uint64(s)%10)
}

// checkDigit returns the digit that makes the sum of the decimal digits of
// n and the digit a multiple of 10.
func checkDigit(n uint64) uint8 {
	var sum uint64
	for ; n > 0; n /= 10 {
		sum += n % 10
	}
	return uint8((10 - sum%10) % 10)
}
