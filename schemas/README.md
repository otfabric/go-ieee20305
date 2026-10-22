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

## Verification

The generator verifies each XSD file against the SHA-256 checksums in
`manifest.yaml` before running. Update the checksums in the manifest after
obtaining the files:

```bash
shasum -a 256 schemas/sep.xsd
```
