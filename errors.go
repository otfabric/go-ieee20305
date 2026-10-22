// SPDX-License-Identifier: MIT

package ieee20305

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	// ErrCertificate is matched by a failure in certificate validation,
	// LFDI/SFDI derivation, or mutual TLS setup.
	ErrCertificate = errors.New("ieee20305: certificate error")
	// ErrSchedule is matched by a conflict in event scheduling: an
	// overlap that cannot be resolved, a primacy conflict, or a
	// randomization that falls outside allowed bounds.
	ErrSchedule = errors.New("ieee20305: schedule conflict")
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
	}
	return false
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
