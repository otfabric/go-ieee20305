// SPDX-License-Identifier: MIT

package interop

// The ieee20305-interop images the suite runs against, pinned by the digests
// of release v0.2.0 (its manifest.json; container contract 0.3). The environment variable named
// beside each overrides it, for example with a locally built image. The
// Interop workflow pins the same digests (TestWorkflowPins).
const (
	// EnvoyImage is ANU Envoy v2.5.0, an IEEE 2030.5 / CSIP-AUS server.
	// Override: IEEE20305_INTEROP_ENVOY_IMAGE.
	EnvoyImage = "ghcr.io/otfabric/ieee20305-interop-envoy@sha256:2dd3587071de81d7b96a7dbf65bf414a06a643a32f5d785b42ba7ebb0cb04d2d"
	// GridAPPSDImage is the GridAPPS-D server v0.18.0 and client v0.1.0.
	// Override: IEEE20305_INTEROP_GRIDAPPSD_IMAGE.
	GridAPPSDImage = "ghcr.io/otfabric/ieee20305-interop-gridappsd@sha256:7b04e6c482df6536d15ba06e17e4c317a68031cabeed54cc03bc5b395f5b8d68"
	// Py20305Image is the client of py2030_5 v0.16.0.
	// Override: IEEE20305_INTEROP_PY20305_IMAGE.
	Py20305Image = "ghcr.io/otfabric/ieee20305-interop-py20305@sha256:de29e6bd5da56d33199e5e73bc11aa2bd48e4c05c2bacc5c42796e81940045f2"
	// EPRIImage is the client of the EPRI IEEE 2030.5 client v0.2.11.
	// Override: IEEE20305_INTEROP_EPRI_IMAGE.
	EPRIImage = "ghcr.io/otfabric/ieee20305-interop-epri@sha256:f11a38feee6d971d0027bc9b24ca1b3bc4373018c168fa29c3b902b9c322188f"
)
