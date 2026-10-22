# Features

What go-ieee20305 implements, how it is tested, and how far each
implementation of [ieee20305-interop](https://github.com/otfabric/ieee20305-interop)
(v0.2.0, container contract 0.3) goes with it.

- **Server** and **Client / agent**: the packages `server` and `client`,
  `agent`.
- **e2e**: the end-to-end tests of `e2e/` that cover the feature, between our
  own client, agent and server, in both editions where the feature exists in
  both.
- **Envoy … EPRI**: the other implementation, in the role of its image:
  Envoy and GridAPPS-D as servers (against our client and agent), GridAPPS-D,
  py20305 and EPRI as clients (against our server). Envoy is a server only;
  py20305 and EPRI are clients only.
- **Interop op**: the operation of the container contract, or the step of
  the suite, that exercises the feature (see `interop/`).

| Mark | Meaning |
|------|---------|
| ✅ tested | Exercised against our side by the interop suite (`make interop`, the Interop workflow), and passing |
| ✅ | The stack implements it upstream; the image does not offer it, so it is not exercised |
| ◐ | Partly; see the note |
| ⚠ bug | A known upstream bug stops it; the suite skips it while the bug reproduces (`interop/known_test.go`) |
| — | The stack does not implement it, or not in this role |
| · | Not established either way |

`scripts/features.go` checks this table: every e2e test it names exists, and
every row marked tested names an interop operation the suite runs.

## Matrix

| Feature | Server | Client / agent | e2e | Envoy | GridAPPS-D server | GridAPPS-D client | py20305 | EPRI | Interop op |
|---|---|---|---|---|---|---|---|---|---|
| **Transport and security** | | | | | | | | | |
| Mutual TLS 1.2, ECDHE-ECDSA | ✅ | ✅ | `TestChaosClient`, `TestChaosServer` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `register` |
| CCM_8 cipher suite (module `tlsccm`) | ✅ | ✅ | — (the tlsccm module's tests) | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `register` |
| Certificates with HardwareModuleName; LFDI, SFDI | ✅ | ✅ | `TestAgentStopsOnCertificateError` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `register` |
| A device certificate of another CA refused | ✅ | ✅ | `TestAgentStopsOnCertificateError` | ✅ tested | ✅ tested | · | · | · | `device of another CA` |
| DNS-SD discovery | ✅ Advertise | ✅ Browse | `TestDiscoveryToClient` | — | ◐ ¹ | — | ◐ ¹ | ◐ ¹ | |
| **Registration and device** | | | | | | | | | |
| EndDevice found by LFDI; Registration and PIN check | ✅ | ✅ | `TestAgentProvisioned2018`, `TestAgentPINMismatch` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `register` |
| In-band registration (the device posts its EndDevice) | ✅ | ✅ | `TestAgentInBand` | ◐ ² | ◐ ² | · | · | · | `unprovisioned device` |
| Time | ✅ | ✅ | `TestTime`, `TestClockSkew` | ✅ tested | ✅ tested | · | · | · | `time` |
| FunctionSetAssignments | ✅ | ✅ | `TestDERControlLifecycle` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `poll-controls` |
| SelfDevice | ✅ | ✅ | `TestSelfDevice` | · | ✅ | · | ✅ | · | |
| DeviceInformation, DeviceStatus, PowerStatus, Configuration | ✅ | ✅ | `TestDeviceResources` | · | ✅ | · | · | · | |
| LoadShedAvailability | ✅ | ✅ | `TestDeviceResources` | · | · | · | · | · | |
| LogEvent | ✅ | ✅ | `TestDeviceResources`, `TestAggregatorClient` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ | `log-event` |
| **Distributed energy resources** | | | | | | | | | |
| DERProgram and DERControl events: schedule, primacy, supersede, cancel | ✅ | ✅ | `TestDERControlLifecycle`, `TestDERControlCancelAndSupersede` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `poll-controls` |
| DefaultDERControl | ✅ | ✅ | `TestDefaultDERControlAndCurrentProgram` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | — | `poll-controls` |
| DERCurve | ✅ | ✅ | `TestDERCurves` | · | ◐ ³ | ✅ | · | · | |
| DERCapability, DERSettings, DERStatus, DERAvailability | ✅ | ✅ | `TestDefaultDERControlAndCurrentProgram` | ✅ tested | — ⁴ | ✅ tested | ✅ tested | · | `put-der` |
| Responses to events (DERControlResponse) | ✅ | ✅ | `TestResponseDeduplication`, `TestEventFunctionSets` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ⚠ bug ⁵ | `respond` |
| CSIP (California): several programs, limits and fixed power | ✅ | ✅ | `TestDERControlLifecycle` | ✅ | ✅ tested | ✅ tested | ✅ tested | — | `poll-controls` |
| CSIP-AUS: site limits, DOE modes, ConnectionPoint (`csipaus`) | ✅ | ✅ | `TestCSIPAUS`, `TestCSIPClientErrors` | ✅ tested | — | — | ✅ tested | — | `poll-controls`, `put-der` |
| **Other function sets** | | | | | | | | | |
| Metering mirrors (MirrorUsagePoint, readings) | ✅ | ✅ | `TestMirrorMetering`, `TestFlowPolicy` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ⚠ bug ⁵ | `report` |
| Pricing: TariffProfile, RateComponent, TimeTariffInterval, ConsumptionTariffInterval | ✅ | ✅ | `TestEventFunctionSets` | ✅ tested | — | — | ◐ tested ⁶ | — | `poll-pricing` |
| Demand response and load control (EndDeviceControl) | ✅ | ✅ | `TestEventFunctionSets` | — | · | · | · | ◐ ⁷ | |
| Messaging (TextMessage) | ✅ | ✅ | `TestEventFunctionSets` | — | ◐ ³ | · | — | — | |
| Flow reservation | ✅ | ✅ | `TestFilesAndFlowReservation`, `TestFlowPolicy` | — | ✅ tested | ✅ tested | — | — | `flow-reservation` |
| Prepayment | ✅ | ✅ | `TestPrepaymentReads` | — | — | — | — | — | |
| Billing | ✅ | ✅ | `TestBilling` | — | — | — | — | — | |
| Software download (File, FileStatus) | ✅ | ✅ | `TestFilesAndFlowReservation` | — | — | — | — | — | |
| **Subscriptions** | | | | | | | | | |
| Subscription and Notification | ✅ | ✅ agent and `NotificationHandler` | `TestNotifications`, `TestAgentNotificationsServed`, `TestNotificationStorm`, `TestUndeliverable` | ◐ ⁸ | ✅ tested | ✅ | ✅ tested | — | `subscribe` |
| **Roles and editions** | | | | | | | | | |
| Aggregator managing devices | ✅ | ✅ | `TestAggregatorClient`, `TestAggregationByEdition` | ✅ | · | ✅ | · | · | |
| IEEE 2030.5-2018 | ✅ | ✅ | `TestAgentProvisioned2018`, `TestEventFunctionSets` | ✅ tested | ✅ tested | ✅ tested | ✅ tested | ✅ tested | `register` |
| IEEE 2030.5-2023 | ✅ | ✅ | `TestEventFunctionSets`, `TestDeviceResources` | — | ✅ tested | ✅ tested | ✅ tested | — | `register` |

1. The stack discovers servers by DNS-SD upstream. The containers run on
   Docker networks without multicast, so the contract's `discover` reaches
   the server by its URL, and DNS-SD itself is not exercised across stacks.
2. Our client sends an unprovisioned device: the server refuses it, or lets
   it register with a PIN of its own. Either is accepted, and the PIN check
   tells the device.
3. GridAPPS-D serves curves and text messages, but its image cannot be
   seeded with them, so no fixture has them.
4. The GridAPPS-D server of the image serves no DERList.
5. EPRI's client writes its documents in the name space of the draft sep.xsd
   2.0.4, `http://ieee.org/2030.5`; a server of IEEE 2030.5 refuses them
   with 400.
6. Tested with the baseline and csip fixtures. With csip-aus, py20305 does
   not model the `RateComponentLink` that CSIP-AUS 1.3 requires in a
   TimeTariffInterval, and finds no interval: a known bug.
7. EPRI's client handles some EndDeviceControls upstream; its image does not
   offer them.
8. Envoy accepts subscriptions from aggregator certificates only, and its
   image runs without notifications.

## Library

Beyond the protocol, each of the server, the client and the agent has
pluggable logging, metrics hooks and a retry policy
([OBSERVABILITY.md](../OBSERVABILITY.md)), one taxonomy of errors
([ERRORS.md](../ERRORS.md)), and an Admin API on the server to provision
devices and publish programs, events and tariffs. They are tested in
`TestObservability`, `TestNotificationMetrics`, `TestAgentRetries` and the
chaos tests (`TestChaosClient`, `TestChaosServer`, `TestChaosProxy`), with
500 devices at once in `TestManyAgents`.
