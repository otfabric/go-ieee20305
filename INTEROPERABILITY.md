# Interoperability: otfabric/go-ieee20305

What go-ieee20305 implements of IEEE 2030.5-2018 and IEEE 2030.5-2023, and
of the CSIP and CSIP-AUS profiles, area by area; the decisions it takes where
the standard leaves room; and how it is verified. Which other implementation
does what, and which of our features are exercised against them, is in
[docs/FEATURES.md](docs/FEATURES.md).

Marks: ✅ implemented · ◐ partly (see the note) · — not implemented.

## Transport and security

| Requirement | Client | Server | Notes |
|-------------|:------:|:------:|-------|
| HTTP/1.1 over TLS 1.2, mutual authentication | ✅ | ✅ | `security.ClientTLSConfig`, `ServerTLSConfig`; TLS 1.2 minimum |
| TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 | ◐ | ◐ | In the module `tlsccm` (a fork of the Go TLS stack); the core module offers the ECDHE-ECDSA AES-GCM suites |
| ECDSA P-256 certificates with a HardwareModuleName | ✅ | ✅ | `security.Verify` accepts the critical subjectAltName that crypto/x509 refuses |
| LFDI and SFDI from the certificate fingerprint | ✅ | ✅ | `security.CertificateLFDI`, `LFDI.SFDI` with its check digit |
| `application/sep+xml`, namespace `urn:ieee:std:2030.5:ns` | ✅ | ✅ | Documents of another namespace are refused (400) |
| Service discovery (DNS-SD, `_smartenergy._tcp`, subtypes) | ✅ | ✅ | `discovery.Browse`, `Advertise`; see [Known deviations](#known-deviations) |

## Resources and methods

| Requirement | Client | Server | Notes |
|-------------|:------:|:------:|-------|
| Every resource of the WADL | ✅ | ✅ | The resource table is generated from `sep_wadl.xml` |
| Method conformance (mandatory, optional, discouraged) | — | ✅ | 405 for what the WADL does not allow; discouraged methods only with `Config.EnableDiscouraged` |
| Links, not paths: resources found from DeviceCapability | ✅ | ✅ | The client refuses links and redirects to another server |
| `href` on every resource; ListLink `all` | ✅ | ✅ | The server fills both on every GET |
| List paging (`s`, `l`), the ordering of each list | ✅ | ✅ | `client.Iterate`; the server orders each list as the standard does |
| `pollRate` | ✅ | ✅ | The agent polls at the server's rate |
| Error documents with reason codes | ✅ | ✅ | `ieee20305.StatusError.Document` |
| Time | ✅ | ✅ | `client.Time` reports the offset of the server's clock |

## Function sets

| Function set | Client and agent | Server | Notes |
|--------------|:----------------:|:------:|-------|
| Device capability, EndDevice, registration and PIN | ✅ | ✅ | Provisioned (`Admin.ProvisionDevice`) or in-band registration |
| Function set assignments | ✅ | ✅ | |
| SelfDevice, DeviceInformation, DeviceStatus, PowerStatus, Configuration | ✅ | ✅ | |
| LoadShedAvailability, IPInterface, LogEvent | ✅ | ✅ | Lists devices post to are capped (`MaxListItems`) |
| DER: programs, controls, default controls, curves, DER resources | ✅ | ✅ | Primacy, supersession by opMod, randomization |
| Demand response and load control | ✅ | ✅ | Conflicts by device category |
| Pricing (TOU, block, consumption tariff intervals) | ✅ | ✅ | |
| Messaging | ✅ | ✅ | |
| Prepayment | ✅ | ✅ | |
| Billing | ✅ | ✅ | |
| Metering, metering mirror | ✅ | ✅ | A mirror becomes a UsagePoint with its readings |
| Flow reservation | ✅ | ✅ | `server.Config.FlowPolicy` answers requests |
| Software download (File, FileStatus) | ✅ | ✅ | |
| Responses (`responseRequired`, `replyTo`) | ✅ | ✅ | Stored once per device, event and status |
| Subscription and Notification | ✅ | ✅ | Coalesced, retried, cancelled when undeliverable |
| Aggregation (AggregatedDevice, managed devices) | ✅ | ✅ | `RoleAggregator` acts for the devices it manages |

## Event lifecycle

| Requirement | Client and agent | Server |
|-------------|:----------------:|:------:|
| Scheduled, Active, Completed, Cancelled, Superseded | ✅ | ✅ (EventStatus) |
| Primacy, then creation time, decides between conflicting events | ✅ | ✅ |
| `randomizeStart`, `randomizeDuration` | ✅ | — servers do not randomize |
| Cancellation with and without randomization | ✅ | ✅ `Admin.CancelEvent` |
| Responses as `responseRequired` asks (received, started, completed, superseded, cancelled) | ✅ | ✅ |
| Default controls when no event applies | ✅ | ✅ |

## Editions

The model follows IEEE 2030.5-2023 (`sep.xsd` 2.2). A 2018 peer receives
documents without what 2018 does not define (`MarshalEdition`, generated
`Strip2018`), and the server takes the edition of each device from its
provisioning (`DeviceSpec.Edition`, `Config.DefaultEdition`). Resources that
exist in one edition only (`InEdition`) are served to that edition only.

## Profiles

| Profile | Support |
|---------|---------|
| CSIP (California) | ✅ The DER programs, controls and responses CSIP requires are those of the base standard; tested with the `csip` fixture of ieee20305-interop |
| CSIP-AUS v1.3 (Australia) | ✅ The types of the CSIP-AUS schemas (`csipaus`), site limits and DOE modes, the ConnectionPoint of each EndDevice, CSIP-AUS tariffs (`csipserver`, `csipclient`) |

## Decisions

Where the standard leaves room, the library takes these decisions:

- **PUT answers 204.** Also the first PUT of a resource the server linked
  without a value (a DERSettings): the resource exists, the PUT updates it.
- **Notifications carry the latest state.** A burst of changes to a
  subscribed resource is one notification of the state after the burst.
- **The client does not repeat a POST** after a failure unless `RetryPOST`
  is set, since a lost answer leaves the outcome unknown; the server
  deduplicates EndDevices, Responses and mirrors, which makes it safe there.
- **Notification URIs are guarded.** The server does not post to loopback,
  link-local, multicast or unspecified addresses unless
  `NotificationURIAllowed` says so.

## Known deviations

- **CCM_8 is not in the core module**; see above. A deployment that must
  offer it uses `tlsccm`.
- **DNS-SD uses the link-local mDNS groups** (224.0.0.251, ff02::fb, port
  5353). IEEE 2030.5 specifies xmDNS, whose site-local scope this package
  does not join.
- **The server keeps no history beyond its Store**: ended events are pruned
  after `EventRetention`, and capped lists drop their oldest items.

## Verification status

- **End to end**, between our own client, agent and server, for every
  function set in both editions, under injected faults and load (`e2e/`).
- **Against four other implementations**, Envoy, GridAPPS-D (server and
  client), py20305 and EPRI, through
  [ieee20305-interop](https://github.com/otfabric/ieee20305-interop)
  v0.2.0, on every push ([README](README.md#interop-tests)).
- **Generated types** against the examples of the IEEE 2030.5-2023
  supplemental material, and encoded documents against `sep.xsd`.
- **Not** against certified test equipment (the SunSpec or UL harnesses)
  or field devices.
