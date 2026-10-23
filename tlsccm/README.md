# tlsccm

The cipher suite IEEE 2030.5 requires, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, for
the clients and servers of [go-ieee20305](../README.md). Go's `crypto/tls` does
not implement it ([golang/go#27484](https://github.com/golang/go/issues/27484)),
so this module carries a copy of `crypto/tls` with CCM_8 added
(`internal/gotls`), and is a module of its own: the library itself has no
dependencies.

```go
// A client
hc := tlsccm.NewHTTPClient(deviceCert, serverCAs, tlsccm.Options{})
c, err := client.New(client.Config{Server: "https://utility.example:8443", HTTPClient: hc})

// A server
srv, err := server.New(server.Config{PeerCertificates: tlsccm.PeerCertificates})
inner, err := net.Listen("tcp", ":8443")
hs := &http.Server{Handler: srv}
tlsccm.ConfigureServer(hs)
l, err := tlsccm.NewListener(inner, serverCert, deviceCAs, tlsccm.Options{})
err = hs.Serve(l)
```

Connections are TLS 1.2 on P-256 with CCM_8 only; `Options.AllowGCM` also
offers TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 for peers without CCM_8, such as
clients on `crypto/tls`. Certificates are verified by the library's `security`
package, which accepts the HardwareModuleName names of IEEE 2030.5
certificates.

Handshakes are bounded by `Options.HandshakeTimeout` (10 s), and their
failures go to `Options.Logger` at Warn.

## The copy of crypto/tls

`internal/gotls` is `crypto/tls` of Go 1.22.0 with the CCM_8 cipher suite, and
`internal/ccm` the AES-CCM of Pion. Only tlsccm can use them, always with
TLS 1.2, P-256, no session resumption and verification by `security`.

The copy is kept byte for byte as upstream, except for what
[internal/gotls/UPSTREAM.md](internal/gotls/UPSTREAM.md) records, so that it
can be checked and fixed:

```sh
go run ./internal/upstream -diff    # the copy against crypto/tls of go1.22.0
go run ./internal/upstream -vulns   # crypto/tls vulnerabilities since, reviewed or not
```

Every `crypto/tls` vulnerability Go publishes after Go 1.22.0 is reviewed in
[internal/gotls/SECURITY.md](internal/gotls/SECURITY.md): whether the settings
of tlsccm reach it, and the fix ported when they do. A weekly workflow opens
an issue for one not yet reviewed.

The module needs Go 1.26.8 or later. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the licenses of the copies
(Go, Battelle Memorial Institute, Pion).
