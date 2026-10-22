// SPDX-License-Identifier: MIT

// Package e2e holds end-to-end tests of go-ieee20305 against itself:
// the client of this module against the server of this module, over
// real HTTPS connections with mutual TLS on loopback.
//
// Where package interop shows that each side agrees with other
// implementations, this package shows that the two sides agree with
// each other, in every feature, including those no other implementation
// can exercise yet.
package e2e
