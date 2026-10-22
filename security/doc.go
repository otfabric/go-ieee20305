// SPDX-License-Identifier: MIT

// Package security implements IEEE 2030.5 certificate handling: mutual
// TLS configuration, LFDI and SFDI derivation from device certificates,
// certificate chain validation, and the TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8
// cipher suite required by the standard.
//
// IEEE 2030.5 uses ECDSA certificates on the NIST P-256 curve. The
// Long-Form Device Identifier (LFDI) is the leftmost 160 bits of the
// SHA-256 fingerprint of the device certificate. The Short-Form Device
// Identifier (SFDI) is the leftmost 36 bits of the same fingerprint,
// with a four-bit checksum appended.
package security
