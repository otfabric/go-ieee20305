// SPDX-License-Identifier: MIT

// Package interop holds tests of the client and the server of go-ieee20305
// against IEEE 2030.5 implementations of others: the container images of
// github.com/otfabric/ieee20305-interop, pinned by digest in images.go.
//
//   - Our client against each server image (Envoy, GridAPPS-D): the tests
//     start the server seeded with the baseline fixture it carries, and
//     check what the client reads and writes against that fixture.
//   - Our server against each client image (GridAPPS-D, py20305, EPRI): the
//     tests seed the server from the fixture of the client image, serve it
//     on the host, and run the client's operations against it.
//
// A pairing runs over the cipher suites both ends have: the client of this
// module speaks TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 through the tlsccm module,
// and AES-128-GCM through crypto/tls. The images declare theirs
// (print-capabilities); a pairing or operation an image does not declare is
// skipped.
//
// It is a module of its own, as it depends on tlsccm, and needs Docker:
//
//	make test-interop
package interop
