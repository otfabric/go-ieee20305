// SPDX-License-Identifier: MIT

package interop

// The ieee20305-interop images the suite runs against, pinned by the digests
// of release v0.3.1 (its manifest.json; container contract 0.3). The environment variable named
// beside each overrides it, for example with a locally built image. The
// Interop workflow pins the same digests (TestWorkflowPins).
const (
	// EnvoyImage is ANU Envoy v2.5.0, an IEEE 2030.5 / CSIP-AUS server.
	// Override: IEEE20305_INTEROP_ENVOY_IMAGE.
	EnvoyImage = "ghcr.io/otfabric/ieee20305-interop-envoy@sha256:850d16d293866fbd424716df6f3fb5ebb954a87616e10b082dbe5ea16c3f1bdb"
	// GridAPPSDImage is the GridAPPS-D server v0.18.0 and client v0.1.0.
	// Override: IEEE20305_INTEROP_GRIDAPPSD_IMAGE.
	GridAPPSDImage = "ghcr.io/otfabric/ieee20305-interop-gridappsd@sha256:c72fbdb7c91fae1a59ee87938cc834651db2924317dd362814dc682d57a5b3af"
	// Py20305Image is the client of py2030_5 v0.16.0.
	// Override: IEEE20305_INTEROP_PY20305_IMAGE.
	Py20305Image = "ghcr.io/otfabric/ieee20305-interop-py20305@sha256:d48c7680428fd4f438fae2f187d547fdf5e85e0961e5c9a582e3501dd20e38e9"
	// EPRIImage is the client of the EPRI IEEE 2030.5 client v0.2.11.
	// Override: IEEE20305_INTEROP_EPRI_IMAGE.
	EPRIImage = "ghcr.io/otfabric/ieee20305-interop-epri@sha256:364ce856b5eeeb715dbeb07ee7533ab389129a456426ae5895a6bba91f8fa4ee"
)
