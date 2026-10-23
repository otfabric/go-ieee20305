# go-ieee20305 Releases

Releases are cut with the `Release` workflow (semantic versioning, annotated
tag, GitHub release). The section of the version below, up to the `---` that
ends it, is the release's notes. Before v1.0.0 a minor release may change the
API.

## v0.5.2

**Date:** 2026-10-23  
**Previous release:** v0.5.1

### Summary

Five fixes found by raising the test coverage of agent, server, security
and internal to 95-100% (the module 95.7%), and tlsccm v0.3.0, which
depends on no fork: its CCM_8 is a copy of Go's `crypto/tls` in the module,
checked against Go's security releases.

### Fixed

- **agent** — a DERProgram without a DERControlList ranked as primacy 0, so
  its DefaultDERControl won over that of the program of highest precedence.
- **server** — bootstrap ignored store errors other than ErrNoNode, and
  started without `/tm` when the store failed.
- **server** — `Admin.NewList` with a link field pointing to a scalar
  panicked instead of returning `ErrInvalid`.
- **server** — a response the server could not encode answered 400; it is
  the server's failure, 500.
- **generator** — a type derived from `xs:hexBinary` with enumerations
  generated code that did not compile (no schema of ours has one).

### tlsccm v0.3.0

Released with this version as `tlsccm/v0.3.0`:

