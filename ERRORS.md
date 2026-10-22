# Errors

Every error of go-ieee20305 matches one of a small set of sentinels of the
root package, through `errors.Is`. Typed errors carry the details and are
found with `errors.As`. Error text is for people and may change; test the
sentinels and the types.

```go
_, err := c.DeviceCapability(ctx)
switch {
case errors.Is(err, ieee20305.ErrCertificate):
	// fix the PKI: sending again will not help
case ieee20305.Retryable(err):
	time.Sleep(max(ieee20305.RetryAfter(err), time.Second)) // or set a RetryPolicy
case errors.Is(err, ieee20305.ErrNotFound):
	// the server has no such resource
}
```

## Sentinels

### Answers of a server

A `*StatusError`, which a client returns for a non-2xx response, matches the
sentinel of its status.

| Sentinel | Status | Retryable |
|---|---|---|
| `ErrBadRequest` | 400 | no |
| `ErrUnauthorized` | 401 | no |
| `ErrForbidden` | 403 | no |
| `ErrNotFound` | 404 | no |
| `ErrConflict` | 409 | no |
| `ErrUnavailable` | 503, 429 | yes, after `RetryAfter` |
| `ErrServer` | 5xx (503 too) | yes |

`StatusError` holds the request (`Method`, `Path`), the `StatusCode`, the
server's `Error` document when it sent one (its `ReasonCode` says why), and
the `RetryAfter` it asked for.

### Other failures

| Sentinel | Meaning | Retryable |
|---|---|---|
| `ErrUnavailable` | The server could not be reached: connection refused or reset, a host name that does not resolve | yes |
| `ErrTimeout` | The context's deadline, the HTTP client's timeout, or a network timeout | yes |
| `ErrCanceled` | The context was canceled | no |
| `ErrCertificate` | A certificate did not verify, either way (unknown CA, expired, wrong name or usage, a peer's certificate alert, no trusted CAs given), or an LFDI pin did not match | no |
| `ErrProtocol` | An answer that is not what IEEE 2030.5 says: undecodable, too large, the wrong document, a redirect to another server, a list that never ends | no |
| `ErrInvalid` | A document or value that does not satisfy the schema; `*ValidationError` names the field | no |
| `ErrConfiguration` | A configuration a constructor cannot work with | no |
| `ErrClosed` | Use after Close: a server's store, an agent, discovery | no |

`ieee20305.Retryable(err)` is true for `ErrUnavailable`, `ErrServer` and
`ErrTimeout`, unless the error also matches `ErrCanceled`, `ErrCertificate`,
`ErrConfiguration`, `ErrClosed` or `ErrProtocol`. Whether a request is safe
to send again is the caller's to decide (see the `RetryPolicy` of the
client). `ieee20305.RetryAfter(err)` returns the delay the server asked for.

A classified error keeps the error it came from in its chain. `errors.As`
finds a `*net.OpError`, an `x509.UnknownAuthorityError`, a
`*tls.CertificateVerificationError` and so on.

## By package

| Package | Returns |
|---|---|
| root | `*ValidationError` (`ErrInvalid`) from `Validate` and the codecs; `ErrProtocol` for documents that do not decode |
| `client` | `*StatusError`, and the transport sentinels above. `ErrNotRegistered`: an EndDevice without a RegistrationLink. `ErrPINMismatch`: a Registration whose PIN is not the device's |
| `agent` | What the client returns. `ErrNotStarted`: a call that needs what `Start` reads. `ErrClosed` after `Close`. `Run` stops on `ErrPINMismatch` and `ErrClosed`, and retries the rest |
| `server` | `ErrNoNode` (matches `ErrNotFound`) from a `Store`. `ErrConfiguration` from `New`. `ErrClosed` for changes after `Close`. `ErrInvalid` from `Admin` for unknown resources |
| `security` | `ErrCertificate` from `Verify` and `VerifyPeerCertificate`; `ErrInvalid` from `ParseLFDI` |
| `schedule` | `ErrInvalid` from `FromEvent` for a value that is not an event |
| `discovery` | `ErrConfiguration` for a service that cannot be advertised. `ErrProtocol` for a record that names no usable server (`ErrNoDcap`). `ErrClosed` (and `net.ErrClosed`) after Close |
| `tlsccm` | `ErrConfiguration` from `NewListener` without client CAs. `ErrNoConnection` (matches `ErrCertificate`) from `PeerCertificates` |

## In a server's hooks

A hook may return any error. The server answers with:

- the status of an `*HTTPError` (500 when its `Status` is zero), with an
  Error document of its `Reason` for a 400;
- otherwise, the status of the sentinel the error matches: 404 for
  `ErrNotFound` (and `ErrNoNode`), 403 for `ErrForbidden`, 401 for
  `ErrUnauthorized`, 400 for `ErrBadRequest` and `ErrInvalid`, 409 for
  `ErrConflict`, 503 for `ErrUnavailable`;
- 500 for anything else, logged at Error.

An `*HTTPError` matches the sentinel of its status, as a `StatusError` does.
