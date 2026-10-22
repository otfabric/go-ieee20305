// SPDX-License-Identifier: MIT

//go:build interop

// Package interop holds tests that run this module's client against
// IEEE 2030.5 servers implemented by others. They are excluded from
// ordinary builds by the "interop" build tag and need the peer
// environments of github.com/otfabric/ieee20305-interop to be running.
package interop
