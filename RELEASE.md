# go-ieee20305 Releases

Releases are cut with the `Release` workflow (semantic versioning, annotated
tag, GitHub release). The section of the version below, up to the `---` that
ends it, is the release's notes. Before v1.0.0 a minor release may change the
API.

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
