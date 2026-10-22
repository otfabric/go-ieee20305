# go-ieee20305 — IEEE 2030.5 for Go

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-ieee20305.svg)](https://pkg.go.dev/github.com/otfabric/go-ieee20305)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-ieee20305/graph/badge.svg)](https://codecov.io/gh/otfabric/go-ieee20305)

A Go implementation of [IEEE 2030.5](https://standards.ieee.org/ieee/2030.5/5897/) (Smart Energy Profile 2.0), the smart-grid communication standard for utility-to-DER communication. It covers IEEE 2030.5-2018 and IEEE 2030.5-2023, with support for the CSIP (California) and CSIP-AUS (Australia) implementation profiles. It provides both sides of the HTTPS/XML API:

- **`client`**: an IEEE 2030.5 client for device registration, DER control polling, event handling and telemetry reporting;
- **`server`**: an IEEE 2030.5 server as an embeddable `http.Handler`;
- **`ieee20305`** (the module root): the resource model both share, generated from the official IEEE 2030.5-2023 schema;
- **`csipaus`**: the CSIP-AUS v1.3 profile, generated from the official CSIP-AUS schemas;
- **`security`**: mutual TLS, LFDI/SFDI derivation, certificate chain validation;
- **`schedule`**: primacy-based event scheduling and the active/superseded/cancelled state machine;
- **`discovery`**: DNS-SD service discovery for IEEE 2030.5 servers.

## Package overview

| Package | Description |
|---|---|
| `ieee20305` | Every type of IEEE 2030.5-2023 (`sep.xsd` 2.2) with XML tags, validation, schema defaults, document codec, and the WADL resource table. Generated. |
| `csipaus` | CSIP-AUS v1.3: the redefined IEEE 2030.5 types and the CSIP-AUS namespace. Generated. |
| `client` | IEEE 2030.5 client over HTTPS with mutual TLS. |
| `server` | IEEE 2030.5 server as an `http.Handler`. |
| `security` | Certificate handling, LFDI/SFDI derivation, mTLS configuration. |
| `schedule` | Event scheduling: primacy, overlap resolution, randomization. |
| `discovery` | DNS-SD service discovery (`_smartenergy._tcp`). |
| `internal/xml` | Document encoding and decoding shared by `ieee20305` and `csipaus`. |
| `internal/gen` | The code generator, and the tooling that downloads and checks the upstream schemas. |

## Quick start

```go
package main

import (
	"fmt"
	"log"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
)

func main() {
	// A CSIP-AUS dynamic operating envelope: a 5 kW export limit.
	var ctl csipaus.DERControl
	ctl.MRID = ieee20305.MRIDType{0x01, 0x02, 0x03, 0x04}
	ctl.Interval = ieee20305.DateTimeInterval{Start: 1700000000, Duration: 1800}
	ctl.DERControlBase.OpModExpLimW = &ieee20305.ActivePower{Multiplier: 3, Value: 5}
	if err := ctl.Validate(); err != nil {
		log.Fatal(err)
	}
	doc, err := csipaus.Marshal(&ctl)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(doc))

	// Decode any IEEE 2030.5 document.
	v, err := ieee20305.Decode([]byte(`<Time xmlns="urn:ieee:std:2030.5:ns"><currentTime>1700000000</currentTime><dstEndTime>0</dstEndTime><dstOffset>0</dstOffset><dstStartTime>0</dstStartTime><quality>7</quality><tzOffset>0</tzOffset></Time>`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(v.(*ieee20305.Time).CurrentTime)
}
```

## Design principles

- **Zero external dependencies.** The module depends only on the Go standard library. `make nodeps` (part of `make check`) enforces this.
- **The specification is the source of truth.** The resource types are generated from the official IEEE 2030.5 and CSIP-AUS schemas by the generator in `internal/gen`; nothing in the model is maintained by hand. See [Code generation](#code-generation).
- **Certificate-based identity.** Device identity is derived from the device certificate (LFDI = SHA-256 fingerprint, SFDI = truncated fingerprint). No passwords, no tokens.
- **Standard Go conventions.** `gofmt`, `go vet`, the race detector, and fuzz testing.

## Development

```sh
# Run all checks (format, lint, vet, vulnerability scan, tests, coverage)
make check

# Run the library tests with the race detector
make test

# Build the examples into ./bin
make build

# Run the end-to-end tests
make e2e

# Run the interop suite against reference implementations (needs Docker)
make interop
```

Run `make` (or `make help`) for the full list of targets.

## Code generation

The `*_gen.go` files are generated from the upstream schemas pinned, with their
SHA-256, in [`schemas/manifest.json`](schemas/manifest.json): the IEEE 2030.5-2023
`sep.xsd` and `sep_wadl.xml`, and the CSIP-AUS v1.3 schemas. The upstream files
are not committed (IEEE copyright); the generated code is.

```sh
make download        # fetch the pinned schemas into schemas/upstream/ and verify them
make generate        # regenerate the *_gen.go files
make check-updates   # report upstream changes and new releases
```

Never edit a generated file: fix the generator in `internal/gen` or its inputs
in the manifest, and regenerate. CI regenerates on every push and fails on any
difference, validates encoded documents against the official schemas, and
checks weekly for upstream updates. See [`schemas/README.md`](schemas/README.md).

## License

[MIT](LICENSE)
