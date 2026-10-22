# Contributing to go-ieee20305

Thank you for your interest in contributing to `go-ieee20305`! This document covers the guidelines for contributing.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```sh
   git clone git@github.com:<your-username>/go-ieee20305.git
   cd go-ieee20305
   ```
3. Create a feature branch:
   ```sh
   git checkout -b feature/my-change
   ```

## Development Setup

### Prerequisites

- Go 1.23+ (see [go.mod](go.mod)); the code must build and pass on Go 1.23, so do not use newer language or standard-library features
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for the checks
- Docker, for `make interop` only

### Build & Test

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
make download     # the pinned upstream schemas, for make generate and the schema tests
make generate     # regenerate the *_gen.go files
```

Run `make` (or `make help`) for the full list of targets.

### Documentation

A change of the public API updates its godoc, [API.md](API.md), and the
README or the guide it touches ([ERRORS.md](ERRORS.md),
[OBSERVABILITY.md](OBSERVABILITY.md)). A new feature gets a row in
[docs/FEATURES.md](docs/FEATURES.md) that names its e2e test, and `make
features` checks it. The programs in [examples](examples) must build and
run (`make build`).

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Start every Go file with `// SPDX-License-Identifier: MIT`
- No dependencies outside the standard library: `go.mod` has no `require`, and `make nodeps` (part of `make check`) fails when it gets one
- Respect the package boundaries: the root package knows neither HTTP nor XML encoding, the XML wire format stays in `internal/xml`, `client` and `server` do not import each other

### Formatting

`make fmt` formats with the `gofmt` of the Go version in `go.mod`, which is the version CI uses, not necessarily the one you have installed. `make fmt-check` (part of `make check`) fails if either that `gofmt` or your local one would change a file.

### Tests

- Tests must pass with `-race` and must not depend on timing beyond generous upper bounds
- An error a caller may want to tell apart matches a sentinel with `errors.Is`: wrap one of package `ieee20305` (`fmt.Errorf("%w: ...", ieee20305.ErrInvalid)`), or give a type an `Is` method
- A parser of bytes from elsewhere (a network packet, an XML document) gets a fuzz target, listed in the `fuzz` target of the Makefile

## Submitting

1. Run `make check`
2. Commit with a message that says what changed and why
3. Open a pull request against `main` describing the change and how it was tested

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
