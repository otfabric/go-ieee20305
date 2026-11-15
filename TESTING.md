# Testing

How to run each test tier of go-ieee20305. What each feature is tested by is
in [docs/FEATURES.md](docs/FEATURES.md); the results against other
implementations are in [INTEROPERABILITY.md](INTEROPERABILITY.md).

## Tiers

| Tier | Command | Needs | In CI |
|---|---|---|---|
| Unit | `make test` | Go | every pull request |
| End-to-end | `make e2e` | Go | through `make test`, as `e2e` is one of the library test packages |
| Shuffled, twice | `make stress` | Go | every pull request (job Race and shuffle) |
| Coverage | `make coverage-gate` | Go | every pull request (job Coverage gate) |
| Fuzz | `make fuzz` | Go | 10 s per target in CI; 5 min per target nightly (Fuzz workflow) |
| Benchmarks | `make bench` (codecs, the scheduler, a list GET of the server) | Go | not run |
| Cross-compilation | `make cross` | Go | not run by this repository's workflows |
| Schema tests | `make download`, then `make test` | network for the download, `xmllint` | job Generated code |
| Generated code | `make generate-verify`, `make generate-check` | network for `generate-check` | job Generated code |
| Feature matrix | `make features` | Go | job Generated code |
| tlsccm | `make test-tlsccm` | Go 1.26.8, staticcheck, golangci-lint, govulncheck | job tlsccm |
| Interop | `make interop` | Docker, staticcheck, golangci-lint | every pull request (Interop workflow) |

`make check` is the local gate: format, `nodeps`, `features`,
`generate-verify`, lint, vet, govulncheck, tests, the coverage gate and tlsccm.

## End-to-end

`e2e/` runs the client and agent of this module against the server of this
module, over HTTPS with mutual TLS on loopback. `e2e/chaos_test.go` injects
latency, unavailability, aborts, connection resets, corruption and truncation
through `internal/chaos`, with fixed seeds. This shows that the two sides agree
with each other, not that they agree with anyone else.

## Hostile peers

`client/hostile_test.go` runs the client against a server that lies about
its lists (`all` of 0 or 2^32-1, pages that ignore `s`), fans out links
without end, loops redirects or leaves the server, sends bodies that never
end or stall, and documents that are no XML or nest deeply.
`server/hostile_test.go` sends the server query parameters at and beyond
their limits, oversized, malformed and nested bodies, subscription floods,
notification URIs that spell loopback and link-local addresses, receivers
that redirect, stall or never end their answer. `discovery/hostile_test.go`
floods the browser's cache and feeds it malformed packets, and
`internal/dnsmsg/boundary_test.go` checks the limits of names, labels,
pointers, sections and record data.

`server/hostile_test.go` also has one device post into the lists of the
resources it creates until the server refuses, and checks the number of
resources it owns. `agent/limits_test.go` has a server list more events
than an agent keeps, and `schedule/limits_test.go` bounds the events, the
comparisons a resolution makes and the transitions that wait for a
subscriber.

### Allocation budgets

A limit on a length is tested with the bytes the code allocates, not only
with the error it returns: `server/budget_internal_test.go` and
`client/budget_test.go` send bodies of up to 2^62 declared bytes, and
bodies without end, at a limit of 64 KiB, and lists that claim 2^32-1
items, and compare what `runtime.MemStats` counts with a budget derived
from the limit. `internal/dnsmsg/boundary_test.go` does the same for DNS
section counts.

## Lifecycle contract

The rows are those of the organisation's contract matrix for types that
own goroutines: the Agent, the Server (with its notifier and its event
scheduling), the Scheduler and the advertisement and browser of
`discovery`. Lifecycle tests wait for events (a channel, a request the
other side has received, a goroutine seen blocked at the wait a test means
to interrupt), not for time.

| Row | Covered by | Notes |
|---|---|---|
| L1 Close twice | `agent` `TestCloseTwice`; `server` `TestServerCloseTwice`; `schedule` `TestCloseTwice`; `discovery` `TestBrowseAdvertise` (second `Close` of an advertisement) | Each `Close` returns; the server's returns nil |
| L2 Concurrent Close | the same tests, from 8 goroutines | |
| L3 Close during a blocked read | `agent` `TestCloseEndsRun` (a request the server does not answer); `server` `TestHostileReceiverAtClose` (a receiver that does not answer a notification) | |
| L4 Close during a write | n/a | `net/http` owns the streams |
| L5 Close during a handshake | n/a in this module | `net/http` and `crypto/tls` own it; `tlsccm` tests its listener (`TestListenerClose`) |
| L6 Cancel a pending request | `client` `ErrCanceled` tests (`errors_test.go`); `agent` `TestRunEndsWithContext` | |
| L7 Late response | n/a | one request per HTTP exchange, no correlation of the library's own |
| L8 Response after reconnect | n/a | no session state |
| L9 Callback calls a public method | `agent` `TestHandlerCallsAgent`; `server` `TestHooksAndConfig` | |
| L10 Callback calls Close | `agent` `TestCloseFromHandler` (a handler, a `Metrics` method, two handlers at once); `server` `TestServerCloseFromMetrics` (an event, a notification, both at once) | `TestCloseWaitsForHandler` and `TestServerCloseWaitsForMetrics`: a `Close` on another goroutine still waits |
| L11 Slow callback | `agent` `TestSlowHandler`; `server` `TestHostileReceiverAtClose` and the notification retry tests | A handler that does not return holds back its function set only |
| L12 Queue at capacity | `schedule` `TestQueueBound`, `TestQueueCompaction`, `TestQueueKeepsUp`; `agent` `TestSlowHandler`, `TestRespond` (Responses kept to send again) | |
| L13 Concurrent sessions | `server` `TestAccess`, `TestConcurrentRegistration`; `e2e` with several devices | |
| L14 Failure during startup | `server` `TestBootstrapGetError`, the store fault sweeps of `faults_internal_test.go`; `discovery` `TestBrowseErrors`, `TestAdvertiseErrors` | Each package's `TestMain` then finds a goroutine a failed start left |
| L15 Shutdown(ctx) | n/a | no `Shutdown` method |
| L16 Close racing a retry | `agent` `TestCloseEndsRun` (`Run` waiting to start again, and waiting for its next poll) | |

