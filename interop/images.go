// SPDX-License-Identifier: MIT

//go:build interop

package interop

// The ieee20305-interop images the suite runs against, pinned by the digests
// of release v0.1.0 (its manifest.json). The environment variable named
// beside each overrides it, for example with a locally built image.
const (
	// envoyImage is ANU Envoy v2.5.0, an IEEE 2030.5 / CSIP-AUS server.
	// Override: IEEE20305_INTEROP_ENVOY_IMAGE.
	envoyImage = "ghcr.io/otfabric/ieee20305-interop-envoy@sha256:7c3369535b105c164ef0c19be51fc54e4f98e74ea5871ff88d5629b2426ed04a"
)
