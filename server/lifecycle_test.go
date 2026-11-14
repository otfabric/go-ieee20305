// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/internal/leakcheck"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/server"
)

// within is how long the tests give the server to do what it must do at
// once; a test that passes does not wait for it.
const within = 10 * time.Second

// returned fails t unless ch is closed within the time of the tests.
func returned(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(within):
		t.Fatalf("%s did not return within %v", what, within)
	}
}

// hooks is a Metrics whose methods call the functions set, if any.
type hooks struct {
	server.NopMetrics
	request, response, notification, cancelled, transition func()
	// delivery gets the error of each delivery of a notification.
	delivery func(err error)
}

func call(f func()) {
	if f != nil {
		f()
	}
}

func (m *hooks) OnRequest(string)                                           { call(m.request) }
func (m *hooks) OnResponse(string, string, int, time.Duration)              { call(m.response) }
func (m *hooks) OnSubscriptionCancelled(string, uint8)                      { call(m.cancelled) }
func (m *hooks) OnEventTransition(string, schedule.Status, schedule.Status) { call(m.transition) }

func (m *hooks) OnNotification(_ string, _ time.Duration, err error) {
	call(m.notification)
	if m.delivery != nil {
		m.delivery(err)
	}
}

// activeControl creates a DERProgram of every client with a DERControl
// that is active now, and returns the href of the control: the server
// reports its transition to an EventMetrics and writes its status.
func activeControl(t *testing.T, srv *server.Server) string {
	t.Helper()
	ctx := context.Background()
	a := srv.Admin()
	if _, err := a.Get(ctx, "/derp"); err != nil {
		if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
			t.Fatal(err)
		}
	}
	prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{}, "")
	if err != nil {
		t.Fatal(err)
	}
	c := &ieee20305.DERControl{}
	c.MRID = ieee20305.MRIDType{1}
	c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(a.Now().Unix()), Duration: 3600}
	href, err := a.Create(ctx, prog+"/derc", c, "")
	if err != nil {
		t.Fatal(err)
	}
	return href
}

// subscribed provisions the device dev of h, subscribes it to its
// FunctionSetAssignmentsList with notifications to uri, and returns the
// function that changes the list, which notifies.
func subscribed(t *testing.T, h *harness, uri string) (change func()) {
	t.Helper()
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", uri))); status != http.StatusCreated {
		t.Fatalf("POST of the subscription: %d %s", status, data)
	}
	return func() {
		t.Helper()
		if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestServerCloseTwice: Close may be called more than once, and from
// several goroutines at once; it returns nil each time, and the handler
// answers 503 afterwards (contract rows L1 and L2).
func TestServerCloseTwice(t *testing.T) {
	leakcheck.Check(t)
	snk := newSink(t, http.StatusCreated)
	h := newHarness(t, notifyConfig(snk), "dev")
	change := subscribed(t, h, snk.URL+"/n")
	activeControl(t, h.srv)
	change()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 16)
	for range 8 {
		wg.Go(func() {
			<-start
			errs <- h.srv.Close()
			errs <- h.srv.Close()
		})
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	returned(t, done, "Close from 8 goroutines")
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Close returned %v", err)
		}
	}
	if err := h.srv.Close(); err != nil {
		t.Errorf("Close again returned %v", err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/dcap", nil); status != http.StatusServiceUnavailable {
		t.Errorf("GET after Close: %d", status)
	}
}