Code of the application that does not return normally (a handler, a
`Metrics` method, a `Logger`) is covered by `TestHandlerDoesNotReturnNormally`,
`TestMetricsDoNotReturnNormally`, `TestPollCallbacksDoNotReturnNormally`,
`TestReadingsDoNotReturnNormally` and `TestLoggerDoesNotReturnNormally` in
`agent`, and `TestServerMetricsDoNotReturnNormally` and
`TestServerLoggerDoesNotReturnNormally` in `server`.

### Goroutine leaks

`internal/leakcheck` compares the goroutines of two stack dumps by their
identifiers: one that was started since the first dump and still exists is
reported with its stack. There is no tolerance in numbers. The `TestMain`
of `agent`, `client`, `csipaus/csipserver`, `discovery`, `e2e`, `schedule`,
`security` and `server` runs the package's tests and then fails the run
if a goroutine they started is left; `leakcheck.Check(t)` does the same
around one test.

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

A missing prerequisite skips, unless the switch of the test is set: then it
fails.

| Test | Skipped without | Fails instead with |
|---|---|---|
| The interop suite (build tag `interop`) | Docker | `IEEE20305_REQUIRE_INTEROP=1`, which `make interop` sets |
| `TestOfficialExamples`, the schema tests of `csipaus` | `schemas/upstream` (`make download`) | `IEEE20305_REQUIRE_SCHEMAS=1` |
| `TestEditionsValidate`, schema validation in `csipaus` | `xmllint` and `schemas/upstream` | `IEEE20305_REQUIRE_SCHEMAS=1` |
| Multicast tests of `discovery` | a host that delivers multicast; also skipped with `-short` | `IEEE20305_REQUIRE_MULTICAST=1` |
| Tests in `internal/gen/emit` that build a module | the `go` tool; also skipped with `-short` | |

CI's job Generated code installs `xmllint`, downloads the schemas and sets
`IEEE20305_REQUIRE_SCHEMAS`; its Coverage gate job sets
`IEEE20305_REQUIRE_MULTICAST`.

## Coverage

`make coverage-gate` requires `COVER_MIN` percent (90) of statements with the
race detector, over `COVER_PKGS` in the [Makefile](Makefile), and
`make coverage-pkg-check` requires `COVER_PKG_MIN` (90) of each of its packages. The shared `go-ci`
workflow is a separate gate, with its own `coverage-min` in
[ci.yml](.github/workflows/ci.yml). `COVER_PKGS` leaves out:

| Excluded | Why |
|---|---|
| `/examples` | example programs, built by `make build` and partly run in CI |
| `internal/gen` | the command of the code generator, run by `make generate`; its subpackages are counted |
| `e2e` | holds tests only |
| `tlsccm`, `interop` | nested modules, not part of `./...` |

## Fuzzing

`make fuzz` runs every `Fuzz` function of the library packages: it finds
them with `go test -list`, so a new target needs no registration.
`make fuzz-targets` lists them as `<package>:<target>`, and
`make fuzz FUZZ_TARGETS=<package>:<target>` runs only those given.
`make fuzz FUZZTIME=5m` sets the time per target.

A failing input lands in the target's `testdata/fuzz/<target>/` directory. The
Fuzz workflow keeps such inputs as the artifact `fuzz-failures`. To make one a
regression test, fix the bug and commit the input in that directory: `go test`
then runs it on every pass.

## When CI runs

| Event | Workflow | What runs |
|---|---|---|
| Pull request | `pr.yml` | `docs.yml` (docsync, otfabric-policy) always; `ci.yml`, `interop.yml` unless every changed file is documentation; then `OT Fabric Required Checks` |
| Push to a branch | none | nothing; dispatch `ci.yml`, `interop.yml` to test a branch without a pull request |
| Push to `main` | `docs.yml` | the light checks; the merged commit was tested as the pull request's merge commit |
| Schedule | `fuzz.yml`, `schema-updates.yml` | the scheduled runs, on `main` |
| Release | `release.yml` | the release workflow tests the release commit again |

Documentation means an added or modified `*.md` file outside `.github/`, `testdata/` and `vendor/`, other than `AGENTS.md` and `CLAUDE.md`. A deleted or renamed document, or any other file, runs everything. `OT Fabric Required Checks` fails unless every job that applies succeeded; its summary lists them.
