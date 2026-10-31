# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `go-ieee20305`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-ieee20305/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### What IEEE 2030.5 messages do

DER controls carry power limits and operating modes that equipment acts on. Metering data reveals how a site uses energy. Whoever can issue controls can curtail or dispatch generation and storage; whoever can read metering data learns a site's consumption and production patterns.

### Transport: mutual TLS

IEEE 2030.5 requires HTTPS with mutual TLS 1.2: the server and every device
present a certificate, and each verifies the other's chain.

- `security.ClientTLSConfig`, `ServerTLSConfig` and `NotificationTLSConfig`
  verify chains with `security.Verify` against the pool you give them, and
  never against the system's roots: a nil pool is an error
  (`ErrConfiguration`), not a fallback. Give a device the CA of its
  utility's servers, and a server the CA of its devices, nothing wider.
- Verification cannot be turned off through this library. The configurations
  set `InsecureSkipVerify` only to replace the verification of crypto/tls,
  which rejects the critical HardwareModuleName subjectAltName of IEEE
  2030.5 device certificates, with `security.Verify`, which accepts it and
  checks the chain, validity, key usage and names.
- The standard's cipher suite, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, is not in
  crypto/tls. The core module offers the ECDHE-ECDSA AES-GCM suites, which
  are as strong; the module `tlsccm` adds CCM_8 for peers that offer nothing
  else. It carries a copy of Go 1.22's `crypto/tls` with CCM_8, used only
  with TLS 1.2, P-256 and no session resumption; every `crypto/tls`
  vulnerability Go publishes since is reviewed, and its fix ported when it
  reaches tlsccm ([tlsccm/internal/gotls/SECURITY.md](tlsccm/internal/gotls/SECURITY.md)).
  Keep tlsccm updated.

### Device identity

Devices are identified by their certificate fingerprint: the LFDI is the
leftmost 160 bits of the SHA-256 fingerprint of the device certificate, the
SFDI its leftmost 36 bits with a check digit. There are no passwords or
bearer tokens. The server authorizes every request by the LFDI of the
client certificate: a device reads what is shared with it and its own
resources, and changes only its own; an aggregator acts only for the devices
it was given (`DeviceSpec.ManagedBy`).

### Registration and the PIN

A device should not trust a server only because its certificate verifies:
the Registration of its EndDevice carries a PIN that the device was given
out of band, and `client.CheckPIN` (or `agent.Config.VerifyPIN`) compares
them. Keep the PIN check on; without it, any server of the same CA can
control the device.

### Hardening a server

- **Provision devices** (`Admin.ProvisionDevice`) and leave
  `InBandRegistration` off unless unknown devices must register themselves;
  an in-band device is not registered until `Admin.Register` is called.
- **Request limits.** `Config.MaxRequestBytes` bounds a request body,
  `MaxLimit` a page of a list, and `MaxListItems` the lists devices post to
  (LogEvents, Responses, readings), so that a device cannot grow the server
  without bound. Ended events are pruned after `EventRetention`.
- **Notifications are outbound requests** to a URI the device chooses. The
  default `NotificationURIAllowed` refuses loopback, link-local, multicast
  and unspecified addresses; tighten it to the networks of your devices.
  The notifier presents the server's certificate and verifies the
  listener's (`server.NewNotificationClient`).
- **Set `http.Server` timeouts** (`ReadHeaderTimeout` at least), as with any
  Go HTTP server.

### A client

- `client.Client` follows links and redirects only within its server, so a
  document cannot send a device to another host, or make it send credentials
  in a URL. A link's host matches the configured one but for ASCII case;
  userinfo, Unicode case variants and other spellings (a trailing dot, the
  default port) are refused.
- `Config.MaxResponseBytes` bounds a response, and `MaxListItems` what
  `Iterate` reads from a list.
- A `client.NotificationHandler` should check the sender: set
  `NotificationConfig.Authorize` to accept only the server's certificate.

### Input handling

Every document from the network is decoded into the generated types and
checked against the schema's facets and cardinalities (`Validate`). The
codecs, the server's handler, HardwareModuleNames, the scheduler, DNS
messages and hrefs have fuzz targets (`make fuzz`), run nightly in CI.
Documents in another XML namespace are refused.
