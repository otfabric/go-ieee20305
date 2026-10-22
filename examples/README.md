# Examples

Runnable programs, from the root of the repository. Each needs nothing but
Go: certificates come from `security.TestCA` and friends, and servers listen
on the loopback interface. `make build` builds them all into `./bin`.

| Example | What it shows |
|---------|---------------|
| [quickstart](quickstart/main.go) | A server and a device agent in one process: provisioning, a DER control, the agent scheduling it and the server receiving its Responses |
| [server](server/main.go) | A standalone server over mutual TLS, with a test PKI written to a directory, a provisioned device and a DER program |
| [client](client/main.go) | A device against that server: DeviceCapability, registration, the PIN check, and a walk of the resource tree |
| [csipaus](csipaus/main.go) | CSIP-AUS: default site limits, a dynamic operating envelope, and the connection point of a device |
| [observability](observability/main.go) | A log/slog logger, metrics hooks on the client and the server, and client retries over a server that answers 503 |

## quickstart

```console
$ go run ./examples/quickstart
device LFDI E31FC3641C84E6EFE7CA4CF241AB7EADDA851B85, SFDI 609681546898
server listening at https://127.0.0.1:49258/dcap
agent: control 03 is Scheduled
server: response 1 to control 03
agent: control 03 is Active
server: response 2 to control 03
```

The agent receives the control (Response 1, *event received*), and starts it
(Response 2, *event started*). Identifiers and ports differ on every run.

## server and client

```console
$ go run ./examples/server -pki /tmp/pki
wrote a test PKI to /tmp/pki
provisioned device 5FE403DD679902C405FFEEC5AC4C21304A00DE5A (SFDI 257404594785) with PIN 111115
serving https://127.0.0.1:8443/dcap
```

In another terminal:

```console
$ go run ./examples/client -pki /tmp/pki
EndDevice /edev/1 (created: false) for LFDI 5FE403DD679902C405FFEEC5AC4C21304A00DE5A
the PIN of the Registration matches
/dcap                                    *ieee20305.DeviceCapability (via root)
  /derp                                    *ieee20305.DERProgramList (via DERProgramListLink)
  /rsps                                    *ieee20305.ResponseSetList (via ResponseSetListLink)
  /tm                                      *ieee20305.Time (via TimeLink)
  /edev                                    *ieee20305.EndDeviceList (via EndDeviceListLink)
    /derp/1/derc                             *ieee20305.DERControlList (via DERControlListLink)
    /edev/1/fsa                              *ieee20305.FunctionSetAssignmentsList (via FunctionSetAssignmentsListLink)
    /edev/1/rg                               *ieee20305.Registration (via RegistrationLink)
    …
      /edev/1/fsa/1/derp                       *ieee20305.DERProgramList (via DERProgramListLink)
```

## csipaus

```console
$ go run ./examples/csipaus
default limits: export 5000 W, import 5000 W, generation -, load -
connection point: 4407123456
active envelope: export 1500 W, import -, generation -, load -
```

## observability

```console
$ go run ./examples/observability
client: responses and retries
  GET /dcap 200                            5
  GET /edev 200                            5
  retry GET /dcap                          4
server: responses by WADL resource
  GET DeviceCapability 200                 5
  GET EndDeviceList 200                    5
```

Every third request gets a 503 before it reaches the server; the client's
`ExponentialBackoff` retries it, and only the answers of the server itself
are counted by the server's metrics.

## Godoc examples

`Example…` functions in [example_test.go](../example_test.go) and
[security/example_test.go](../security/example_test.go) show the codec, the
error classification, the backoff and the device identifiers; `go test`
checks their output.
