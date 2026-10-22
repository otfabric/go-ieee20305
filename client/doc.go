// SPDX-License-Identifier: MIT

// Package client is an IEEE 2030.5 client: it reads and writes the
// resources of one server over HTTPS with mutual TLS, identifying the
// device by its certificate.
//
// A Client resolves the hrefs of resources against its server and refuses
// links and redirects to another server. It reads documents with Get,
// GetList and Fetch, starting from DeviceCapability, and writes them with
// Post, Put and Delete. A failure is a *ieee20305.StatusError carrying the
// server's Error document when it sent one, or matches ieee20305.ErrProtocol
// or ieee20305.ErrTimeout. With csipaus.Codec as its Codec, a client reads
// and writes CSIP-AUS documents.
//
// A device registers with Register: it finds its EndDevice in the server's
// EndDeviceList by the SFDI of its certificate (IdentityOf), or creates it.
// Registration retrieves what the server registered, and CheckPIN checks the
// PIN the device was given out of band.
//
// Program and control polling, event handling and telemetry reporting are
// built on these calls in later releases.
package client
