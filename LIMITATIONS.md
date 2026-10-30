# Limitations

What go-ieee20305 does not do, and what has not been verified. Function sets
and their test evidence are in [docs/FEATURES.md](docs/FEATURES.md), decisions
and verification in [INTEROPERABILITY.md](INTEROPERABILITY.md).

## Scope not implemented

- The server keeps its resources in memory unless a `Store` is given; there is
  no database adapter in this module. It keeps no history beyond its `Store`:
  ended events are pruned after `EventRetention`, and capped lists drop their
  oldest items.

## Verification

- Tested against Envoy, GridAPPS-D (server and client), py20305 and EPRI, through
  [ieee20305-interop](https://github.com/otfabric/ieee20305-interop) v0.2.0.
  The matrix per feature is in [docs/FEATURES.md](docs/FEATURES.md).
- Not verified across implementations:
  - Prepayment, billing and files: no other stack implements them, so they are
    tested end to end between this module's own client and server only.
  - DNS-SD: the containers run on Docker networks without multicast, so interop
    reaches servers by URL. `discovery` is tested against an in-memory network
    and this module's own peer.
- Not tested against certified test equipment (such as the SunSpec or UL
  harnesses) or field devices.
- Certification: none. IEEE 2030.5 or CSIP certification has not been attempted.

## Known deviations

- The standard's cipher suite TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 is not in the
  core module, which offers the ECDHE-ECDSA AES-GCM suites on `crypto/tls`.
  CCM_8 needs the module `tlsccm`, on a copy of Go 1.22's `crypto/tls` (TLS 1.2
  only).
- DNS-SD uses the link-local mDNS groups (224.0.0.251, ff02::fb, port 5353).
  IEEE 2030.5 specifies xmDNS, whose site-local scope `discovery` does not join.
