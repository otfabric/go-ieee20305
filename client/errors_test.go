// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// The failures of a request are classified by the sentinels of package
// ieee20305, and keep the error they come from.
func TestTransportErrors(t *testing.T) {
	p := newPKI(t)
	ctx := context.Background()

	// Nothing listens: unavailable, and retryable.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c, err := client.New(client.Config{Server: "https://" + addr, HTTPClient: client.NewHTTPClient(p.device, p.roots)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.DeviceCapability(ctx)
	var opErr *net.OpError
	if !errors.Is(err, ieee20305.ErrUnavailable) || !ieee20305.Retryable(err) || !errors.As(err, &opErr) {
		t.Errorf("refused: %v", err)
	}

	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			time.Sleep(200 * time.Millisecond)
		case "/busy":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/later":
			w.Header().Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		case "/bad":
			w.Header().Set("Content-Type", client.MediaType)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error xmlns="urn:ieee:std:2030.5:ns"><reasonCode>1</reasonCode></Error>`))
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	})
	c = newClient(t, p, srv, client.Config{})

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Get(canceled, "/slow", &ieee20305.Time{}); !errors.Is(err, ieee20305.ErrCanceled) || ieee20305.Retryable(err) {
		t.Errorf("canceled: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := c.Get(short, "/slow", &ieee20305.Time{}); !errors.Is(err, ieee20305.ErrTimeout) || !ieee20305.Retryable(err) {
		t.Errorf("deadline: %v", err)
	}
	err = c.Get(ctx, "/busy", &ieee20305.Time{})
	if !errors.Is(err, ieee20305.ErrUnavailable) || ieee20305.RetryAfter(err) != 7*time.Second {
		t.Errorf("503: %v, RetryAfter %v", err, ieee20305.RetryAfter(err))
	}
	if d := ieee20305.RetryAfter(c.Get(ctx, "/later", &ieee20305.Time{})); d < 59*time.Minute || d > time.Hour {
		t.Errorf("429 with a date: RetryAfter %v", d)
	}
	err = c.Get(ctx, "/bad", &ieee20305.Time{})
	var se *ieee20305.StatusError
	if !errors.As(err, &se) || se.Document == nil || se.Document.ReasonCode != 1 || ieee20305.Retryable(err) {
		t.Errorf("400: %v", err)
	}
	if err := c.Get(ctx, "/loop", &ieee20305.Time{}); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("redirect loop: %v", err)
	}

	// A server of another CA: a certificate error, not retryable.
	other := newPKI(t)
	c, err = client.New(client.Config{Server: srv.URL, HTTPClient: client.NewHTTPClient(p.device, other.roots)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.DeviceCapability(ctx)
	if !errors.Is(err, ieee20305.ErrCertificate) || ieee20305.Retryable(err) {
		t.Errorf("unknown CA: %v", err)
	}
	// And a device the server does not trust.
	c, err = client.New(client.Config{Server: srv.URL, HTTPClient: client.NewHTTPClient(other.device, p.roots)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.DeviceCapability(ctx)
	if !errors.Is(err, ieee20305.ErrCertificate) || ieee20305.Retryable(err) {
		t.Errorf("an untrusted device: %v", err)
	}
}
