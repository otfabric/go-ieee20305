# go-ieee20305 — IEEE 2030.5 for Go

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-ieee20305.svg)](https://pkg.go.dev/github.com/otfabric/go-ieee20305)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-ieee20305/graph/badge.svg)](https://codecov.io/gh/otfabric/go-ieee20305)

A Go implementation of [IEEE 2030.5](https://standards.ieee.org/ieee/2030.5/5897/) (Smart Energy Profile 2.0), the smart-grid communication standard for utility-to-DER communication. It covers IEEE 2030.5-2018 and IEEE 2030.5-2023, with support for the CSIP (California) and CSIP-AUS (Australia) implementation profiles. It provides both sides of the HTTPS/XML API:

- **`client`**: an IEEE 2030.5 client: resources, lists, walks of the resource tree, registration, subscriptions and notifications;
- **`agent`**: the behaviour of a device on the client: registration and PIN check, polling, the events of every function set (DER, DRLC, pricing, messaging, prepayment, flow reservation) with their Responses, metering mirrors and files;
- **`server`**: an IEEE 2030.5 server as an embeddable `http.Handler`, for every resource of the WADL: device provisioning and in-band registration, aggregators and operators, event lifecycle, subscriptions with notifications, metering mirrors, flow reservations;
- **`ieee20305`** (the module root): the resource model both share, generated from the official IEEE 2030.5-2023 schema;
- **`csipaus`**: the CSIP-AUS v1.3 profile, generated from the official CSIP-AUS schemas;
- **`security`**: mutual TLS, LFDI/SFDI derivation, certificate chain validation;
- **`schedule`**: primacy-based event scheduling and the active/superseded/cancelled state machine;
- **`discovery`**: DNS-SD service discovery for IEEE 2030.5 servers.

## IEEE 2030.5 in five minutes

- **REST over HTTPS, XML on the wire.** A server exposes resources (`application/sep+xml`); clients `GET`, `PUT`, `POST` and `DELETE` them. Everything starts at **DeviceCapability** (`/dcap`), and clients find every other resource by following **links**, never by building paths.
- **Mutual TLS is identity.** Every device has a certificate. Its SHA-256 fingerprint gives the **LFDI** (long form) and **SFDI** (short form, with a check digit) that name the device. There are no passwords. The standard's cipher suite is `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8`.
- **EndDevice and registration.** Each device has an **EndDevice** in the server's EndDeviceList. Its **Registration** carries a **PIN**, which the device checks against the PIN it was given out of band before it trusts the server.
- **Function set assignments.** An EndDevice's **FunctionSetAssignments** (FSAs) tell it which programs apply to it: DER programs, demand response, pricing, messaging, prepayment, files and billing.
- **Programs and events.** A program (such as a **DERProgram**) has a **primacy**, where lower wins, and a list of time-bound **events** (such as a **DERControl**: "limit export to 5 kW from 14:00 for 30 minutes"). Clients schedule the events themselves. When events conflict, primacy and creation time decide which one applies, start and duration may be randomized, and the server can cancel events. A **DefaultDERControl** applies when no event does.
- **Responses.** An event may ask (`responseRequired`, `replyTo`) for **Responses** when the device receives it, starts it, completes it, or sees it cancelled or superseded.
- **Polling or subscriptions.** Clients poll at the server's `pollRate`, or **subscribe** to a resource and receive **Notifications** at an HTTPS endpoint of their own.
- **Lists.** Collections are lists that clients page through with `s` (start) and `l` (limit). A **ListLink** gives the number of items in `all`.
- **Metering.** Devices report readings by posting a **MirrorUsagePoint** and its **MirrorMeterReadings**, which the server exposes as UsagePoints and MeterReadings.
- **Editions and profiles.** IEEE 2030.5-2018 and -2023 differ in optional elements and resources. Profiles constrain or extend the standard: **CSIP** (California) and **CSIP-AUS** (Australia, which adds site limits, the dynamic operating envelopes).

## Package overview

| Package | Description |
|---|---|
| `ieee20305` | Every type of IEEE 2030.5-2023 (`sep.xsd` 2.2) with XML tags, validation, schema defaults, document codec, and the WADL resource table. Generated. |
| `csipaus` | CSIP-AUS v1.3: the redefined IEEE 2030.5 types and the CSIP-AUS namespace. Generated. |
| `client` | IEEE 2030.5 client over HTTPS with mutual TLS. |
| `agent` | Device behaviour on `client`: registration, polling, events, Responses, metering. |
| `server` | IEEE 2030.5 server as an `http.Handler`, with `Admin` to provision and seed it. |
| `csipaus/csipserver`, `csipaus/csipclient` | CSIP-AUS for the server (profile, ConnectionPoint, site limits) and the client. |
| `security` | Certificate handling, LFDI/SFDI derivation, mTLS configuration. |
| `schedule` | Event scheduling: primacy, overlap resolution, randomization. |
| `discovery` | DNS-SD service discovery (`_smartenergy._tcp`). |
| `internal/xml` | Document encoding and decoding shared by `ieee20305` and `csipaus`. |
| `internal/gen` | The code generator, and the tooling that downloads and checks the upstream schemas. |
| `tlsccm` (module) | The cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, which `crypto/tls` lacks, through the TLS fork of GridAPPS-D. A module of its own, with dependencies. |
| `interop` (module) | Tests of the client and the server against other implementations: the images of [ieee20305-interop](https://github.com/otfabric/ieee20305-interop). |

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

- **Zero external dependencies.** The module depends only on the Go standard library. `make nodeps` (part of `make check`) enforces this. The nested modules `tlsccm` and `interop` have dependencies of their own.
- **The specification is the source of truth.** The resource types are generated from the official IEEE 2030.5 and CSIP-AUS schemas by the generator in `internal/gen`; nothing in the model is maintained by hand. See [Code generation](#code-generation).
- **Certificate-based identity.** Device identity is derived from the device certificate (LFDI = SHA-256 fingerprint, SFDI = truncated fingerprint). No passwords, no tokens.
- **Standard Go conventions.** `gofmt`, `go vet`, the race detector, and fuzz testing.
- **Tested end to end and against others.** `e2e` runs agents against the server over mutual TLS, with every function set, under injected faults (lost, replayed and truncated requests, 503s, aborted responses, connection resets during the TLS handshake, clock skew) and with 500 agents at once; `interop` runs the client against the Envoy and GridAPPS-D servers and the GridAPPS-D, py20305 and EPRI clients against the server. Statement coverage is gated at 90%.

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

# Run the tests shuffled, twice, with the race detector
make stress

# Run every fuzz target (FUZZTIME=30s each by default)
make fuzz

# Test the CCM_8 module (Go 1.26.8)
make test-tlsccm

# Run the client and the server against the ieee20305-interop images (needs Docker)
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
