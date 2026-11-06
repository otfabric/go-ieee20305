// SPDX-License-Identifier: MIT

package interop

// The ieee20305-interop images the suite runs against, pinned by the digests
// of release v0.4.1 (its manifest.json; container contract 0.3). The environment variable named
// beside each overrides it, for example with a locally built image. The
// Interop workflow pins the same digests (TestWorkflowPins).
const (
	// EnvoyImage is ANU Envoy v2.5.0, an IEEE 2030.5 / CSIP-AUS server.
	// Override: IEEE20305_INTEROP_ENVOY_IMAGE.
	EnvoyImage = "ghcr.io/otfabric/ieee20305-interop-envoy@sha256:7e040b623dd8fb95b05330270c0d52eb75f5cbf54e243a5b18f6fa8235697359"
	// GridAPPSDImage is the GridAPPS-D server v0.19.0 and client v0.1.0.
	// Override: IEEE20305_INTEROP_GRIDAPPSD_IMAGE.
	GridAPPSDImage = "ghcr.io/otfabric/ieee20305-interop-gridappsd@sha256:40d77b1ecb8a2f7e98a04267c5f25baca96a619c66c79a7b762685975e7ba082"
	// Py20305Image is the client of py2030_5 v0.16.2.
	// Override: IEEE20305_INTEROP_PY20305_IMAGE.
	Py20305Image = "ghcr.io/otfabric/ieee20305-interop-py20305@sha256:e42a719905aa2b464d1d9458e2f36d34b8e1533b251a4c9707dc91b4d37389f3"
	// EPRIImage is the client of the EPRI IEEE 2030.5 client v0.2.11.
	// Override: IEEE20305_INTEROP_EPRI_IMAGE.
	EPRIImage = "ghcr.io/otfabric/ieee20305-interop-epri@sha256:ce2be5554928c0dcd4b7f2d78c26c3768f5896b120e38f16a066423946dbcd2d"
)
