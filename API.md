# API Reference: otfabric/go-ieee20305

The public API by package. The godoc on
[pkg.go.dev](https://pkg.go.dev/github.com/otfabric/go-ieee20305) is the
authoritative text for each symbol; this document is the map.

## Table of contents

- [Package `ieee20305`](#package-ieee20305)
- [Package `client`](#package-client)
- [Package `agent`](#package-agent)
- [Package `server`](#package-server)
- [Package `security`](#package-security)
- [Package `schedule`](#package-schedule)
- [Package `discovery`](#package-discovery)
- [Packages `csipaus`, `csipaus/csipserver`, `csipaus/csipclient`](#packages-csipaus-csipauscsipserver-csipauscsipclient)
- [Module `tlsccm`](#module-tlsccm)
- [Concurrency](#concurrency)

## Package `ieee20305`

`import ieee20305 "github.com/otfabric/go-ieee20305"`

The resource model, and what the other packages share.

### Resources

Every type of IEEE 2030.5-2023 (`sep.xsd` 2.2) is a Go struct generated from
the schema (`*_gen.go`): `DeviceCapability`, `EndDevice`, `DERProgram`,
`DERControl`, `MirrorUsagePoint`, `TariffProfile`, … Base types are embedded,
optional elements are pointers, repeated elements slices, and the schema's
scalar types named types (`UInt32`, `String32`, `HexBinary160`, `MRIDType`,
`TimeType`, …). Each type has:

- `Validate() error`: the facets and cardinalities of the schema, as a
  `*ValidationError`;
- `Get…` methods returning the schema default of an absent value
  (`DeviceCapability.GetPollRate`);
- the link and list accessors of `Linker`, `Lister` and `Hrefer`.

| Function | Use |
|----------|-----|
| `Marshal(v)`, `Unmarshal(data, v)` | Encode and decode a document of IEEE 2030.5-2023 |
| `MarshalEdition(v, e)` | Encode for an edition: for `Edition2018`, without what 2018 does not define |
| `Decode(data)` | Decode a document of any element into its type |
| `ElementName(v)`, `NewElement(name)` | The document element of a type, and back |
| `Resources`, `LookupResource(id)` | The resource table of the WADL: sample path, methods, conformance |
| `Links(v)` | Every link of a document, with the WADL resource it points to |
| `Clone(v)`, `Derives(elem, of)`, `InEdition(id, e)` | Deep copy; type derivation; whether a resource exists in an edition |
| `ParseEdition(s)`, `Edition2018`, `Edition2023` | Editions; `Edition.Codec()` is the codec of one |
| `StandardCodec`, `Codec` | The codec of the package, and the interface `csipaus.Codec` also implements |
| `MediaType` | `application/sep+xml` |

Elements a schema admits without declaring (`xs:any`) are kept as
`ExtensionElements`.

### Errors

```go
var (
	ErrInvalid       // a value the library refuses
	ErrConfiguration // a configuration that cannot work
	ErrProtocol      // a peer broke the schema or the protocol
	ErrCertificate   // a certificate was rejected, by either end
	ErrTimeout       // no answer in time
	ErrUnavailable   // refused or reset connection, DNS failure, 503, 429
	ErrNotFound      // 404
	ErrForbidden     // 403
	ErrBadRequest    // 400
	ErrConflict      // 409
	ErrServer        // 5xx
	ErrCanceled      // the context was cancelled
	ErrClosed        // used after Close
)

type StatusError struct {
	Method, Path string
	StatusCode   int
	Document     *Error        // the server's Error document, if it sent one
	RetryAfter   time.Duration // the server's Retry-After
}

func Retryable(err error) bool          // whether trying again may succeed
func RetryAfter(err error) time.Duration // the delay a server asked for
```

A `*StatusError` matches the sentinel of its status with `errors.Is`. See
[ERRORS.md](ERRORS.md).

### Logging, retries, metrics

```go
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
type FieldLogger interface { Logger; With(kv ...any) FieldLogger }

func NewSlogLogger(h slog.Handler) FieldLogger
func NewStdLogger(l *log.Logger, debug bool) FieldLogger
func NopLogger() Logger
func ComponentLogger(l Logger, name string) Logger

type RetryPolicy interface {
	ShouldRetry(attempt int, err error) (retry bool, wait time.Duration) // attempt from 0
}
func NoRetry() RetryPolicy
type ExponentialBackoff struct {
	Initial, Max time.Duration // 100 ms and 30 s when zero
	Attempts     int           // 3 when zero, unbounded when negative
	NoJitter     bool
}
```

Metrics interfaces are per package: `client.Metrics`, `server.Metrics`,
`agent.Metrics`. See [OBSERVABILITY.md](OBSERVABILITY.md).

## Package `client`

`import "github.com/otfabric/go-ieee20305/client"`

A client of one server, over HTTPS with mutual TLS.

### Construction

```go
func New(cfg Config) (*Client, error)
func NewHTTPClient(device tls.Certificate, roots *x509.CertPool) *http.Client

type Config struct {
	Server               string       // URL of the server, or of its DeviceCapability
	DeviceCapabilityPath string       // "/dcap" when empty and Server has no path
	HTTPClient           *http.Client // with the device's certificate: NewHTTPClient
	Codec                Codec        // ieee20305.StandardCodec when nil; csipaus.Codec{} for CSIP-AUS
	UserAgent            string
	MaxResponseBytes     int64
	RetryPolicy          ieee20305.RetryPolicy // NoRetry when nil
	RetryPOST            bool                  // retry POSTs too (the server deduplicates)
	Metrics              Metrics
	Logger               ieee20305.Logger
	MaxListItems         uint32 // the most items Iterate reads; 100000 when zero
}
```

### Resources

| Method | Use |
|--------|-----|
| `DeviceCapability(ctx)` | The root of the resource tree |
| `Get(ctx, href, v)`, `Fetch(ctx, href)`, `Head(ctx, href)` | Read a resource into `v`, or into the type of its element |
| `GetList(ctx, href, opts, v)`, `FetchList(ctx, href, opts)` | One page of a list (`ListOptions{Start, Limit, SFDI}`) |
| `Iterate(ctx, href, pageSize, fn)` | Every item of a list, page by page |
| `Walk(ctx, root, opts, visit)` | The resource tree, breadth first, each resource once (`Step{Href, Link, Doc, Depth}`) |
| `Post`, `Put`, `Delete` | Write; `Post` returns the `Location` |
| `Resolve(href)` | The URL of an href; links to another server are refused |
| `Time(ctx)` | The server's clock (`TimeSync.Offset`) |

### Registration

```go
func IdentityOf(cert *x509.Certificate) Identity // LFDI and SFDI of a certificate
func (c *Client) FindEndDevice(ctx, listHref string, id Identity) (*ieee20305.EndDevice, error)
func (c *Client) Register(ctx, listHref string, id Identity) (ed *ieee20305.EndDevice, created bool, err error)
func (c *Client) Registration(ctx, ed *ieee20305.EndDevice) (*ieee20305.Registration, error)
func CheckPIN(r *ieee20305.Registration, pin uint32) error // ErrPINMismatch
var ErrNotRegistered, ErrPINMismatch
```

### Subscriptions

```go
func (c *Client) Subscribe(ctx, list, resource, notificationURI string, limit uint32) (string, error)
func (c *Client) Unsubscribe(ctx, href string) error

func NewNotificationHandler(handle func(ctx context.Context, n *ieee20305.Notification) error,
	cfg NotificationConfig) (*NotificationHandler, error) // an http.Handler
type NotificationConfig struct {
	Codec            Codec
	Authorize        func(r *http.Request) error // by the peer's certificate, say
	MaxBytes         int64
	MaxSubscriptions int // the subscriptions whose last notification is remembered
}
```

The handler passes on a notification only when its content is new, and
answers a failed `handle` with 500 so that the server retries.

### Metrics

`Metrics` (`OnRequest`, `OnResponse`, `OnError`), the optional
`AttemptMetrics` (`OnRetry`), and `NopMetrics` to embed.

## Package `agent`

`import "github.com/otfabric/go-ieee20305/agent"`

The behaviour of a device on a client.

```go
func New(cfg Config) (*Agent, error)
func (a *Agent) Run(ctx context.Context) error   // register, then poll until ctx ends or a permanent error
func (a *Agent) Start(ctx context.Context) error // only register: DeviceCapability, time, EndDevice, PIN, FSAs
func (a *Agent) Close()
```

### Config

| Field | Use |
|-------|-----|
| `Client`, `Identity` | The client of the device's certificate, and `client.IdentityOf` it |
| `PIN`, `VerifyPIN` | The out-of-band PIN, checked against the Registration |
| `DeviceInformation` | Put to the EndDevice after registration |
| `PollRate` | Overrides the server's pollRate |
| `Backoff` | `ieee20305.ExponentialBackoff` for failures; permanent errors stop `Run` |
| `NotificationURI`, `SubscribedPollRate` | Subscribe to the FSAs, programs and controls, and poll less |
| `OnAssignments` | The FunctionSetAssignments, whenever they change |
| `OnDERControl`, `OnDefaultDERControl` | DER events and the default control |
| `OnEndDeviceControl`, `OnTextMessage`, `OnTimeTariffInterval`, `OnSupplyInterruptionOverride`, `OnFlowReservation` | The events of DRLC, messaging, pricing, prepayment and flow reservation, as `schedule.Transition`s |
| `OnFile` | Files offered for download |
| `MirrorUsagePoint`, `Readings`, `PostRate` | Metering: the mirror, and the readings posted at `PostRate` |
| `Clock`, `Seed` | The clock of scheduling, and the seed of randomization |
| `Metrics`, `Logger` | Plug points |

The agent posts the Responses events ask for (received, started,
completed, superseded, cancelled), and retries those that fail.

### Methods

| Method | Use |
|--------|-----|
| `State()` | A copy of what the agent knows: DeviceCapability, time offset, EndDevice, Registration, FSAs, counts of polls, Responses and posts |
| `Active(fs)`, `ActiveDERControls()`, `Events(fs)` | The events of a function set (`DER`, `DRLC`, `Messaging`, `Pricing`, `Prepayment`, `FlowReservation`) |
| `Log(ctx, ev)` | Post a LogEvent |
| `PutDeviceStatus`, `PutPowerStatus`, `PutFileStatus` | Report the device |
| `Mirror(ctx, mup)`, `PostReadings(ctx, mup, readings...)` | Metering outside `Readings` |
| `RequestFlowReservation(ctx, req)` | Ask for a flow reservation |
| `NotificationHandler()` | The `http.Handler` to serve at `NotificationURI` |

`ErrNotStarted` is returned by calls that need what `Start` reads.

## Package `server`

`import "github.com/otfabric/go-ieee20305/server"`

An IEEE 2030.5 server as an `http.Handler`.

```go
func New(cfg Config) (*Server, error)
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *Server) Admin() *Admin
func (s *Server) Store() Store
func (s *Server) Close() error
```

### Config

| Field | Use |
|-------|-----|
| `Store` | Where resources live; `NewMemStore` when nil |
| `Profile`, `DefaultEdition` | The documents served (`StandardProfile`, `csipserver.Profile`) and the default edition of devices |
| `PeerCertificates` | How a request's client certificates are found (`tlsccm.PeerCertificates` behind the CCM_8 listener) |
| `InBandRegistration` | Unknown devices may post their EndDevice |
| `PollRate`, `DefaultLimit`, `MaxLimit`, `MaxRequestBytes` | What the server tells and takes |
| `EnableDiscouraged`, `Disable`, `ClientLists` | Methods beyond, or short of, the WADL's conformance |
| `NotificationClient`, `NotificationURIAllowed`, `NotificationWorkers`, `NotificationRetry`, `NotificationCancelAfter` | Subscriptions: the client of the notifier (`NewNotificationClient`), the guard of the URIs devices choose (`DefaultNotificationURIAllowed`), and delivery |
| `EventClock`, `EventRetention` | The clock of the event lifecycle, and how long ended events stay |
| `OnResponse` | Each Response, once |
| `FlowPolicy` | Answers FlowReservationRequests (`GrantAll`) |
| `MaxListItems` | The cap of the lists devices post to |
| `Hooks` | Per WADL resource: `Get`, `Create`, `Update`, `Delete` of your own |
| `Clock`, `Metrics`, `Logger` | Plug points |

### Admin

The operator's API, beside what devices see.

| Method | Use |
|--------|-----|
| `ProvisionDevice(ctx, DeviceSpec{LFDI, Edition, Role, ManagedBy, Register, PIN, EndDevice})` | Tell the server of a device; returns its EndDevice href |
| `Register(ctx, lfdi, pin)`, `RemoveDevice`, `EndDevice(lfdi)` | Registration and lookup |
| `AssignFSA(ctx, lfdi, fsa)` | Give a device a FunctionSetAssignments |
| `Root(ctx, resource, href, v)` | A top-level resource, linked from DeviceCapability |
| `NewList(ctx, resource, href, parent, link, owner)` | A list under a resource |
| `Create(ctx, list, v, owner)`, `AddMember(ctx, list, member)` | Create an item, with its parts; add an existing resource to a list |
| `Get`, `Put`, `Delete`, `Members` | Raw access to the nodes of the store |
| `CancelEvent(ctx, href, randomized)` | Cancel an event |
| `Document(resource)`, `Now()` | An empty document of a resource; the server's clock |

`Role` is `RoleDevice`, `RoleAggregator` or `RoleOperator`. `ErrNoNode`
matches `ieee20305.ErrNotFound`. A hook's error may be an `*HTTPError`, or
a sentinel the server answers with its status.

### Metrics

`Metrics` (`OnRequest`, `OnResponse`), the optional `NotificationMetrics`
and `EventMetrics`, and `NopMetrics`.

## Package `security`

`import "github.com/otfabric/go-ieee20305/security"`

| Symbol | Use |
|--------|-----|
| `LFDI`, `CertificateLFDI(cert)`, `DERBytesLFDI(der)`, `ParseLFDI(s)` | The Long-Form Device Identifier |
| `SFDI`, `LFDI.SFDI()`, `SFDI.Valid()` | The Short-Form Device Identifier, with its check digit |
| `HMN`, `HardwareModuleName(cert)`, `MarshalHMNSAN(h)` | The HardwareModuleName of a certificate |
| `Verify(chain, VerifyOptions)`, `VerifyPeerCertificate(o)` | Chain verification that accepts the critical HardwareModuleName |
| `ClientTLSConfig`, `ServerTLSConfig`, `NotificationTLSConfig` | TLS configurations of a device, a server and a notifier |
| `TestCA`, `TestServerCert`, `TestDeviceCert`, `TestDeviceCertHMN`, `TestServerCertHMN` | Throwaway certificates for tests |

## Package `schedule`

`import "github.com/otfabric/go-ieee20305/schedule"`

```go
func New(o Options) *Scheduler
func FromEvent(v any, primacy uint8, program string) (Event, error) // from a DERControl, EndDeviceControl, …

func (s *Scheduler) Upsert(e Event)
func (s *Scheduler) Cancel(mrid string, randomized bool)
func (s *Scheduler) Remove(mrid string) bool
func (s *Scheduler) Get(mrid string) (Event, bool)
func (s *Scheduler) Events() []Event
func (s *Scheduler) Active(at time.Time) []Event
func (s *Scheduler) Window(mrid string) (start, end time.Time, ok bool)
func (s *Scheduler) Timeline(from, to time.Time) []Segment
func (s *Scheduler) Subscribe() (<-chan Transition, func())
func (s *Scheduler) Close()
```

`Options` sets the clock, the seed, randomization and the conflict rule (`DERConflicts`,
`DRLCConflicts`, `AllConflict`). `Status` is `Scheduled`,
`Active`, `Completed`, `Cancelled`, `Superseded`, …; `ResponseRequired(flags,
t)` says whether, and with which status, a transition is answered.

## Package `discovery`

`import "github.com/otfabric/go-ieee20305/discovery"`

```go
func Browse(ctx context.Context, o BrowseOptions) (<-chan Service, error)
func Advertise(ctx context.Context, s Service, o AdvertiseOptions) (*Advertisement, error)
func NewMulticastTransport(ifi *net.Interface) (PacketTransport, error)
```

`Service` is a resolved `_smartenergy._tcp` instance; `Service.DcapURL()`
is the URL of its DeviceCapability. `ErrNoDcap` matches `ErrProtocol`.

## Packages `csipaus`, `csipaus/csipserver`, `csipaus/csipclient`

- `csipaus`: the CSIP-AUS v1.3 types (generated): `DERControl`,
  `DefaultDERControl`, `DERControlBase` with the site limits, `EndDevice`
  with its `ConnectionPointLink`, `TariffProfile`, … `Codec{}` encodes and
  decodes them. `Limits` and `LimitsOf(v)` are the dynamic operating
  envelope of a control.
- `csipserver`: `Configure(cfg)` and `Profile()` make a server of the
  CSIP-AUS profile; `Envelope(mrid, start, d, limits)`,
  `SetDefaultLimits(ctx, admin, program, limits)` and `Watts(w)` publish
  limits.
- `csipclient`: `EndDevice(ctx, c, href)`, `ConnectionPoint(ctx, c, ed)`,
  `SetConnectionPoint(ctx, c, ed, id)`.

## Module `tlsccm`

`import "github.com/otfabric/go-ieee20305/tlsccm"` (Go 1.26.8)

```go
func NewHTTPClient(device tls.Certificate, roots *x509.CertPool, o Options) *http.Client
func NewListener(inner net.Listener, server tls.Certificate, clientCAs *x509.CertPool, o Options) (net.Listener, error)
func ConfigureServer(s *http.Server)
func PeerCertificates(r *http.Request) ([]*x509.Certificate, error) // for server.Config
type Options struct {
	AllowGCM         bool // offer AES-128-GCM after CCM_8
	HandshakeTimeout time.Duration
	Logger           ieee20305.Logger
}
```

See [tlsccm/README.md](tlsccm/README.md).

## Concurrency

- A `client.Client`, a `client.NotificationHandler`, a `server.Server` (and
  its `Admin`), and a `schedule.Scheduler` are safe for concurrent use.
- An `agent.Agent` runs its own goroutines; its methods may be called from
  any goroutine. Its handlers (`OnDERControl`, …) are called from the agent's
  goroutines: make them safe for concurrent use, and return quickly.
- `Logger` and `Metrics` implementations are called synchronously from many
  goroutines: they must be safe for concurrent use and must not block.
- A `server.Store` must be safe for concurrent use.