- `internal/gotls` (Go 1.22.0's `crypto/tls` with the CCM_8 cipher suite)
  and `internal/ccm` (Pion's AES-CCM) are part of the module, which requires
  only `golang.org/x/crypto` and `golang.org/x/sys` now. The listener that
  completes handshakes is tlsccm's own, and logs through `Options.Logger`.
- The ALPN error quotes the client's protocols (GO-2025-4008, backported).
  Every other `crypto/tls` vulnerability published since Go 1.22.0 is
  reviewed in `tlsccm/internal/gotls/SECURITY.md`: none reaches tlsccm's
  TLS 1.2 connections without session resumption.
- `go run ./internal/upstream` (or `make tlsccm-upstream`) checks the copy
  against `crypto/tls` of Go 1.22.0, and lists `crypto/tls` vulnerabilities
  not yet reviewed; a weekly workflow opens an issue for either.
- **Upgrade notes:** `ClientConfig` and `ServerConfig` are no longer
  exported (nothing used them); a negative `HandshakeTimeout` is
  `ErrConfiguration`. A binary distribution must reproduce the notices of
  `tlsccm/THIRD_PARTY_NOTICES.md`.

### Tested

Coverage: agent 99.3%, server 99.6%, security 97.3%, internal 95.9-100%;
the module 95.7%. TLS handshake tests run over loopback TCP (they could hang
over `net.Pipe` on Go 1.23). The interop suite passes with tlsccm's own
CCM_8 against the GridAPPS-D and EPRI images, which offer nothing else.

---

## v0.5.1

**Date:** 2026-10-23  
**Previous release:** v0.5.0

### Summary

A fix of the server, found by ieee20305ctl: a device that read a
ResponseList it had answered got 500.

### Fixed

- **Lists of derived items** — the generated `SetListItems` of a list takes
  the types derived from its item type, by the base they embed: a
  ResponseList holds the DERControlResponses, DrResponses, PriceResponses
  and TextResponses devices post (as Responses), and a DERCurveList its
  DERCurveControlTypes. The server answered every GET of such a list with
  500. Fixed in the generator; `e2e` reads a ResponseList after a Response.

---

## v0.5.0

**Date:** 2026-10-23  
**Previous release:** v0.4.0

### Summary

The Go API of the client, server and agent in the form of the other
otfabric libraries: one taxonomy of errors, pluggable logging, retry
policies and metrics hooks. A review of the code fixed races, unbounded
growth and unsafe defaults. The agent subscribes. The interop suite runs
every operation of ieee20305-interop v0.2.0 (contract 0.3) in both
directions, in a workflow of its own, and
[docs/FEATURES.md](docs/FEATURES.md) states, feature by feature, what is
tested and how far each other implementation goes.

**Upgrade notes**

Before v1.0.0 a minor release may change the API; this one does:

- `Logger` in `server.Config` and `agent.Config` is an `ieee20305.Logger`,
  not a `*slog.Logger`: pass `ieee20305.NewSlogLogger(handler)`.
- `agent.Config.Backoff` (an `ieee20305.ExponentialBackoff`) replaces
  `RetryDelay` and `MaxRetryDelay`. The agent now stops on errors that
  waiting does not fix (4xx, certificates, configuration), and drops a
  Response a server refuses with a 4xx.
- `server.Config.NotificationRetry` (an `ieee20305.RetryPolicy`) and
  `NotificationCancelAfter` (24 h) replace `NotificationAttempts` and
  `NotificationRetryDelay`. A 4xx cancels a subscription at once.
- `server.Config.NotificationURIAllowed` guards the notificationURIs devices
  choose. Its default, `DefaultNotificationURIAllowed`, refuses loopback,
  link-local, multicast and unspecified addresses: set it to reach
  listeners on such addresses (in tests, say).
- `client.NewNotificationHandler(handle, client.NotificationConfig{...})`
  builds a `NotificationHandler`; its fields are no longer set directly.
- `ieee20305.ErrSchedule` is gone (the scheduler returns `ErrInvalid`).
  `discovery.ErrNoDcap` matches `ErrProtocol`; `server.ErrNoNode` matches
  `ErrNotFound`.
- `security.ClientTLSConfig`, `ServerTLSConfig` and `Verify` fail with
  `ErrConfiguration` on a nil pool of roots, instead of falling back to the
  system's roots.
- The server answers a first PUT of a resource it linked (a DERSettings,
  say) with 204, as any other PUT, not 201.
- `server.Admin.Create` refuses, with `ErrInvalid`, an item its list cannot
  hold.
- Lists that clients post to are capped by `server.Config.MaxListItems`
  (1000; the oldest go first), and events in a final state are pruned after
  `server.Config.EventRetention` (24 h). `client.Iterate` stops after
  `client.Config.MaxListItems` items (100000).
- The tlsccm module changes too (`NewListener` returns an error;
  `Options.Logger` replaces `ErrorLog`), in tlsccm/v0.2.0, released apart.

### Added

- **Errors** ([ERRORS.md](ERRORS.md)) — `ErrServer`, `ErrConflict`,
  `ErrUnavailable` and `ErrCanceled`; `StatusError.RetryAfter`;
  `ieee20305.Retryable` and `ieee20305.RetryAfter`. Transport failures of
  the client are classified (timeouts, refused and reset connections,
  certificates), and the server answers the sentinels of its hooks with
  their statuses. `agent.ErrNotStarted`; `ErrClosed` after `Close`
  everywhere.
- **Logging** — `ieee20305.Logger` (`Debugf`, `Infof`, `Warnf`, `Errorf`),
  `FieldLogger`, `NopLogger`, `NewSlogLogger` and `NewStdLogger`, in the
  configuration of the server, client, agent, discovery and tlsccm, with a
  `component` field. Silent by default.
- **Retries** — `ieee20305.RetryPolicy`, `NoRetry` and
  `ExponentialBackoff` (full jitter, `Retry-After`); `client.Config.RetryPolicy`
  and `RetryPOST`.
- **Metrics** ([OBSERVABILITY.md](OBSERVABILITY.md)) — `client.Metrics`,
  `server.Metrics` and `agent.Metrics`, with the optional
  `client.AttemptMetrics`, `server.NotificationMetrics` and
  `server.EventMetrics`, and a `NopMetrics` to embed in each.
- **Agent subscriptions** — `agent.Config.NotificationURI` and
  `SubscribedPollRate`, and `Agent.NotificationHandler`: the agent
  subscribes to its FunctionSetAssignments, programs and controls, and polls
  less while subscribed.
- **Notifications** — `server.NewNotificationClient` and
  `security.NotificationTLSConfig`, which verify listeners served with
  device certificates.
- `server.Admin.Now`; `ieee20305.MediaType`.
- **Documentation** — [API.md](API.md), [ARCHITECTURE.md](ARCHITECTURE.md),
  [INTEROPERABILITY.md](INTEROPERABILITY.md) and
  [docs/FEATURES.md](docs/FEATURES.md); a README with client and server
  quickstarts, a comparison with Envoy, GridAPPS-D, py20305 and EPRI, the
  interop qualification and the limitations; a fuller SECURITY.md; and
  runnable [examples](examples/README.md) (quickstart, server, client,
  csipaus, observability) with `Example` functions, which CI runs.

### Fixed

- The locks of Responses and mirrors were shared by every Server; the
  notifier panicked when it reloaded subscriptions without a client; a PUT
  of an event lost the EventStatus the scheduler had just written.
- `server.New` leaked its goroutines on an error; `Agent.Close` could wait on
  posts without end; `Agent.State` shared its maps.
- A TimeTariffInterval has its ConsumptionTariffIntervalList.
- `client.Walk` fetched its first page twice.
- The package documentation of `security` claimed CCM_8, which is in
  `tlsccm`, and described the SFDI's check digit wrongly.

### Tested

- `e2e` for every function set in both editions, discovery to client,
  SelfDevice, aggregators, curves, prepayment, the agent with and without
  subscriptions, and the logging, metrics and retries under injected faults.
- `interop`, now against ieee20305-interop v0.2.0, and in the Interop
  workflow: every fixture (baseline, csip, csip-aus) and every operation of
  each image, both ways: register, discover, poll-controls, poll-pricing,
  report, respond, log-event, put-der, flow-reservation and subscribe, with
  our notifier delivering to py20305 and GridAPPS-D notifying our
  NotificationHandler. Known upstream bugs skip while they reproduce.
- `make features` checks [docs/FEATURES.md](docs/FEATURES.md) against the
  tests. Statement coverage 92.8%.

---

## v0.4.0

**Date:** 2026-10-22  
**Previous release:** v0.3.0

### Summary

A full IEEE 2030.5 client and server, for every resource of the WADL and
both editions (2018 and 2023): a device agent for every function set, a
server with provisioning, registration, event lifecycle, subscriptions and
metering, CSIP-AUS helpers, event scheduling, certificate verification for
HardwareModuleName certificates and DNS-SD discovery. Tested end to end,
under injected faults, with 500 devices at once, and against the Envoy,
GridAPPS-D, py20305 and EPRI implementations.

**Upgrade notes**

- `server`, `schedule` and `discovery` were placeholders; they are new APIs.
- `client.Codec` is now an alias of the new `ieee20305.Codec`, and
  `ieee20305.StandardCodec` the codec of the root package.
- `security.ClientTLSConfig` and `ServerTLSConfig` verify chains with
  `security.Verify`, which accepts the critical HardwareModuleName
  subjectAltName of IEEE 2030.5 certificates.
- The cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 is in the new module
  `github.com/otfabric/go-ieee20305/tlsccm` (Go 1.26.8), released apart; the
  library keeps no dependencies.

### Added

- **Editions** — `ieee20305.Edition`, `MarshalEdition` and `InEdition`: the
  types follow IEEE 2030.5-2023, and documents for 2018 peers are written
  without what 2018 does not define (generated `Strip2018`).
- **Generated helpers** — `Clone`, `Links` (every link of a document with its
  WADL resource), list items and counts, `Derives` and `TypeBase`, and tests of
  every generated type.
- **`client`** — `Head`, `FetchList`, `Iterate`, `Walk` (the resource tree,
  breadth first, same server only), `Time` (offset of the server's clock),
  `Subscribe`/`Unsubscribe`, `NotificationHandler` (deduplicates
  repeated notifications), `Codec`.
- **`agent`** — a device on a client: registration and PIN check, polling at
  the server's pollRate with backoff; the events of DER, DRLC, messaging,
  pricing, prepayment and flow reservation, scheduled with randomization,
  primacy and the conflicts of each function set, with the Responses they ask
  for (sent again after a failure); the DefaultDERControl, metering mirrors,
  files, flow reservation requests, LogEvents and device status.
- **`server`** — an `http.Handler` for every resource of the WADL, with the
  conformance of each method, paging and the ordering rules of lists, and
  documents in the edition of each client. `Admin` provisions devices (role:
  device, aggregator, operator; edition; PIN) and seeds resources, which come
  with their parts (an EndDevice with its lists, a DERProgram with its
  controls and default). In-band registration; aggregators act for the
  devices they manage. The EventStatus of events follows a scheduler, Active
  lists and the CurrentDERProgram are computed, Responses are stored once,
  subscriptions are notified of changes (coalesced, retried, cancelled when
  undeliverable), MirrorUsagePoints mirror to UsagePoints with their
  readings, and FlowReservationRequests are answered by a `FlowPolicy`.
  Every GET response carries its href and the `all` of its ListLinks.
- **`schedule`** — the scheduler of events: primacy, supersession within
  conflicting events, randomization, cancellation, timelines and the
  ResponseRequired rules.
- **`security`** — `Verify`, `VerifyPeerCertificate`, `HardwareModuleName` and
  test certificates with HardwareModuleNames.
- **`discovery`** — DNS-SD browsing and advertising of `_smartenergy._tcp`.
- **`csipaus`** — `Limits` (site limits); `csipaus/csipserver` (the CSIP-AUS
  profile of a server, with the ConnectionPoint of each EndDevice, envelopes
  and default limits) and `csipaus/csipclient` (the ConnectionPoint of a
  device).

### Tested

- `e2e`: agents against the server over mutual TLS for every function set
  and both editions; chaos (lost, replayed and truncated requests, 503s,
  aborted responses, connection resets during the TLS handshake, clock skew,
  corrupted responses, malformed requests); 500 agents at once; a storm of
  200 subscriptions; no goroutine leaks.
- `interop` (a module of its own): the client against the Envoy (2018, CCM_8
  and GCM) and GridAPPS-D (2018, 2023, CCM_8) servers; the GridAPPS-D,
  py20305 and EPRI clients against the server, for every operation they
  declare.
- Fuzz targets for the codecs, the server handler, HardwareModuleNames,
  scheduling, DNS messages and hrefs, nightly; statement coverage at least
  90%.

---

## v0.3.0

**Date:** 2026-10-22  
**Previous release:** v0.2.0

### Summary

Device registration: a device finds or creates its EndDevice on a server
and checks the PIN of its Registration.

**Upgrade notes:** none.

### Added

- **`client.Register`** — makes sure the server's EndDeviceList has an
  EndDevice for the device: finds it by the SFDI of the device certificate,
  or posts one with its SFDI and LFDI and reads back what the server created.
- **`client.FindEndDevice`** — selects the device with the `sFDI` query
  parameter, and pages through the list and matches itself in case the server
  ignores it. An EndDevice with the device's SFDI but another LFDI is not the
  device.
- **`client.Registration`** and **`client.CheckPIN`** — the Registration of an
  EndDevice (`ErrNotRegistered` without a RegistrationLink) and the check of
  its PIN against the one given out of band (`ErrPINMismatch`).
- **`client.IdentityOf`** — the LFDI and SFDI of a device certificate.
- **`client.ListOptions.SFDI`** — the `sFDI` query parameter.

---

## v0.2.0

**Date:** 2026-10-22  
**Previous release:** v0.1.0

### Summary

A first IEEE 2030.5 client: it reads and writes the resources of a server
over HTTPS with mutual TLS.

**Upgrade notes**

- `StatusError` has a new field, `Document`. Code that builds it with unkeyed
  fields has to be adjusted.

### Added

- **`client`** — `New(Config)` for one server; `DeviceCapability`, `Get`,
  `GetList` (the `s` and `l` query parameters), `Fetch`, `Post` (returning the
  `Location` of the created resource), `Put` and `Delete`, with the media type
  `application/sep+xml`. Hrefs are resolved against the server, and links or
  redirects to another server are refused, so the device certificate is only
  ever presented to that server. A response body is limited in size (1 MiB by
  default), EXI and other media types are refused, and failures match
  `ErrProtocol`, `ErrTimeout` or the status sentinels. `NewHTTPClient` builds
  the HTTP client with the device certificate.
- **`security.ClientTLSConfig` and `security.ServerTLSConfig`** — TLS 1.2 or
  later with mutual TLS. The cipher suite IEEE 2030.5 requires,
  TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, is not implemented by `crypto/tls`.
- **`csipaus.Codec`** — the CSIP-AUS codec for a client.
- **`StatusError.Document`** — the Error document of a failed response, with
  its reason code in the error text.

---

## v0.1.0

**Date:** 2026-10-22  
**Previous release:** none

### Summary

The IEEE 2030.5 resource model, generated from the official specifications:
every type of the IEEE 2030.5-2023 schema, the CSIP-AUS v1.3 profile, and the
resource table of the IEEE 2030.5 WADL, with XML encoding, schema validation
and certificate identities. The protocol packages (client, server, schedule,
discovery) are placeholders.

**Upgrade notes:** first release.

### Added

- **Resource model** (package `ieee20305`) — generated from the IEEE
  2030.5-2023 `sep.xsd` (schema 2.2): a Go type for each of its 364 complex
  and 29 simple types. Structs embed their base type and carry XML tags that
  keep namespaces, element order, optionality and cardinality; scalar types
  such as `UInt32`, `String32` and the hexadecimal `HexBinary160` are named
  types. Elements the schema admits without declaring them (`xs:any`) are kept
  as `ExtensionElement`s, and `Notification.Resource` carries any resource type
  through `xsi:type` (`AnyResource`).
- **Validation and defaults** — `Validate` on every type checks the facets and
  cardinalities of the schema and reports a `*ValidationError` with the path of
  the field; `Get` methods return the schema default of an absent value, such
  as `DeviceCapability.GetPollRate`.
- **Documents** — `Marshal`, `Unmarshal` and `Decode` for the 129 document
  types, with the namespace declared once and `schemaVer` written.
- **WADL** — `Resources` and `LookupResource`: the 135 resources of the IEEE
  2030.5 WADL with sample paths, methods, conformance levels, representations,
  query parameters and status codes.
- **CSIP-AUS v1.3** (package `csipaus`) — generated from the official CSIP-AUS
  schemas, which redefine IEEE 2030.5 types: the redefined types, the types
  that contain them (such as `DERControl` and `DERControlList`), and the types
  of the CSIP-AUS namespace; everything else is the base package's. Its codec
  does not write `schemaVer`, since CSIP-AUS v1.3 profiles IEEE 2030.5-2018.
- **Certificates** (package `security`) — LFDI and SFDI derivation from a
  device certificate, and ECDSA P-256 test PKI generation (CA, server with DNS
  and IP names, devices).
- **Code generation** — the generator in `internal/gen` and the upstream
  sources it reads, pinned by URL and SHA-256 in `schemas/manifest.json` and
  downloaded by `make download` (they are not redistributed). `make generate`
  regenerates; explicit, documented overrides correct two upstream defects.
- **CI** — regenerates from upstream on every push and fails on drift, then
  round-trips the 80 official IEEE examples and validates the encoded documents
  with `xmllint` against the official IEEE 2030.5 and CSIP-AUS schemas. A weekly
  workflow opens an issue when upstream content changes or a release appears.

### Verification

- All 80 examples of the IEEE 2030.5-2023 supplemental material decode,
  re-encode to equal values and, where the example itself is valid, validate
  against `sep.xsd`.
- CSIP-AUS documents (DERControl with operating envelope limits, EndDevice with
  connection point, ConnectionPoint, DERSettings, DERCapability) validate
  against the official CSIP-AUS v1.3 schemas.
- The SFDI matches the worked example of IEEE 2030.5 (`167261211391`).

---
