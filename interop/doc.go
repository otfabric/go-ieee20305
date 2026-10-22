// SPDX-License-Identifier: MIT

//go:build interop

// Package interop holds tests that run this module's client against
// IEEE 2030.5 servers implemented by others: the container images of
// github.com/otfabric/ieee20305-interop, pinned by digest in images.go. The
// tests generate the TLS material with the security package, start each
// server with Docker, seeded with the baseline fixture it carries, and check
// what the client reads and writes against that fixture.
//
// They are excluded from ordinary builds by the "interop" build tag:
//
//	make test-interop
//
// Today the suite runs against Envoy. The GridAPPS-D server offers only
// TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, which crypto/tls does not implement.
package interop
