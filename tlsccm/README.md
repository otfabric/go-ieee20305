# tlsccm

The cipher suite IEEE 2030.5 requires, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, for
the clients and servers of [go-ieee20305](../README.md). Go's `crypto/tls` does
not implement it ([golang/go#27484](https://github.com/golang/go/issues/27484)),
so this module wraps the fork of GridAPPS-D, and is a module of its own: the
library itself has no dependencies.

```go
// A client
hc := tlsccm.NewHTTPClient(deviceCert, serverCAs, tlsccm.Options{})
c, err := client.New(client.Config{Server: "https://utility.example:8443", HTTPClient: hc})

// A server
srv, err := server.New(server.Config{PeerCertificates: tlsccm.PeerCertificates})
inner, err := net.Listen("tcp", ":8443")
hs := &http.Server{Handler: srv}
tlsccm.ConfigureServer(hs)
err = hs.Serve(tlsccm.NewListener(inner, serverCert, deviceCAs, tlsccm.Options{}))
```

Connections are TLS 1.2 on P-256 with CCM_8 only; `Options.AllowGCM` also
offers TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 for peers without CCM_8, such as
clients on `crypto/tls`. Certificates are verified by the library's `security`
package, which accepts the HardwareModuleName names of IEEE 2030.5
certificates.

The module needs Go 1.26.8 or later. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
for the license of the fork.
