# IEEE 2030.5 Schema Files

The XSD files used for code generation are **not committed** to this repository.
The IEEE 2030.5 specification and its schema are proprietary to the IEEE
Standards Association and cannot be redistributed without explicit permission.

## Obtaining the XSD

1. Visit the [IEEE Standards Association](https://standards.ieee.org/) and
   obtain IEEE Std 2030.5-2023 (or 2030.5-2018 for the earlier version).
2. Extract `sep.xsd` from the specification package.
3. Place the XSD file(s) in this directory (`schemas/`).

## For CSIP-AUS extensions

The CSIP-AUS XSD is available from the Australian National University (ANU)
and ARENA's DER integration program.

## Running code generation

Once the schema files are in place, generate all Go types:

```bash
make generate          # runs all three generators (base, csipaus, wadl)
# or equivalently:
go generate ./...
```

The generated `*_gen.go` files carry a `// Code generated ... DO NOT EDIT.`
header with the source schema's SHA-256 checksum. **Do not edit them by hand.**
If the output is wrong, fix the generator in `internal/gen/` instead.

Generated files are committed to the repository so that downstream consumers
(and CI) do not need the proprietary XSD to build.

## Verification

The `make generate-verify` target checks that every committed `*_gen.go`
header checksum matches `manifest.yaml` — it does not need the XSD files,
only the manifest. CI runs this on every push.

After obtaining or updating the schema files, record their checksums:

```bash
shasum -a 256 schemas/sep.xsd schemas/csipaus.xsd schemas/sep.wadl
```

Then update the corresponding `sha256:` fields in `manifest.yaml`.
