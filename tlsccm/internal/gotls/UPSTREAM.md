# Provenance of internal/gotls

`internal/gotls` is `crypto/tls` of the Go standard library with the
cipher suite IEEE 2030.5 mandates, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8
(RFC 7251), which Go does not implement (golang/go#27484). `internal/ccm`
is the AES-CCM of the Pion project that the cipher suite uses.

The base below was established by content: each file of `crypto/tls`
that the copy carries is identical to that of the base, once the changes
below are undone.

## Base

- Go **1.22.0**, tag `go1.22.0`, commit `a10e42f219abb9c5bc4e7d86d9464700a42c7d57`.
- Later releases of Go are not a practical base: from Go 1.23 `crypto/tls`
  imports more internal packages of the standard library (ECH, ML-KEM, and
  from Go 1.24 the FIPS 140 module), each of which a copy would have to
  stand in for.

## Changes against crypto/tls

| Change | Files |
|--------|-------|
| The package is `gotls`, not `tls` | every file |
| Stand-ins for the internal packages a copy cannot import: `crypto/internal/boring`, `crypto/internal/boring/fipstls`, `internal/cpu` (no AES hardware detection: the AES-GCM preference order of a CPU without AES-NI; AES itself still uses the hardware), `internal/godebug` (no GODEBUG settings) | `stubs/*`, and their imports in `boring.go`, `cipher_suites.go`, `common.go`, `conn.go`, `handshake_client.go` |
| CCM_8: registered by an `init` that ranks it before GCM | `cipher_suites_ccm.go` (Battelle Memorial Institute) |
| GO-2025-4008: the ALPN error quotes the client's protocols | `handshake_server.go` (this repository; see SECURITY.md) |
| Not carried: `generate_cert.go` (a command), `fipsonly/` (boringcrypto only), the tests of crypto/tls | |

The copy is otherwise byte for byte the upstream files, and is kept so:
it is not reformatted or linted (see `.golangci.yml`), so that it can be
compared with upstream and fixes ported to it.

## Checking

From the tlsccm module:

```sh
go run ./internal/upstream -diff    # the copy against crypto/tls of go1.22.0
go run ./internal/upstream -vulns   # crypto/tls vulnerabilities since, reviewed or not
```

`-diff` normalizes the copy back to `crypto/tls` (the package name, the
imports of the stand-ins, the recorded patches) and requires the result to
be identical to the files of `go1.22.0`; a file that is neither upstream's
nor recorded as the copy's own is an error. `-vulns` is described in
[SECURITY.md](SECURITY.md). The weekly workflow "Upstream schemas" runs
both and opens an issue on a finding.

## Licenses

- Go: BSD-3-Clause, [LICENSE](LICENSE) (The Go Authors).
- The CCM_8 registration and the stand-ins: Battelle Memorial Institute,
  [LICENSE-BATTELLE](LICENSE-BATTELLE) and [NOTICE-BATTELLE](NOTICE-BATTELLE);
  see [../../THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md).
- `internal/ccm`: MIT, [../ccm/LICENSE](../ccm/LICENSE) (The Pion community).
