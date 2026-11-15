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
  never against the system's roots. A nil pool is not a fallback: the
  configurations take it, and every handshake then fails with an error
  that matches `ErrConfiguration` (and `ErrCertificate`). Only
  `tlsccm.NewListener` refuses a nil pool at once, with `ErrConfiguration`.
  Give a device the CA of its utility's servers, and a server the CA of
  its devices, nothing wider.
- Verification cannot be turned off through this library. The configurations
  set `InsecureSkipVerify` only to replace the verification of crypto/tls,
  which rejects the critical HardwareModuleName subjectAltName of IEEE
  2030.5 device certificates, with `security.Verify`, which accepts it and
  checks the chain, validity, key usage and names. The check runs in
  `VerifyConnection`, which crypto/tls calls on resumed sessions too.
- The standard's cipher suite, TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, is not in
  crypto/tls. The core module offers the ECDHE-ECDSA AES-GCM suites, which
  are as strong; the module `tlsccm` adds CCM_8 for peers that offer nothing
  else. It carries a copy of Go 1.22's `crypto/tls` with CCM_8, used only
  with TLS 1.2, P-256 and no session resumption; every `crypto/tls`
  vulnerability Go publishes since is reviewed, and its fix ported when it
  reaches tlsccm ([tlsccm/internal/gotls/SECURITY.md](tlsccm/internal/gotls/SECURITY.md)).
  As in Go 1.25, tlsccm neither offers nor accepts SHA-1 handshake
  signatures (RFC 9155), and its client refuses a server that picks a
  curve it did not offer. Keep tlsccm updated.

### Device identity

Devices are identified by their certificate fingerprint: the LFDI is the
leftmost 160 bits of the SHA-256 fingerprint of the device certificate, the
SFDI its leftmost 36 bits with a check digit. There are no passwords or
bearer tokens. The server authorizes every request by the LFDI of the
client certificate: a device reads what is shared with it and its own
resources, and changes only its own; an aggregator acts only for the devices
it was given (`DeviceSpec.ManagedBy`) and those it registers itself. By
default an aggregator may register a device the server was not given (as
CSIP-AUS aggregators do for their sites), never one the operator gave the
server without assigning it to that aggregator; `Config.AssignedDevicesOnly`
lets aggregators register only the devices assigned to them.

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
- **Assign devices to aggregators** (`DeviceSpec.ManagedBy`) and set
  `Config.AssignedDevicesOnly` unless aggregators must register devices
  the operator did not give the server.