// TestServerCloseFromMetrics: the Metrics methods the server calls on
// goroutines of its own may close it (contract row L10): for the
// transition of an event, for a notification, and for both at once.
func TestServerCloseFromMetrics(t *testing.T) {
	t.Run("event", func(t *testing.T) {
		leakcheck.Check(t)
		m := &hooks{}
		h := newHarness(t, server.Config{Metrics: m}, "dev")
		// The method closes the server once the test has made its control:
		// a closed server refuses what is still being stored.
		ready, closed := make(chan struct{}), make(chan struct{})
		m.transition = sync.OnceFunc(func() {
			<-ready
			_ = h.srv.Close()
			_ = h.srv.Close()
			close(closed)
		})
		activeControl(t, h.srv)
		close(ready)
		returned(t, closed, "Close called from EventMetrics.OnEventTransition")
		_ = h.srv.Close()
	})
	t.Run("notification", func(t *testing.T) {
		leakcheck.Check(t)
		snk := newSink(t, http.StatusCreated)
		m := &hooks{}
		cfg := notifyConfig(snk)
		cfg.Metrics = m
		h := newHarness(t, cfg, "dev")
		ready, closed := make(chan struct{}), make(chan struct{})
		m.notification = sync.OnceFunc(func() {
			<-ready
			_ = h.srv.Close()
			close(closed)
		})
		subscribed(t, h, snk.URL+"/n")()
		close(ready)
		returned(t, closed, "Close called from NotificationMetrics.OnNotification")
		_ = h.srv.Close()
	})
	t.Run("both at once", func(t *testing.T) {
		leakcheck.Check(t)
		snk := newSink(t, http.StatusCreated)
		m := &hooks{}
		cfg := notifyConfig(snk)
		cfg.Metrics = m
		h := newHarness(t, cfg, "dev")
		var entered, closed sync.WaitGroup
		entered.Add(2)
		closed.Add(2)
		both := make(chan struct{})
		closing := func() {
			entered.Done()
			<-both
			_ = h.srv.Close()
			closed.Done()
		}
		m.transition, m.notification = sync.OnceFunc(closing), sync.OnceFunc(closing)
		change := subscribed(t, h, snk.URL+"/n")
		activeControl(t, h.srv)
		change()
		entered.Wait()
		close(both)
		done := make(chan struct{})
		go func() { closed.Wait(); close(done) }()
		returned(t, done, "Close called from two Metrics methods at once")
		_ = h.srv.Close()
	})
}

// TestServerCloseWaitsForMetrics: a Close on another goroutine waits for a
// Metrics method the server is in, and returns when it has returned.
func TestServerCloseWaitsForMetrics(t *testing.T) {
	leakcheck.Check(t)
	m := &hooks{}
	h := newHarness(t, server.Config{Metrics: m}, "dev")
	entered, release := make(chan struct{}), make(chan struct{})
	m.transition = sync.OnceFunc(func() {
		close(entered)
		<-release
	})
	activeControl(t, h.srv)
	returned(t, entered, "EventMetrics was not called: it")
	closed := make(chan struct{})
	go func() {
		_ = h.srv.Close()
		close(closed)
	}()
	if !leakcheck.WaitBlocked("server.(*events).close(", within) {
		t.Fatal("Close did not come to wait for the Metrics method")
	}
	select {
	case <-closed:
		t.Fatal("Close returned while the Metrics method ran")
	default:
	}
	close(release)
	returned(t, closed, "Close, after the Metrics method returned,")
}

// entries is a Logger that keeps its Error entries.
type entries struct {
	mu     sync.Mutex
	errors []string
}

func (l *entries) Debugf(string, ...any) {}
func (l *entries) Infof(string, ...any)  {}
func (l *entries) Warnf(string, ...any)  {}
func (l *entries) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

// reported fails t unless l holds one Error entry, of the callback name
// with its value and a stack that names the function of the test.
func (l *entries) reported(t *testing.T, name string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.errors) != 1 || !strings.HasPrefix(l.errors[0], name+" did not return normally: ended by the test\n") ||
		!strings.Contains(l.errors[0], "server_test.endsAbnormally") {
		t.Fatalf("Error entries %q, want one of %s with its stack", l.errors, name)
	}
}

// endsAbnormally is a function of the application that does not return
// normally.
func endsAbnormally() { panic("ended by the test") }

// statusStore is a Store that tells of each write of an EventStatus that
// says active, and of each subscription it deletes.
type statusStore struct {
	server.Store
	active, cancelled chan string
}

func (s *statusStore) Delete(ctx context.Context, href string) error {
	err := s.Store.Delete(ctx, href)
	if err == nil && strings.Contains(href, "/sub/") {
		s.cancelled <- href
	}
	return err
}

func (s *statusStore) Put(ctx context.Context, n *server.Node) error {
	err := s.Store.Put(ctx, n)
	if c, ok := n.Value.(*ieee20305.DERControl); ok && err == nil && c.EventStatus.CurrentStatus == 1 {
		s.active <- n.Href
	}
	return err
}

// receiver takes notifications: it answers with its status, and tells of
// each one it got, once it has chosen the answer.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	got    chan struct{}
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{status: http.StatusCreated, got: make(chan struct{}, 16)}
	r.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		r.mu.Lock()
		status := r.status
		r.mu.Unlock()
		r.got <- struct{}{}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.Close)
	return r
}

