# go-ieee20305 - IEEE 2030.5 Client and Server Library for Go

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-ieee20305.svg)](https://pkg.go.dev/github.com/otfabric/go-ieee20305)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-ieee20305/actions/workflows/ci.yml)
[![Interop](https://github.com/otfabric/go-ieee20305/actions/workflows/interop.yml/badge.svg)](https://github.com/otfabric/go-ieee20305/actions/workflows/interop.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-ieee20305/graph/badge.svg)](https://codecov.io/gh/otfabric/go-ieee20305)
[![Latest Release](https://img.shields.io/github/v/release/otfabric/go-ieee20305?label=release)](https://github.com/otfabric/go-ieee20305/releases)

A pure Go implementation of [IEEE 2030.5](https://standards.ieee.org/ieee/2030.5/5897/)
(Smart Energy Profile 2.0), the protocol between utilities, aggregators and
distributed energy resources (DER): inverters, batteries, EV chargers and
meters. Editions 2018 and 2023, with the CSIP (California) and CSIP-AUS
(Australia) profiles. No cgo, no dependencies outside the standard library.

The library provides:

- A **client** of one server over mutual TLS: every resource and list, paging,
  walks of the resource tree, registration with the PIN check, subscriptions
  and a notification handler
- A device **agent** on the client: registration, polling at the server's
  pollRate, the events of every function set (DER, demand response, pricing,
  messaging, prepayment, flow reservation) scheduled by primacy with
  randomization, the Responses they ask for, default controls, metering
  mirrors, files, log events and subscriptions
- A **server** as an embeddable `http.Handler`, for every resource of the
  IEEE 2030.5 WADL: device provisioning and in-band registration,
  aggregators and operators, an event lifecycle that keeps the EventStatus
  and the Active lists, Responses, subscriptions with retried and coalesced
  notifications, metering mirrors and flow reservations, and an `Admin` API
  to provision devices and publish programs, events and tariffs
- The **resource model**, generated from the official IEEE 2030.5-2023 and
  CSIP-AUS v1.3 schemas: every type with validation, schema defaults and a
  codec that writes either edition
- **Certificates and discovery**: LFDI and SFDI, verification of
  certificates with a HardwareModuleName, DNS-SD, and the CCM_8 cipher suite
  in the module `tlsccm`
- What production use needs: `context.Context` everywhere, one taxonomy of
  errors, retry policies, structured logging and metrics hooks, bounded lists
  and queues
- **Verified against third-party stacks**: every push runs the client
  against the Envoy and GridAPPS-D servers, and the GridAPPS-D, py20305 and
  EPRI clients against the server, for every operation each offers

**Docs:** [API.md](API.md) (public API) · [ARCHITECTURE.md](ARCHITECTURE.md) (packages, server, agent, generator) · [INTEROPERABILITY.md](INTEROPERABILITY.md) (what is implemented, and the known deviations) · [docs/FEATURES.md](docs/FEATURES.md) (features against the tests and the other stacks) · [ERRORS.md](ERRORS.md) (error semantics) · [OBSERVABILITY.md](OBSERVABILITY.md) (logging, metrics, retries) · [SECURITY.md](SECURITY.md) · [RELEASE.md](RELEASE.md) (changelog) · [examples](examples/README.md)

## Table of contents

- [IEEE 2030.5 in five minutes](#ieee-20305-in-five-minutes)
- [go-ieee20305 vs Envoy, GridAPPS-D, py20305 and EPRI](#go-ieee20305-vs-envoy-gridapps-d-py20305-and-epri)
- [Install](#install)
- [Project structure](#project-structure)
- [Client quickstart](#client-quickstart)
- [Server quickstart](#server-quickstart)
- [Production features](#production-features)
- [Error semantics](#error-semantics)
- [Examples](#examples)
- [Development](#development)
- [Code generation](#code-generation)
- [Interop tests](#interop-tests)
- [Limitations](#limitations)
- [License](#license)

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

## go-ieee20305 vs Envoy, GridAPPS-D, py20305 and EPRI

go-ieee20305 is tested against four other implementations (see
[Interop tests](#interop-tests)). All are useful projects; this table shows
where they differ so you can pick the right one. The details, feature by
feature and with what is tested, are in [docs/FEATURES.md](docs/FEATURES.md).

| Capability | otfabric/go-ieee20305 | [Envoy](https://github.com/bsgip/envoy) | [GridAPPS-D](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) | [py20305](https://github.com/DERSecurity/py20305) | [EPRI client](https://github.com/epri-dev/IEEE-2030.5-Client) |
|---|:---:|:---:|:---:|:---:|:---:|
| Language | Go | Python | Go | Python | C |
| License | MIT | MIT | Battelle BSD | Apache-2.0 | BSD-3-Clause |
| Client / device | ✅ library and agent | — | ✅ | ✅ | ✅ |
| Server | ✅ embeddable `http.Handler` | ✅ service with a database | ✅ | — | — |
| IEEE 2030.5-2018 / 2023 | ✅ / ✅ | ✅ / — | ✅ / ✅ | ✅ / ✅ | ✅ (SEP 2.0.4 draft) / — |
| CSIP-AUS | ✅ | ✅ | — | ✅ | — |
| Every function set of the WADL | ✅ | DER, pricing, metering ¹ | DER, messaging, flow reservation, metering ¹ | DER, pricing, metering ¹ | DER, partly DRLC, metering ¹ |
| Subscriptions and notifications | ✅ both ends | aggregators only | ✅ | ✅ | — |
| CCM_8 cipher suite | ✅ module `tlsccm` ² | ✅ | ✅ | ✅ | ✅ |
| Dependencies | standard library only | PostgreSQL and more | Go modules | Python packages | OpenSSL |

1. As far as the interop images exercise them; see
   [docs/FEATURES.md](docs/FEATURES.md).
2. `crypto/tls` has no CCM_8: the core module uses the AES-GCM suites, and
   the separate module `tlsccm` adds CCM_8 with a copy of Go 1.22's
   `crypto/tls`, checked against Go's security releases.

Choose **go-ieee20305** to embed a client, a device agent or a server in a Go
program, with every function set and both editions. Choose **Envoy** for a
CSIP-AUS utility server as a ready service with its database and admin API.
Choose **GridAPPS-D** for a Go server and client within the GridAPPS-D
platform. Choose **py20305** for a Python client with CSIP-AUS. Choose the
**EPRI client** for a C client on constrained devices.

## Install

```sh
go get github.com/otfabric/go-ieee20305@latest
```

Go 1.23 or later. The CCM_8 module needs Go 1.26.8:

```sh
go get github.com/otfabric/go-ieee20305/tlsccm@latest
```

## Project structure

```
go-ieee20305/
├── *_gen.go, codec.go, …   the resource model, generated from sep.xsd 2.2 and the WADL
├── csipaus/                CSIP-AUS v1.3 types (generated)
│   ├── csipserver/         the CSIP-AUS profile of a server, envelopes, default limits
│   └── csipclient/         the ConnectionPoint of a device
├── client/                 IEEE 2030.5 client, walks, notifications
├── agent/                  the behaviour of a device on a client
├── server/                 IEEE 2030.5 server (http.Handler) and its Admin API
├── security/               LFDI/SFDI, certificate verification, TLS configurations
├── schedule/               event scheduling: primacy, supersession, randomization
├── discovery/              DNS-SD browse and advertise
├── tlsccm/                 module: the CCM_8 cipher suite
├── interop/                module: tests against the ieee20305-interop images
├── e2e/                    end-to-end, chaos and load tests
├── examples/               runnable programs
├── internal/gen/           the code generator and the schema tooling
└── schemas/                the manifest of the pinned upstream schemas
```

## Client quickstart

A device: a client with its certificate, and an agent that registers,
checks the PIN, and applies the DER controls of its programs.

```go
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"os"
	"os/signal"

	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/schedule"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	device, err := tls.LoadX509KeyPair("device.crt", "device.key")
	if err != nil {
		log.Fatal(err)
	}
	device.Leaf, _ = x509.ParseCertificate(device.Certificate[0])
	pem, err := os.ReadFile("ca.crt")
	if err != nil {
		log.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)

	c, err := client.New(client.Config{
		Server:     "https://utility.example/dcap",
		HTTPClient: client.NewHTTPClient(device, roots),
	})
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(agent.Config{
		Client:    c,
		Identity:  client.IdentityOf(device.Leaf),
		PIN:       111115, // given to the device out of band
		VerifyPIN: true,
		OnDERControl: func(ctx context.Context, t schedule.Transition) error {
			log.Printf("control %s: %v", t.Event.MRID, t.To) // apply or release it
			return nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
```

Without the agent, `client.Client` reads and writes any resource:
`DeviceCapability`, `Register`, `Get`, `Iterate`, `Walk`, `Post`, `Put`,
`Subscribe`. For CSIP-AUS, set `Codec: csipaus.Codec{}` and read the site
limits with `csipaus.LimitsOf`.

## Server quickstart

A server: provision a device with its PIN, give it a DER program, and serve
over mutual TLS. `Admin` is the operator's API; devices see the standard
resources.

```go
srv, err := server.New(server.Config{
	OnResponse: func(ctx context.Context, href string, r any) { /* a device answered */ },
})
if err != nil {
	log.Fatal(err)
}
defer srv.Close()
a := srv.Admin()
_ = a.Root(ctx, "DERProgramList", "/derp", a.Document("DERProgramList"))

lfdi := security.CertificateLFDI(deviceCert)
_, err = a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true, PIN: 111115})
fsa, err := a.AssignFSA(ctx, lfdi, &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{1}})
err = a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String())
program, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{Primacy: 1}, "")
err = a.AddMember(ctx, fsa+"/derp", program)
_, err = a.Create(ctx, program+"/derc", control, "") // a *ieee20305.DERControl

hs := &http.Server{Addr: ":8443", Handler: srv, TLSConfig: security.ServerTLSConfig(serverCert, deviceCAs)}
log.Fatal(hs.ListenAndServeTLS("", ""))
```

The server keeps the EventStatus of the control as it starts and ends,
computes the ActiveDERControlList, and stores the Responses the device
posts. `go run ./examples/quickstart` runs both sides in one process.

## Production features

| Feature | How | Notes |
|---------|-----|-------|
| Cancellation | `context.Context` on every call of the client, agent and Admin | `Agent.Close` cancels what the agent has in flight |
| Retries | `ieee20305.RetryPolicy`: `client.Config.RetryPolicy`, `agent.Config.Backoff`, `server.Config.NotificationRetry` | `ExponentialBackoff` with full jitter honours `Retry-After`; the client does not repeat a POST unless `RetryPOST` is set; 4xx are never retried |
| Structured logging | `ieee20305.NewSlogLogger`, `NewStdLogger`, or any `ieee20305.Logger` | A `component` field per package; silent by default |
| Metrics hooks | `client.Metrics`, `server.Metrics`, `agent.Metrics`, and the optional `AttemptMetrics`, `NotificationMetrics`, `EventMetrics` | Synchronous, no dependencies; a Prometheus adapter is a few lines ([OBSERVABILITY.md](OBSERVABILITY.md)) |
| Subscriptions | `client.Subscribe`, `client.NewNotificationHandler`; `agent.Config.NotificationURI`; `server.Config.NotificationClient` | Notifications are coalesced, retried with backoff and cancelled after `NotificationCancelAfter`; `NotificationURIAllowed` guards the URIs devices choose |
| Bounded resources | `server.Config.MaxListItems`, `EventRetention`; `client.Config.MaxListItems`; capped agent queues | Lists that devices post to keep their newest items; ended events are pruned |
| Persistence | `server.Config.Store` | In memory by default; a `Store` keeps the resources elsewhere |
| CCM_8 | module `tlsccm`: `NewHTTPClient`, `NewListener` | Interoperates with GridAPPS-D and EPRI, which offer nothing else |

## Error semantics

| Situation | How it is reported |
|-----------|--------------------|
| The server answered 4xx or 5xx | `*ieee20305.StatusError` with the Error document, matching `ErrNotFound`, `ErrForbidden`, `ErrBadRequest`, `ErrConflict`, `ErrServer`, `ErrUnavailable` |
| No answer in time | `ieee20305.ErrTimeout` |
| Refused or reset connection, DNS failure | `ieee20305.ErrUnavailable` |
| Certificate rejected, by either end | `ieee20305.ErrCertificate` |
| A document that breaks the schema or the protocol | `ieee20305.ErrProtocol`, `*ieee20305.ValidationError` |
| A value or configuration the library refuses | `ieee20305.ErrInvalid`, `ieee20305.ErrConfiguration` |
| Used after `Close` | `ieee20305.ErrClosed` |
| Worth trying again | `ieee20305.Retryable(err)`, `ieee20305.RetryAfter(err)` |

Full guide: **[ERRORS.md](ERRORS.md)**.

## Examples

```sh
go run ./examples/quickstart      # a server and a device agent in one process
go run ./examples/server -pki /tmp/pki   # a server on 127.0.0.1:8443, with a test PKI
go run ./examples/client -pki /tmp/pki   # a device: register, check the PIN, walk
go run ./examples/csipaus         # CSIP-AUS envelopes, default limits, connection point
go run ./examples/observability   # logging, metrics and retries
```

See [examples/README.md](examples/README.md) for what each prints.

## Development

```sh
make check        # format, lint, vet, vulnerability scan, tests, coverage gate (90%), tlsccm
make test         # the library tests with the race detector
make e2e          # the end-to-end tests
make stress       # the tests shuffled, twice, with the race detector
make fuzz         # every fuzz target (FUZZTIME=30s each by default)
make test-tlsccm  # the CCM_8 module (Go 1.26.8)
make interop      # against the ieee20305-interop images (needs Docker)
make features     # docs/FEATURES.md against the e2e and interop tests
make build        # the examples into ./bin
```

Run `make` (or `make help`) for the full list of targets. See
[CONTRIBUTING.md](CONTRIBUTING.md).

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

## Interop tests

The `interop` module tests go-ieee20305 against four independent
implementations, packaged as container images by
[otfabric/ieee20305-interop](https://github.com/otfabric/ieee20305-interop):
[ANU Envoy](https://github.com/bsgip/envoy),
[GridAPPS-D](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) (server and
client), [DER Security py20305](https://github.com/DERSecurity/py20305) and
the [EPRI IEEE 2030.5 client](https://github.com/epri-dev/IEEE-2030.5-Client).

```sh
make interop    # needs Docker; about two minutes
```

| Direction | What is checked |
|-----------|-----------------|
| go-ieee20305 client → Envoy, GridAPPS-D | For each edition and fixture the server is seeded with (baseline, csip, csip-aus), over CCM_8 and GCM: time, registration and PIN, programs with their default controls and controls, the agent scheduling the active control, metering mirrors, Responses, log events, DER resources, tariffs with their intervals and prices, flow reservations, the CSIP-AUS connection point, and notifications from GridAPPS-D to our `NotificationHandler`; refusal of an unprovisioned device and of a device of another CA |
| GridAPPS-D, py20305, EPRI clients → go-ieee20305 server | Every operation each client declares, for every edition and fixture it handles: register, discover, poll-controls, poll-pricing, report, respond, log-event, put-der, flow-reservation and subscribe (our notifier delivering to the client's listener); the effect of each is checked on the server through `Admin` |

The images are the only thing shared with ieee20305-interop; nothing of it is
cloned or built here. Fixtures come from the images; the scenarios and
assertions live in this repository.

This version is qualified against
[ieee20305-interop v0.2.0](https://github.com/otfabric/ieee20305-interop/releases/tag/v0.2.0)
(container contract 0.3: Envoy v2.5.0, GridAPPS-D server v0.18.0 and client
v0.1.0, py20305 v0.16.0, EPRI client v0.2.11). The images are pinned by
digest in `interop/images.go` and the Interop workflow, which runs on every
push and pull request. To test another build, set
`IEEE20305_INTEROP_ENVOY_IMAGE`, `IEEE20305_INTEROP_GRIDAPPSD_IMAGE`,
`IEEE20305_INTEROP_PY20305_IMAGE` or `IEEE20305_INTEROP_EPRI_IMAGE`;
`IEEE20305_INTEROP_TRACE=1` logs every request and answer.

The suite asks each image what it declares (`print-capabilities`) and skips
what it lacks. Known upstream bugs (`interop/known_test.go`) skip while they
reproduce and fail once they no longer do:

| Stack | Bug | Effect |
|-------|-----|--------|
| EPRI client | Writes documents in the namespace of the draft sep.xsd 2.0.4 | Its `report` and `respond` get 400 from a server of IEEE 2030.5 |
| py20305 | Does not model the `RateComponentLink` CSIP-AUS 1.3 requires in a TimeTariffInterval | Finds no interval of a CSIP-AUS tariff |

## Limitations

- **CCM_8 needs the module `tlsccm`**, on a copy of Go 1.22's `crypto/tls`
  with CCM_8 (TLS 1.2 only); the core module, on `crypto/tls`, offers the
  ECDHE-ECDSA AES-GCM suites.
- **DNS-SD is not exercised across stacks**: the containers run on Docker
  networks without multicast, so interop reaches servers by URL.
  `discovery` is tested against an in-memory network and our own peer.
- **The server keeps its resources in memory** unless a `Store` is given;
  there is no database adapter in this module.
- **Prepayment, billing and files** are implemented and tested end to end
  here, but no other stack implements them, so they are not verified across
  implementations.
- **Verified against four third-party stacks**, not against certified test
  equipment (such as the SunSpec or UL test harnesses) or field devices.

Details in [INTEROPERABILITY.md](INTEROPERABILITY.md).

## License

[MIT](LICENSE). The generated code derives from the IEEE 2030.5 schemas,
which are not part of this repository; see [schemas/README.md](schemas/README.md).
