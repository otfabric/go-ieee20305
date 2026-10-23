# crypto/tls vulnerabilities since the base of the copy

This directory is `crypto/tls` of Go 1.22.0 with CCM_8 added (see
[UPSTREAM.md](UPSTREAM.md)). Go 1.22 is out of support, so a fix Go makes
to `crypto/tls` reaches this copy only when it is reviewed and, where it
applies, ported here.

`go run ./internal/upstream -vulns` (from the tlsccm module; weekly in CI)
lists every vulnerability of `crypto/tls` in Go's vulnerability database
([vuln.go.dev](https://vuln.go.dev)) published after Go 1.22.0 and not
reviewed below. Each review says whether the vulnerability reaches tlsccm,
and what was done.

## What reaches the copy

tlsccm is the only user of this package (it is internal), and configures
every connection the same way:

- **TLS 1.2 only** (`MinVersion` and `MaxVersion`): the TLS 1.3 code (key
  schedule, KeyUpdate, post-handshake messages of TLS 1.3, encryption
  levels) is never run.
- **No session resumption**: the server disables session tickets, and the
  client sets no session cache.
- **No QUIC, no Encrypted Client Hello** (the latter is not in Go 1.22).
- **P-256 only**, with the suites TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 and,
  with `AllowGCM`, TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256.
- Certificates are verified by `security.Verify`, not by the copy.

A vulnerability in code these settings do not reach is reviewed as **not
reachable**; it is still listed, so that the review is complete.

## Reviews

| ID | CVE | Published | Summary | Reaches tlsccm | Done |
|----|-----|-----------|---------|----------------|------|
| GO-2025-4008 | CVE-2025-58189 | 2025-10-29 | ALPN negotiation error contains attacker-controlled information | **Yes**: the TLS 1.2 server's ALPN error | **Patched**: `handshake_server.go` quotes the protocols (`%q`), as Go 1.24.8 does; recorded in UPSTREAM.md |
| GO-2026-4340 | CVE-2025-61730 | 2026-01-28 | Handshake messages may be processed at the incorrect encryption level | No: encryption levels are TLS 1.3 | Not reachable |
| GO-2026-4337 | CVE-2025-68121 | 2026-02-05 | Unexpected session resumption | No: no session tickets, no client session cache | Not reachable |
| GO-2026-4870 | CVE-2026-32283 | 2026-04-07 | Unauthenticated TLS 1.3 KeyUpdate record causes connection retention | No: TLS 1.3 | Not reachable |
| GO-2026-5856 | CVE-2026-42505 | 2026-07-07 | Encrypted Client Hello privacy leak | No: no ECH in Go 1.22 | Not reachable |
| GO-2026-6090 | CVE-2026-56862 | 2026-08-13 | Handshake messages accepted post-handshake (KeyUpdate) | No: KeyUpdate is TLS 1.3; TLS 1.2 refuses renegotiation | Not reachable |
| GO-2026-6607 | CVE-2026-97031 | 2026-10-08 | Malformed ECH outer extension references | No: no ECH in Go 1.22 | Not reachable |

When a new vulnerability is listed: read its fix (the `go.dev/cl` link of
its entry), decide whether these settings reach it, and add a row. If it
reaches tlsccm, port the fix to this directory, record the patch in
`../upstream/main.go` (`patches`) and in UPSTREAM.md, and release tlsccm.
