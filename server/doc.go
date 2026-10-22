// SPDX-License-Identifier: MIT

// Package server implements an IEEE 2030.5 server as an http.Handler.
// It manages device registration, DER programs and controls, event
// lifecycle, metering, and certificate-based access control.
//
// The server authenticates devices by their client certificate,
// derives the LFDI from the certificate fingerprint, and enforces
// access control based on device identity. It serves XML resource
// representations over HTTPS with mutual TLS.
//
// The operator tells the server of its devices with Admin.ProvisionDevice:
// their edition, their Role (a device, an aggregator that manages others,
// or an operator's head-end) and their registration PIN. The server creates
// the EndDevice of a device with its resources. With
// Config.InBandRegistration, an unknown device registers itself by posting
// its EndDevice, and Admin.Register registers it later.
package server
