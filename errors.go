// SPDX-License-Identifier: MIT

package ieee20305

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Categories of failure reported by an IEEE 2030.5 server. An *Error
// matches the one for its HTTP status through errors.Is.
//
// Test for these with errors.Is, and for the types with errors.As; the
// text of an error is for people and may change.
var (
	ErrBadRequest   = errors.New("ieee20305: bad request")
	ErrUnauthorized = errors.New("ieee20305: unauthorized")
	ErrForbidden    = errors.New("ieee20305: forbidden")
	ErrNotFound     = errors.New("ieee20305: not found")
	// ErrConflict is a 409: the request conflicts with the state of the
	// resource.
	ErrConflict = errors.New("ieee20305: conflict")
	// ErrServer is a 5xx: the server failed. Retryable reports whether
	// sending the request again may succeed.
	ErrServer = errors.New("ieee20305: server error")
)

// Categories of failure that are not an answer from a server.
var (
	// ErrInvalid is matched by every *ValidationError: a resource or
	// a message does not satisfy the schema.
	ErrInvalid = errors.New("ieee20305: invalid")
	// ErrConfiguration is matched by what a constructor of this module
	// returns for a configuration it cannot work with.
	ErrConfiguration = errors.New("ieee20305: invalid configuration")
	// ErrTimeout is matched by a call that ran out of time: the
	// deadline of its context, the timeout of the HTTP client, or a
	// network timeout below them.
	ErrTimeout = errors.New("ieee20305: timeout")
	// ErrProtocol is matched by an answer that is not what the
	// protocol says it is: a body that cannot be decoded, one that is
	// too large, or a response that violates the XML schema. The peer
	// acted on the request.
	ErrProtocol = errors.New("ieee20305: protocol error")
	// ErrClosed is matched by the use of something after it was closed.
	ErrClosed = errors.New("ieee20305: closed")
	// ErrUnavailable is matched by a server that cannot be reached or does
	// not serve now: a refused or reset connection, a host name that does
	// not resolve, a 503 or a 429. Sending again later may succeed.
	ErrUnavailable = errors.New("ieee20305: unavailable")
	// ErrCanceled is matched by a call whose context was canceled.
	ErrCanceled = errors.New("ieee20305: canceled")
	// ErrCertificate is matched by a failure in certificate validation,
	// LFDI/SFDI derivation, or mutual TLS setup.
	ErrCertificate = errors.New("ieee20305: certificate error")
)

// StatusError is a non-success HTTP response from an IEEE 2030.5 server. The
// Error document a server may send in the body is the generated type Error.
type StatusError struct {
	// Method and Path identify the request.
	Method string
	Path   string
	// StatusCode is the HTTP status of the response.
	StatusCode int
	// Document is the Error document of the response body, when the
	// server sent one; its ReasonCode says why the request failed.
	Document *Error
	// RetryAfter is the delay the server asked for (the Retry-After header
	// of a 503 or 429), or zero.
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ieee20305: %s %s: %d", e.Method, e.Path, e.StatusCode)
	title := http.StatusText(e.StatusCode)
	if title != "" {
		b.WriteString(" " + title)
	}
	if e.Document != nil {
		fmt.Fprintf(&b, " (reason code %d)", e.Document.ReasonCode)
	}
	return b.String()
}

// Is matches the category sentinel for the response's status code.
func (e *StatusError) Is(target error) bool {
	switch e.StatusCode {
	case http.StatusBadRequest:
		return target == ErrBadRequest
	case http.StatusUnauthorized:
		return target == ErrUnauthorized
	case http.StatusForbidden:
		return target == ErrForbidden
	case http.StatusNotFound:
		return target == ErrNotFound
	case http.StatusConflict:
		return target == ErrConflict
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return target == ErrUnavailable || (e.StatusCode >= 500 && target == ErrServer)
	}
	return e.StatusCode >= 500 && target == ErrServer
}

// Retryable reports whether sending the request of err again may succeed:
// the server was unavailable, failed (5xx) or did not answer in time. It is
// false for an answer that sending again cannot change (another 4xx), a
// response that breaks the protocol, a certificate that does not verify, a
// configuration error, and a canceled call. The request must be one that is
// safe to send twice: see the RetryPolicy of the client.
func Retryable(err error) bool {
	switch {
	case err == nil, errors.Is(err, ErrCanceled), errors.Is(err, ErrCertificate),
		errors.Is(err, ErrConfiguration), errors.Is(err, ErrClosed), errors.Is(err, ErrProtocol):
		return false
	}
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrServer) || errors.Is(err, ErrTimeout)
}

// RetryAfter returns the delay a server asked for in the StatusError of
// err, or zero.
func RetryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

// ValidationError reports a field that violates the IEEE 2030.5 schema.
// It is returned by the Validate methods and by the wire codecs for
// malformed messages.
type ValidationError struct {
	// Field is the path of the property, such as
	// "DERControl.DERControlBase.opModExpLimW".
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return "ieee20305: invalid " + e.Field + ": " + e.Reason
}

// Is matches ErrInvalid.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }
