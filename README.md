# go-ieee20305 — IEEE 2030.5 for Go

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-ieee20305.svg)](https://pkg.go.dev/github.com/otfabric/go-ieee20305)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-ieee20305/graph/badge.svg)](https://codecov.io/gh/otfabric/go-ieee20305)

A Go implementation of [IEEE 2030.5](https://standards.ieee.org/ieee/2030.5/5897/) (Smart Energy Profile 2.0), the smart-grid communication standard for utility-to-DER communication. It covers IEEE 2030.5-2018 and IEEE 2030.5-2023, with support for the CSIP (California) and CSIP-AUS (Australia) implementation profiles. It provides both sides of the HTTPS/XML API:

- **`client`**: an IEEE 2030.5 client for device registration, DER control polling, event handling and telemetry reporting;
- **`server`**: an IEEE 2030.5 server as an embeddable `http.Handler`;
- **`ieee20305`** (the module root): the domain model both share;
- **`security`**: mutual TLS, LFDI/SFDI derivation, certificate chain validation;
- **`schedule`**: primacy-based event scheduling and the active/superseded/cancelled state machine;
- **`discovery`**: DNS-SD service discovery for IEEE 2030.5 servers.

## Package overview

| Package | Description |
|---|---|
| `ieee20305` | Domain model: resources, devices, DER programs and controls, events, metering types. No encoding, no HTTP. |
| `client` | IEEE 2030.5 client over HTTPS with mutual TLS. |
| `server` | IEEE 2030.5 server as an `http.Handler`. |
| `security` | Certificate handling, LFDI/SFDI derivation, mTLS configuration. |
| `schedule` | Event scheduling: primacy, overlap resolution, randomization. |
| `discovery` | DNS-SD service discovery (`_smartenergy._tcp`). |
| `internal/xml` | XML encoding and decoding of IEEE 2030.5 resources. |

## Quick start

```go
package main

import (
	"fmt"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func main() {
	// The domain model is encoding-independent.
	ctrl := ieee20305.DERControl{
		DERControlBase: ieee20305.DERControlBase{
			OpModExpLimW: &ieee20305.ActivePower{
				Multiplier: 0,
				Value:      5000, // 5000 W export limit
			},
		},
	}
	fmt.Printf("Export limit: %d W\n", ctrl.DERControlBase.OpModExpLimW.Value)
}
```

## Design principles

- **Zero external dependencies.** The module depends only on the Go standard library. `make nodeps` (part of `make check`) enforces this.
- **Encoding-independent domain model.** The root package defines the types without XML tags. Wire encoding is an internal concern of the client and server packages.
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

## License

[MIT](LICENSE)
