// SPDX-License-Identifier: MIT

// Package client implements an IEEE 2030.5 client that communicates
// with a utility server over HTTPS with mutual TLS. It handles device
// registration, resource discovery, DER program and control polling,
// event scheduling, and telemetry reporting.
//
// The client identifies itself with a device certificate from which its
// LFDI and SFDI are derived. All communication uses the XML encoding
// defined by the standard.
package client
