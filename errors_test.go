// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func TestStatusError(t *testing.T) {
	for _, c := range []struct {
		status int
		is     error
	}{
		{http.StatusBadRequest, ieee20305.ErrBadRequest},
		{http.StatusUnauthorized, ieee20305.ErrUnauthorized},
		{http.StatusForbidden, ieee20305.ErrForbidden},
		{http.StatusNotFound, ieee20305.ErrNotFound},
	} {
		err := &ieee20305.StatusError{Method: http.MethodGet, Path: "/dcap", StatusCode: c.status}
		if !errors.Is(err, c.is) {
			t.Errorf("%d: errors.Is(%v) = false", c.status, c.is)
		}
		if errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("%d: matches ErrProtocol", c.status)
		}
	}
	err := &ieee20305.StatusError{Method: http.MethodPost, Path: "/edev", StatusCode: http.StatusConflict,
		Document: &ieee20305.Error{ReasonCode: 3}}
	if errors.Is(err, ieee20305.ErrBadRequest) {
		t.Error("409 matches ErrBadRequest")
	}
	if got := err.Error(); got != "ieee20305: POST /edev: 409 Conflict (reason code 3)" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&ieee20305.StatusError{Method: "GET", Path: "/x", StatusCode: 599}).Error(); got != "ieee20305: GET /x: 599" {
		t.Errorf("Error() of an unknown status = %q", got)
	}
}

func TestValidationError(t *testing.T) {
	err := &ieee20305.ValidationError{Field: "sFDI", Reason: "greater than 1"}
	if !errors.Is(err, ieee20305.ErrInvalid) {
		t.Error("ValidationError does not match ErrInvalid")
	}
	if got := err.Error(); got != "ieee20305: invalid sFDI: greater than 1" {
		t.Errorf("Error() = %q", got)
	}
}

func TestCodecErrors(t *testing.T) {
	if _, err := ieee20305.Marshal(struct{}{}); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Marshal of a non-document type = %v", err)
	}
	if err := ieee20305.Unmarshal([]byte("<x/>"), new(int)); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Unmarshal into a non-document type = %v", err)
	}
	var ed ieee20305.EndDevice
	if err := ieee20305.Unmarshal([]byte("<Time xmlns=\"urn:ieee:std:2030.5:ns\"/>"), &ed); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Unmarshal of another document = %v", err)
	}
	if _, err := ieee20305.Decode([]byte("not xml")); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Decode of garbage = %v", err)
	}
	ed.SFDI = 1
	out, err := ieee20305.Marshal(&ed)
	if err != nil || !strings.Contains(string(out), "<sFDI>1</sFDI>") {
		t.Fatalf("Marshal = %s, %v", out, err)
	}
	var back ieee20305.EndDevice
	if err := ieee20305.Unmarshal(out, &back); err != nil || back.SFDI != 1 {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
}

func TestLookupResource(t *testing.T) {
	if len(ieee20305.Resources) == 0 {
		t.Fatal("no resources")
	}
	for _, r := range ieee20305.Resources {
		got, ok := ieee20305.LookupResource(r.ID)
		if !ok || got.ID != r.ID || got.SamplePath != r.SamplePath {
			t.Errorf("LookupResource(%q) = %+v, %v", r.ID, got, ok)
		}
	}
	for _, id := range []string{"", "NoSuchResource", "zzz"} {
		if _, ok := ieee20305.LookupResource(id); ok {
			t.Errorf("LookupResource(%q) found a resource", id)
		}
	}
}

func TestStandardCodec(t *testing.T) {
	c := ieee20305.StandardCodec
	out, err := c.Marshal(&ieee20305.Time{CurrentTime: 7})
	if err != nil {
		t.Fatal(err)
	}
	var tm ieee20305.Time
	if err := c.Unmarshal(out, &tm); err != nil || tm.CurrentTime != 7 {
		t.Errorf("Unmarshal = %+v, %v", tm, err)
	}
	if v, err := c.Decode(out); err != nil {
		t.Errorf("Decode: %v", err)
	} else if _, ok := v.(*ieee20305.Time); !ok {
		t.Errorf("Decode = %T", v)
	}
}

func TestRetryable(t *testing.T) {
	status := func(code int) error {
		return &ieee20305.StatusError{Method: http.MethodGet, Path: "/x", StatusCode: code, RetryAfter: 3 * time.Second}
	}
	for _, c := range []struct {
		name  string
		err   error
		retry bool
		is    error
	}{
		{"500", status(500), true, ieee20305.ErrServer},
		{"502", status(502), true, ieee20305.ErrServer},
		{"503", status(503), true, ieee20305.ErrUnavailable},
		{"429", status(429), true, ieee20305.ErrUnavailable},
		{"409", status(409), false, ieee20305.ErrConflict},
		{"404", status(404), false, ieee20305.ErrNotFound},
		{"timeout", fmt.Errorf("%w: x", ieee20305.ErrTimeout), true, ieee20305.ErrTimeout},
		{"unavailable", fmt.Errorf("%w: x", ieee20305.ErrUnavailable), true, ieee20305.ErrUnavailable},
		{"canceled", fmt.Errorf("%w: %w", ieee20305.ErrCanceled, ieee20305.ErrTimeout), false, ieee20305.ErrCanceled},
		{"certificate", fmt.Errorf("%w: x", ieee20305.ErrCertificate), false, ieee20305.ErrCertificate},
		{"protocol", fmt.Errorf("%w: %w", ieee20305.ErrProtocol, ieee20305.ErrServer), false, ieee20305.ErrProtocol},
		{"configuration", ieee20305.ErrConfiguration, false, ieee20305.ErrConfiguration},
		{"closed", ieee20305.ErrClosed, false, ieee20305.ErrClosed},
		{"other", errors.New("x"), false, nil},
		{"nil", nil, false, nil},
	} {
		if got := ieee20305.Retryable(c.err); got != c.retry {
			t.Errorf("%s: Retryable = %v", c.name, got)
		}
		if c.is != nil && !errors.Is(c.err, c.is) {
			t.Errorf("%s: does not match %v", c.name, c.is)
		}
	}
	if ieee20305.RetryAfter(status(503)) != 3*time.Second || ieee20305.RetryAfter(errors.New("x")) != 0 {
		t.Error("RetryAfter")
	}
	if errors.Is(status(404), ieee20305.ErrServer) || errors.Is(status(500), ieee20305.ErrUnavailable) {
		t.Error("a status matches the wrong category")
	}
}
