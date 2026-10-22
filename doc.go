// SPDX-License-Identifier: MIT

// Package ieee20305 defines the domain model of IEEE 2030.5 (Smart Energy
// Profile 2.0): resources, devices, DER programs and controls, events,
// metering, subscriptions, and the types they are built from.
//
// The types in this package are independent of any wire encoding. Encoding
// and decoding of the XML representation that IEEE 2030.5 specifies is an
// internal concern of the client and server packages, so the structures
// here carry no XML tags and are not meant to be marshalled directly.
//
// Entry points:
//
//   - package client: an IEEE 2030.5 client for a utility server.
//   - package server: an IEEE 2030.5 server as an http.Handler.
//   - package security: mutual TLS, LFDI/SFDI derivation, certificate
//     chain validation.
//   - package schedule: primacy-based event scheduling and the
//     active/superseded/cancelled state machine.
//   - package discovery: DNS-SD service discovery for IEEE 2030.5 servers.
//
// The standard versions covered are IEEE 2030.5-2018 and IEEE 2030.5-2023.
// Implementation profiles CSIP (California) and CSIP-AUS (Australia) are
// supported where they extend or constrain the base standard.
package ieee20305
