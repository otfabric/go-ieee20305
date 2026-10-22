# Observability

The clients, servers and agents of go-ieee20305 report what they do through
two plug points in their configuration: a `Logger` and a `Metrics`. Both
are interfaces of a few methods, nil by default (silent), and free of
dependencies. Adapt them to slog, Prometheus, OpenTelemetry or whatever you
run, in your own code.

## Logging

```go
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
```

Set `Logger` in `client.Config`, `server.Config`, `agent.Config`,
`discovery.BrowseOptions`, `discovery.AdvertiseOptions` and
`tlsccm.Options`. Ready-made adapters:

| Constructor | Writes to |
|---|---|
| `ieee20305.NewSlogLogger(h slog.Handler)` | a `log/slog` handler, at its levels |
| `ieee20305.NewStdLogger(l *log.Logger, debug bool)` | a standard `log.Logger`, with the level in front |
| `ieee20305.NopLogger()` | nowhere (what nil means) |

A `FieldLogger` (`With(kv ...any) FieldLogger`), which both adapters are,
gets a `component` field naming the package (`server`, `client`, `agent`,
`discovery`, `tlsccm`).

What is logged:

| Level | Server | Client | Agent |
|---|---|---|---|
| Debug | every request: method, path, device LFDI, status, duration; refused requests with their reason | every request: method, path, status or error, duration; retries | — |
| Info | devices provisioned and registered; subscriptions created | — | started; waiting for registration |
| Warn | each failed notification attempt; lists trimmed | — | failures it retries; Responses or readings dropped |
| Error | 5xx answers; event statuses it cannot write | — | the error that stops `Run` |

A Logger is called from many goroutines and must not block.

## Metrics

Each package has its own `Metrics` interface. The methods are synchronous,
called from the goroutines doing the work: they must be safe for
concurrent use, quick, and must not block. Embed the package's `NopMetrics`
to implement only some. Optional interfaces extend them, and are found by
type assertion.

### `client.Metrics`

```go
OnRequest(method, path string)
OnResponse(method, path string, status int, d time.Duration)
OnError(method, path string, d time.Duration, err error)
// client.AttemptMetrics, optional:
OnRetry(method, path string, attempt int, err error, wait time.Duration)
```

They are called once per call of the client, whatever its retries. `d`
includes the retries. `path` is an href of the server: aggregate it before
using it as a label. Classify `err` with the sentinels of
[ERRORS.md](ERRORS.md).

### `server.Metrics`

```go
OnRequest(method string)
OnResponse(method, resource string, status int, d time.Duration)
// server.NotificationMetrics, optional:
OnNotification(resource string, d time.Duration, err error)
OnSubscriptionCancelled(resource string, status uint8)
// server.EventMetrics, optional:
OnEventTransition(element string, from, to schedule.Status)
```

`resource` is the WADL resource ID of the request's target, such as
`EndDeviceList` (`""` for a path the server does not have). There are a few
hundred at most, so it can be a label as it is. A notification is reported
once its delivery ends, after retries.

### `agent.Metrics`

```go
OnPoll(d time.Duration, err error)
OnEvent(fs agent.FunctionSet, t schedule.Transition)
OnResponse(fs agent.FunctionSet, status uint8, err error)
```

### A Prometheus adapter, in your code

```go
type promServer struct {
	server.NopMetrics
	requests *prometheus.HistogramVec // labels: method, resource, status
}

func (m promServer) OnResponse(method, resource string, status int, d time.Duration) {
	m.requests.WithLabelValues(method, resource, strconv.Itoa(status)).Observe(d.Seconds())
}

srv, err := server.New(server.Config{Metrics: promServer{requests: ...}})
```

## Retries

Retries are configured, not hidden:

| Where | Setting | Default |
|---|---|---|
| Client | `Config.RetryPolicy`, `Config.RetryPOST` | `NoRetry()` |
| Agent | `Config.Backoff` | from 1 s up to 5 min, with jitter, without end, except after errors waiting does not fix |
| Server notifications | `Config.NotificationRetry`, `Config.NotificationCancelAfter` | from 1 s up to 5 min, with jitter, for up to 24 h; a 4xx cancels at once |

`ieee20305.ExponentialBackoff` retries what `ieee20305.Retryable` says may
succeed, and honours a server's `Retry-After`. Write your own
`RetryPolicy` for anything else. Retries show in the Debug log, in
`client.AttemptMetrics.OnRetry`, and in the Warn lines of the agent and
the notifier.

## Tracing a session

To see the documents themselves, wrap the server's handler or the client's
transport. The interop suite does this with `IEEE20305_INTEROP_TRACE=1`;
see `interop/server_test.go`.
