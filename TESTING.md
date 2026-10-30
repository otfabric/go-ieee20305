# Testing

How to run each test tier of go-ieee20305. What each feature is tested by is
in [docs/FEATURES.md](docs/FEATURES.md); the results against other
implementations are in [INTEROPERABILITY.md](INTEROPERABILITY.md).

## Tiers

| Tier | Command | Needs | In CI |
|---|---|---|---|
| Unit | `make test` | Go | every push and pull request |
| End-to-end | `make e2e` | Go | through `make test`, as `e2e` is one of the library test packages |
| Shuffled, twice | `make stress` | Go | every push and pull request (job Race and shuffle) |
| Coverage | `make coverage-gate` | Go | every push and pull request (job Coverage gate) |
| Fuzz | `make fuzz` | Go | 10 s per target in CI; 5 min per target nightly (Fuzz workflow) |
| Benchmarks | `make bench` | Go | not run |
| Cross-compilation | `make cross` | Go | not run by this repository's workflows |
| Schema tests | `make download`, then `make test` | network for the download, `xmllint` | job Generated code |
| Generated code | `make generate-verify`, `make generate-check` | network for `generate-check` | job Generated code |
| Feature matrix | `make features` | Go | job Generated code |
| tlsccm | `make test-tlsccm` | Go 1.26.8, staticcheck, golangci-lint, govulncheck | job tlsccm |
| Interop | `make interop` | Docker, staticcheck, golangci-lint | every push and pull request (Interop workflow) |

`make check` is the local gate: format, `nodeps`, `features`,
`generate-verify`, lint, vet, govulncheck, tests, the coverage gate and tlsccm.

## End-to-end

`e2e/` runs the client and agent of this module against the server of this
module, over HTTPS with mutual TLS on loopback. `e2e/chaos_test.go` injects
latency, unavailability, aborts, connection resets, corruption and truncation
through `internal/chaos`, with fixed seeds. This shows that the two sides agree
with each other, not that they agree with anyone else.

## Interop

`interop/` is a module of its own, because it depends on `tlsccm`. It runs the
container images of
[ieee20305-interop](https://github.com/otfabric/ieee20305-interop), pinned by
digest in `interop/images.go`; `TestWorkflowPins` checks that the Interop
workflow pins the same images. `make interop INTEROP_TESTFLAGS=-v` prints every
test. Each image can be overridden with its environment variable, such as
`IEEE20305_INTEROP_ENVOY_IMAGE`. `IEEE20305_INTEROP_TRACE`, when set, turns on
the trace output of the server tests.

Known bugs of other implementations are listed in `interop/known_test.go`. Such
a test is skipped while the bug reproduces (`KNOWN_BUG`) and fails once it no
longer does. An operation or cipher suite an image does not declare is skipped
as `SKIP_UNSUPPORTED`.

## Prerequisites and skips

There is no `*_REQUIRE_*` switch yet, so a missing prerequisite skips rather
than fails:

| Test | Skipped without |
|---|---|
| The interop suite | Docker |
| `TestOfficialExamples`, the schema tests of `csipaus` | `schemas/upstream` (`make download`) |
| `TestEditionsValidate`, schema validation in `csipaus` | `xmllint` and `schemas/upstream` |
| Multicast tests of `discovery` | a host that delivers multicast; also skipped with `-short` |
| Tests in `internal/gen/emit` that build a module | the `go` tool; also skipped with `-short` |

CI's job Generated code installs `xmllint` and downloads the schemas, so the
schema tests run there.

## Coverage

`make coverage-gate` requires `COVER_MIN` percent (90) of statements with the
race detector, over `COVER_PKGS` in the [Makefile](Makefile). The shared `go-ci`
workflow is a separate gate, with its own `coverage-min` in
[ci.yml](.github/workflows/ci.yml). `COVER_PKGS` leaves out:

| Excluded | Why |
|---|---|
| `/examples` | example programs, built by `make build` and partly run in CI |
| `internal/gen` | the command of the code generator, run by `make generate`; its subpackages are counted |
| `e2e` | holds tests only |
| `tlsccm`, `interop` | nested modules, not part of `./...` |

## Fuzzing

`FUZZ_TARGETS` in the [Makefile](Makefile) lists the targets as
`<package>:<target>`. A new `Fuzz` function must be added there, as `make fuzz`
does not discover them. `make fuzz FUZZTIME=5m` sets the time per target.

A failing input lands in the target's `testdata/fuzz/<target>/` directory. The
Fuzz workflow keeps such inputs as the artifact `fuzz-failures`. To make one a
regression test, fix the bug and commit the input in that directory: `go test`
then runs it on every pass.