// answer makes r answer the notifications to come with status.
func (r *receiver) answer(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

// await returns once r got a notification.
func (r *receiver) await(t *testing.T) {
	t.Helper()
	select {
	case <-r.got:
	case <-time.After(within):
		t.Fatal("no notification was sent")
	}
}

// TestServerMetricsDoNotReturnNormally: a Metrics method that does not
// return normally is stopped there and logged with its stack; the request
// is answered, the notification delivered and the status of the event
// written all the same, and the server goes on.
func TestServerMetricsDoNotReturnNormally(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(m *hooks, f func())
	}{
		{"Metrics.OnRequest", func(m *hooks, f func()) { m.request = f }},
		{"Metrics.OnResponse", func(m *hooks, f func()) { m.response = f }},
		{"EventMetrics.OnEventTransition", func(m *hooks, f func()) { m.transition = f }},
		{"NotificationMetrics.OnNotification", func(m *hooks, f func()) { m.notification = f }},
		{"NotificationMetrics.OnSubscriptionCancelled", func(m *hooks, f func()) { m.cancelled = f }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leakcheck.Check(t)
			log := &entries{}
			m := &hooks{}
			rcv := newReceiver(t)
			store := &statusStore{Store: server.NewMemStore(nil), active: make(chan string, 4), cancelled: make(chan string, 4)}
			h := newHarness(t, server.Config{Metrics: m, Logger: log, Store: store,
				NotificationClient: rcv.Client(), NotificationURIAllowed: allowAll,
				NotificationRetry: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 1, NoJitter: true}}, "dev")
			change := subscribed(t, h, rcv.URL+"/n")
			// The method fails once, at its first call from here on.
			var once sync.Once
			tt.set(m, func() { once.Do(endsAbnormally) })
			// A request.
			if status, _, data := h.do(t, "dev", http.MethodGet, "/dcap", nil); status != http.StatusOK {
				t.Fatalf("GET: %d %s", status, data)
			}
			// An event.
			control := activeControl(t, h.srv)
			select {
			case href := <-store.active:
				if href != control {
					t.Fatalf("the status of %s was written, want %s", href, control)
				}
			case <-time.After(within):
				t.Fatal("the status of the event was not written")
			}
			// A notification, and one the receiver refuses, which cancels
			// the subscription: the server deletes it once it has told the
			// Metrics of the delivery and of the cancellation.
			change()
			rcv.await(t)
			rcv.answer(http.StatusGone)
			change()
			select {
			case <-store.cancelled:
			case <-time.After(within):
				t.Fatal("the subscription was not cancelled")
			}
			// The server goes on.
			if status, _, data := h.do(t, "dev", http.MethodGet, "/tm", nil); status != http.StatusOK {
				t.Fatalf("GET afterwards: %d %s", status, data)
			}
			// Close waits for the goroutines that call the Metrics.
			_ = h.srv.Close()
			log.reported(t, tt.name)
		})
	}
}

// brokenLogger is a Logger none of whose methods returns normally.
type brokenLogger struct{}

func (brokenLogger) Debugf(string, ...any) { endsAbnormally() }
func (brokenLogger) Infof(string, ...any)  { endsAbnormally() }
func (brokenLogger) Warnf(string, ...any)  { endsAbnormally() }
func (brokenLogger) Errorf(string, ...any) { endsAbnormally() }

// TestServerLoggerDoesNotReturnNormally: the entries of a Logger that does
// not return normally are lost, and the server works as without a Logger:
// it provisions a device, answers and refuses requests, and fails one.
func TestServerLoggerDoesNotReturnNormally(t *testing.T) {
	leakcheck.Check(t)
	h := newHarness(t, server.Config{Logger: brokenLogger{}, Hooks: map[string]server.Hooks{
		"Time": {Get: func(context.Context, *server.Request) (any, error) {
			return nil, &server.HTTPError{Status: http.StatusBadGateway}
		}},
	}}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{ed: http.StatusOK, "/none": http.StatusNotFound, "/tm": http.StatusBadGateway} {
		if status, _, _ := h.do(t, "dev", http.MethodGet, path, nil); status != want {
			t.Errorf("GET %s: %d, want %d", path, status, want)
		}
	}
}
