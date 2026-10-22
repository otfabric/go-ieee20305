// SPDX-License-Identifier: MIT

// Package server implements an IEEE 2030.5 server as an http.Handler.
// It manages device registration, DER programs and controls, event
// lifecycle, metering, and certificate-based access control.
//
// The server authenticates devices by their client certificate,
// derives the LFDI from the certificate fingerprint, and enforces
// access control based on device identity. It serves XML resource
// representations over HTTPS with mutual TLS.
package server
