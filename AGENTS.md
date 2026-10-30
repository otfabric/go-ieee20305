# AGENTS.md — go-ieee20305

## What this is

Pure Go IEEE 2030.5 (Smart Energy Profile 2.0) library, editions 2018 and 2023, with the CSIP and CSIP-AUS profiles: a client and device agent, an embeddable server, a resource model generated from the official schemas, certificates and discovery. No cgo, and no dependencies outside the standard library (`make nodeps`); CCM_8 lives in the separate module `tlsccm`. Profile: **protocol-library**. Nothing is certified.

## Organisation standards

This repository follows **otfabric/engineering policy 1.1**, profile **protocol-library** (`.otfabric/policy.yaml`, mode `enforce`). Where this file and the organisation standards disagree, this file wins here: say so in your report.

## Where the truth lives

| Question | Source |
|---|---|
| Wire behaviour | IEEE 2030.5 schemas pinned in `schemas/manifest.json`; decisions and known deviations in INTEROPERABILITY.md |
| What is implemented and tested against which stack | docs/FEATURES.md (`make features` checks it against the tests), INTEROPERABILITY.md |
| Scope not implemented, what is not verified | LIMITATIONS.md |
| Design | ARCHITECTURE.md |
| Errors | ERRORS.md |
| Release history | RELEASE.md (`## vX.Y.Z` … `---`) |

## Hard rules

1. Never lower `COVER_MIN` (90) or `coverage-min` in `.github/workflows/ci.yml`, add lint suppressions, or skip or delete tests to make CI pass.
2. `*_gen.go` files are generated: change `internal/gen` or the pinned sources, then `make generate`. Never edit them by hand.
3. Never invent protocol semantics: cite the specification or record the decision in INTEROPERABILITY.md.
4. Interop claims only from passing runs against the pinned ieee20305-interop release (`interop/images.go`).
5. No dependency in the core module: `make nodeps` fails. TLS verification cannot be turned off through this library (SECURITY.md).
6. `tlsccm` carries a copy of Go's `crypto/tls`: keep it reviewed against Go's vulnerabilities (`make tlsccm-upstream`).
7. No incidental breaking changes to exported API; this library stays on v0 or v1.

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| generated code inputs | `make generate generate-check` |
| concurrency or fault behaviour | `make stress` |
| a decoder | `make fuzz` |
| interop-relevant behaviour | `make interop` (Docker) |
| a feature's coverage | `make features` |

## Definition of Done

`make check` green; tests for new behaviour including error paths; a fuzz target for a new decoder of untrusted input; docs updated in the same change; RELEASE.md section for user-visible changes.

## Git and releases

Branches and PRs. Do not push to `main`, tag or run the Release workflow unless the maintainer asks in the session.
