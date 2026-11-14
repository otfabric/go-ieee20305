// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// endsAbnormally is a function of the application that does not return
// normally.
func endsAbnormally() { panic("ended by the test") }

// failingMetrics does not return normally from the method named fail,
// once, and counts the calls of each method.
type failingMetrics struct {
	fail  string
	once  sync.Once
	calls map[string]int
}

func (m *failingMetrics) called(method string) {
	m.calls[method]++
	if m.fail == method {
		m.once.Do(endsAbnormally)
	}
}

func (m *failingMetrics) OnRequest(string, string)                      { m.called("OnRequest") }
func (m *failingMetrics) OnResponse(string, string, int, time.Duration) { m.called("OnResponse") }
func (m *failingMetrics) OnError(string, string, time.Duration, error)  { m.called("OnError") }
func (m *failingMetrics) OnRetry(string, string, int, error, time.Duration) {
	m.called("OnRetry")
}

// errorEntries is a Logger that keeps its Error entries.
type errorEntries []string

func (l *errorEntries) Debugf(string, ...any) {}
func (l *errorEntries) Infof(string, ...any)  {}
func (l *errorEntries) Warnf(string, ...any)  {}
func (l *errorEntries) Errorf(format string, args ...any) {
	*l = append(*l, fmt.Sprintf(format, args...))
}

// flaky is a transport that fails its first request with 503 and answers
// the others with the Time document.
type flaky struct{ requests int }

func (f *flaky) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests++
	if f.requests == 1 {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
	}
	return answers{doc: timeDocument}.RoundTrip(req)
}

const timeDocument = `<Time xmlns="urn:ieee:std:2030.5:ns" href="/tm"><currentTime>1</currentTime><dstEndTime>0</dstEndTime>` +
	`<dstOffset>0</dstOffset><dstStartTime>0</dstStartTime><quality>7</quality><tzOffset>0</tzOffset></Time>`

// TestMetricsDoNotReturnNormally: a Metrics method that does not return
// normally is stopped there and logged with its stack, and the call of the
// client goes on: it sends its request, retries it and returns its answer
// or its error as with a Metrics that returns.
func TestMetricsDoNotReturnNormally(t *testing.T) {
	for _, tt := range []struct {
		method string
		name   string
		fails  bool // whether the call of the client fails (a 503 without a retry)
	}{
		{"OnRequest", "Metrics.OnRequest", false},
		{"OnRetry", "AttemptMetrics.OnRetry", false},
		{"OnResponse", "Metrics.OnResponse", false},
		{"OnError", "Metrics.OnError", true},
	} {
		t.Run(tt.method, func(t *testing.T) {
			var log errorEntries
			m := &failingMetrics{fail: tt.method, calls: map[string]int{}}
			cfg := client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: &flaky{}},
				Metrics: m, Logger: &log}
			if !tt.fails {
				cfg.RetryPolicy = ieee20305.ExponentialBackoff{Initial: time.Nanosecond, Max: time.Nanosecond, Attempts: 2, NoJitter: true}
			}
			c, err := client.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var tm ieee20305.Time
			err = c.Get(context.Background(), "/tm", &tm)
			switch {
			case tt.fails && !errors.Is(err, ieee20305.ErrUnavailable):
				t.Fatalf("Get: %v, want ErrUnavailable", err)
			case !tt.fails && (err != nil || tm.CurrentTime != 1):
				t.Fatalf("Get: %v, time %d", err, tm.CurrentTime)
			}
			if m.calls[tt.method] != 1 || m.calls["OnRequest"] != 1 {
				t.Errorf("calls of the Metrics: %v", m.calls)
			}
			if len(log) != 1 || !strings.HasPrefix(log[0], tt.name+" did not return normally: ended by the test\n") ||
				!strings.Contains(log[0], "client_test.endsAbnormally") {
				t.Fatalf("Error entries %q, want one of %s with its stack", log, tt.name)
			}
			// The client goes on.
			if err := c.Get(context.Background(), "/tm", &tm); err != nil {
				t.Fatalf("Get again: %v", err)
			}
		})
	}
}

// brokenLogger is a Logger none of whose methods returns normally.
type brokenLogger struct{}

func (brokenLogger) Debugf(string, ...any) { endsAbnormally() }
func (brokenLogger) Infof(string, ...any)  { endsAbnormally() }
func (brokenLogger) Warnf(string, ...any)  { endsAbnormally() }
func (brokenLogger) Errorf(string, ...any) { endsAbnormally() }

// TestLoggerDoesNotReturnNormally: the entries of a Logger that does not
// return normally are lost, and the client works as without a Logger, also
// where it logs a retry and a Metrics method that does not return normally.
func TestLoggerDoesNotReturnNormally(t *testing.T) {
	m := &failingMetrics{fail: "OnResponse", calls: map[string]int{}}
	c, err := client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: &flaky{}},
		Metrics: m, Logger: brokenLogger{},
		RetryPolicy: ieee20305.ExponentialBackoff{Initial: time.Nanosecond, Max: time.Nanosecond, Attempts: 2, NoJitter: true}})
	if err != nil {
		t.Fatal(err)
	}
	var tm ieee20305.Time
	if err := c.Get(context.Background(), "/tm", &tm); err != nil || tm.CurrentTime != 1 {
		t.Fatalf("Get: %v, time %d", err, tm.CurrentTime)
	}
	if m.calls["OnRetry"] != 1 || m.calls["OnResponse"] != 1 {
		t.Errorf("calls of the Metrics: %v", m.calls)
	}
}
