// SPDX-License-Identifier: MIT

package interop

// The ieee20305-interop images the suite runs against, pinned by the digests
// of release v0.1.0 (its manifest.json). The environment variable named
// beside each overrides it, for example with a locally built image.
const (
	// EnvoyImage is ANU Envoy v2.5.0, an IEEE 2030.5 / CSIP-AUS server.
	// Override: IEEE20305_INTEROP_ENVOY_IMAGE.
	EnvoyImage = "ghcr.io/otfabric/ieee20305-interop-envoy@sha256:7c3369535b105c164ef0c19be51fc54e4f98e74ea5871ff88d5629b2426ed04a"
	// GridAPPSDImage is the GridAPPS-D server v0.18.0 and client v0.1.0.
	// Override: IEEE20305_INTEROP_GRIDAPPSD_IMAGE.
	GridAPPSDImage = "ghcr.io/otfabric/ieee20305-interop-gridappsd@sha256:df21dc9441a64e52c09cd8275a317b8fce3f40597328742b0cae0e66d12ccfee"
	// Py20305Image is the client of py2030_5 v0.16.0.
	// Override: IEEE20305_INTEROP_PY20305_IMAGE.
	Py20305Image = "ghcr.io/otfabric/ieee20305-interop-py20305@sha256:0c2258d613331e3e76ab61beafe880c25629997524be108ec6b6b68a33f9954e"
	// EPRIImage is the client of the EPRI IEEE 2030.5 client v0.2.11.
	// Override: IEEE20305_INTEROP_EPRI_IMAGE.
	EPRIImage = "ghcr.io/otfabric/ieee20305-interop-epri@sha256:8110f679c2e0704004a3747b96fb2eeea5d6d29e20443f21a755ddaff15764d9"
)