- **Request limits.** `Config.MaxRequestBytes` bounds a request body,
  `MaxLimit` a page of a list, `MaxListItems` the lists devices post to
  (LogEvents, Responses, MirrorUsagePoints, MeterReadings, ReadingSets,
  Readings, FlowReservationResponses), and `MaxDeviceResources` all that
  one device owns, so that a device cannot grow the server without bound.
  In a list of every client the cap of `MaxListItems` counts each
  device's own items, so the posts of one device take nothing of
  another's. Deleting a MirrorUsagePoint deletes its UsagePoint. Ended
  events are pruned from the scheduler after `EventRetention`; their nodes
  stay in the store. The limits and their defaults are in the table under
  [Limits](#limits).
- **Notifications are outbound requests** to a URI the device chooses. The
  default `NotificationURIAllowed` refuses loopback, link-local, multicast
  and unspecified addresses (also with an IPv6 zone, and the IPv4 address
  an IPv6 literal embeds: IPv4-mapped, IPv4-compatible, IPv4-translated,
  NAT64 and 6to4), the hosts-file names of loopback (`localhost`,
  `ip6-localhost`, `ip6-loopback`, `localhost.localdomain`, `localhost6`),
  names with an empty label, and hosts whose last label is a number
  (`127.1`, `0x7f000001`), which some system resolvers read as IPv4
  addresses; tighten it to the networks of your devices.
  It is checked when a subscription is posted or put and again before each
  notification, so subscriptions a persistent store kept from an earlier
  configuration are held to the current one. Host names are not resolved:
  a name that resolves to an internal address passes the default. The
  notifier follows no redirect (a 3xx fails the notification), presents
  the server's certificate and verifies the listener's
  (`server.NewNotificationClient`, whose `Timeout` is one minute). A
  listener dialed by IP address is identified by its chain alone: its
  name is checked only when the notificationURI names it by a DNS name.
  The notifier bounds each attempt to one minute, whatever the
  `NotificationClient`, and stops the notifications of a subscription
  that is deleted.
- **Set `http.Server` timeouts** (`ReadHeaderTimeout` at least), as with any
  Go HTTP server.

### A client

- `client.Client` follows links and redirects only within its server, so a
  document cannot send a device to another host, or make it send credentials
  in a URL. A link's host matches the configured one but for ASCII case;
  userinfo, Unicode case variants and other spellings (a trailing dot, the
  default port) are refused. A `%` is read as an IPv6 zone only inside a
  bracketed IPv6 literal.
- `Config.MaxResponseBytes` bounds a response, and `MaxListItems` what
  `Iterate` reads from a list. `client.NewHTTPClient` bounds response
  headers to 64 KiB. A `Retry-After` is honoured up to 24 hours, and the
  agent takes a server's pollRate up to 24 hours.
- `agent.Config.MaxEvents` bounds the events an agent keeps of a function
  set, and with them the transitions that wait for a handler.
- A `client.NotificationHandler` should check the sender: set
  `NotificationConfig.Authorize` (for the agent,
  `agent.Config.NotificationAuthorize`) to accept only the server's
  certificate. The agent's handler ignores notifications of subscriptions
  that are not the agent's.

### Input handling

Every document from the network is decoded into the generated types and
checked against the schema's facets and cardinalities (`Validate`): the
server answers such a request 400, the client fails with `ErrProtocol`
(wrapping the `*ValidationError`), and a `NotificationHandler` answers a
Notification that violates them 400. The
codecs, the server's handler, HardwareModuleNames, the scheduler, DNS
messages and hrefs have fuzz targets (`make fuzz`), run nightly in CI.
Documents in another XML namespace are refused.

### Limits

What bounds the input of a peer, and the work and memory it can cause. A
limit of zero in a configuration means the default.

| Limit | Default | What happens beyond it |
|---|---|---|
| `server.Config.MaxRequestBytes`: the body of a request | 1 MiB | 413 |
| `server.Config.MaxLimit`: the items of a list page (`l`) | 255 | the page has `MaxLimit` items |
| `s`, `l` of a list request | 0 to 2^32-1 | 400 outside; a start beyond the list gives an empty page |
| `server.Config.MaxListItems`: a list clients post to (in a list of every client, the items of each device) | 1000 | the oldest items are deleted; none when negative |
| `server.Config.MaxDeviceResources`: the resources one device owns | 100 × `MaxListItems` (100000) | 403, counted in `Server.Stats`; none when negative |
| `server.Config.NotificationWorkers`: notifications sent at once | 16 | the others wait |
| `server.Config.NotificationCancelAfter`: a subscription whose notifications fail | 24 h | the subscription is cancelled |
| One attempt to deliver a notification | 1 min, whatever the `NotificationClient` | the attempt fails |
| The answer of a notification's receiver | 64 KiB read | the rest is not read |
| `server.Config.EventRetention`: an ended event in the server's scheduler | 24 h | forgotten (it stays in the store) |
| `client.Config.MaxResponseBytes`: the body of a response | 1 MiB | `ErrProtocol` |
| Response headers (`client.NewHTTPClient`) | 64 KiB | the request fails |
| A `Retry-After`, and a server's pollRate taken by the agent | 24 h | 24 h is used |
| `client.Config.MaxListItems`: the items `Iterate`, `Walk` and `FindEndDevice` read of a list | 100000 | `ErrProtocol` |
| `client.WalkOptions.MaxResources`, `MaxDepth`: a `Walk` | 4096 resources, 8 levels | `ErrProtocol` beyond the resources; deeper links are not followed |
| Redirects of a request, within the server | 10 | `ErrProtocol` |
| `client.NotificationConfig.MaxBytes`: a notification | 1 MiB | 413 |
| `client.NotificationConfig.MaxSubscriptions`: subscriptions whose last notification the handler remembers | 1000 | the least recently notified is forgotten |
| `agent.Config.MaxEvents`: the events of a function set | 1000 (`schedule.DefaultMaxEvents`) | new events are left out, logged and counted in `Agent.EventStats`; none when negative |
| `schedule.Options.QueueSize`: transitions that wait for a subscriber (an agent's handler) | 4 × `MaxEvents` | the queue keeps the latest transition of each event, then drops the oldest; counted in `Stats` |
| Responses and readings an agent keeps to send again | 1000 each | the Response is dropped; the oldest readings are dropped |
| Instances, and hosts, in the cache of `discovery.Browse` | 1024 each | records of new ones are dropped until records expire |
| Addresses of one host in that cache | 16 | further addresses are dropped |
| The TTL of a record in that cache | 75 min | 75 min is used |
| TXT keys of an instance, besides those the standard defines | 16, within 1300 bytes | further keys are dropped |
| A DNS name, a label, a message | 255 octets, 63 octets, 9000 octets | the message is dropped |

Tests hold these bounds: the hostile-peer tests of each package, and
allocation budgets for the bodies and for lists that claim 2^32-1 items
([TESTING.md](TESTING.md)).
