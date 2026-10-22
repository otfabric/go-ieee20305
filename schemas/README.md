# Upstream schemas

The Go types of this module are generated from the official machine-readable
specifications. Those files are **not committed**: the IEEE material may not be
redistributed, and no redistribution licence is stated for the CSIP-AUS
schemas. They are downloaded on demand and verified against
[`manifest.json`](manifest.json).

## manifest.json

The manifest pins every upstream source:

| Source | Used for |
|---|---|
| `ieee-2030.5-2023` — IEEE Std 2030.5-2023 supplemental material (ZIP) | `sep.xsd` (schema 2.2) → package `ieee20305`; `sep_wadl.xml` → `resources_gen.go`; `Examples/` → round-trip tests |
| `ieee-2030.5-2018` — IEEE Std 2030.5-2018 supplemental material (ZIP) | `sep.xsd` → validating CSIP-AUS documents (CSIP-AUS v1.3 profiles 2018) |
| `csipaus-1.3-core`, `csipaus-1.3-ext` — CSIP-AUS v1.3 schemas | package `csipaus` |
| `epri-sep-2.0.4` — EPRI client `sep.xsd` at a pinned commit | reference for compatibility checks |

For each source it records the download URL, the SHA-256 of the download, and
the SHA-256 of every file used from it, with version and licence notes. Its
`targets` say which Go package is generated from which files, and its
`overrides` record, with a reason, each explicit correction of an upstream
defect or ambiguity (for example the unresolvable `ref="RateComponentLink"` in
`csipaus-core-v1.3.xsd`).

## Workflow

```sh
make download        # fetch into schemas/upstream/ and verify every checksum
make generate        # regenerate the *_gen.go files (runs download first)
make generate-verify # offline: generated headers match the manifest
make check-updates   # compare upstream with the manifest
```

`make download` refuses a file whose checksum differs from the manifest. The
generated files are committed and their headers record the checksum of every
input. **Never edit a generated file**: fix the generator in `internal/gen` or
its inputs here, and regenerate.

## Continuous integration

- The `Generated code` job of `ci.yml` downloads the sources, regenerates, and
  fails if the result differs from the committed code. It then round-trips the
  official IEEE examples and validates the encoded documents with `xmllint`
  against the official IEEE 2030.5 and CSIP-AUS schemas.
- `schema-updates.yml` runs weekly. It reports content that changed at a pinned
  URL, new IEEE 2030.5 releases on the IEEE downloads page, and new CSIP-AUS
  version pages, by opening or updating an issue.

## Adopting an upstream change

1. Download the new release and review what changed.
2. Update the URL, version and checksums in `manifest.json` (and its targets if
   file names changed).
3. Run `make generate`, fix the generator if it rejects a new construct, and
   review the diff of the generated code.
4. Run `make check` and commit the manifest and the regenerated code together.
