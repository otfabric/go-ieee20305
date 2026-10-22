// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"net/http"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Request is a request to a resource, as a hook sees it.
type Request struct {
	*http.Request
	// Principal is the client that sent it.
	Principal Principal
	// Node is the resource the request is for.
	Node *Node
	// Spec and Method describe the resource and the method in the WADL.
	Spec   ieee20305.ResourceSpec
	Method ieee20305.MethodSpec
	// Doc is the decoded document of a PUT or POST, which validated.
	Doc any
	// Query holds the paging and selection parameters of the request.
	Query Query
}

// Hooks are the handling of a resource beyond what the server does for any
// resource. A nil hook leaves the default. A hook that returns an
// *HTTPError answers with its status.
type Hooks struct {
	// Get returns the document of the resource, computed rather than
	// stored, such as Time.
	Get func(ctx context.Context, r *Request) (any, error)
	// Create handles a POST to the resource: by default, a POST to a list
	// creates an item. It returns the href of what it created (for the
	// Location header), or "" for 204.
	Create func(ctx context.Context, r *Request) (string, error)
	// Update handles a PUT: by default it replaces the document.
	Update func(ctx context.Context, r *Request) error
	// Delete handles a DELETE: by default it deletes the resource.
	Delete func(ctx context.Context, r *Request) error
}

// over returns h with the hooks it lacks taken from under.
func (h Hooks) over(under Hooks) Hooks {
	if h.Get == nil {
		h.Get = under.Get
	}
	if h.Create == nil {
		h.Create = under.Create
	}
	if h.Update == nil {
		h.Update = under.Update
	}
	if h.Delete == nil {
		h.Delete = under.Delete
	}
	return h
}

// HTTPError is an error with the HTTP status the server answers with, and
// the reasonCode of the Error document, if any.
type HTTPError struct {
	Status int
	// Reason is the reasonCode of the IEEE 2030.5 Error document the
	// server sends for a 400; see ReasonInvalidRequest and the others.
	Reason ieee20305.UInt16
	Err    error
}

// Reason codes of the IEEE 2030.5 Error document.
const (
	ReasonInvalidRequest ieee20305.UInt16 = 0
	ReasonUnsupported    ieee20305.UInt16 = 1
)

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, http.StatusText(e.Status), e.Err)
	}
	return fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status))
}

func (e *HTTPError) Unwrap() error { return e.Err }

// Is matches the sentinel of package ieee20305 for its status, as a
// StatusError of a client does: ErrNotFound for a 404, ErrServer for a
// 5xx, and so on.
func (e *HTTPError) Is(target error) bool {
	return (&ieee20305.StatusError{StatusCode: e.Status}).Is(target)
}

func httpError(status int, format string, a ...any) *HTTPError {
	return &HTTPError{Status: status, Err: fmt.Errorf(format, a...)}
}
