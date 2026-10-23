# Third-party notices

The tlsccm module carries copies of this software, in `internal/`. See
[internal/gotls/UPSTREAM.md](internal/gotls/UPSTREAM.md) for what was copied,
and what was changed.

## The Go standard library: `internal/gotls`

`internal/gotls` is the package `crypto/tls` of Go 1.22.0.

Copyright 2009 The Go Authors. Licensed under the BSD 3-Clause license in
[internal/gotls/LICENSE](internal/gotls/LICENSE).

## Battelle Memorial Institute: the CCM_8 registration and the stand-ins

The registration of TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8
(`internal/gotls/cipher_suites_ccm.go`, and its tests) and the stand-ins of
`internal/gotls/stubs`.

Copyright (c) 2026, Battelle Memorial Institute. All rights reserved.
Licensed under the license in
[internal/gotls/LICENSE-BATTELLE](internal/gotls/LICENSE-BATTELLE), with the
disclaimers of [internal/gotls/NOTICE-BATTELLE](internal/gotls/NOTICE-BATTELLE).
A binary distribution of a program built with tlsccm must reproduce both
files.

## Pion: `internal/ccm`

The AES-CCM mode (RFC 3610) of the Pion project.

Copyright (c) The Pion community. Licensed under the MIT license in
[internal/ccm/LICENSE](internal/ccm/LICENSE).
