// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/leakcheck"
	"github.com/otfabric/go-ieee20305/schedule"
)

// within is how long the tests give the agent to do what it must do at
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

// startRun runs a on its own goroutine and returns the channel of the
// error Run returns.
func startRun(a *Agent, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	return done
}

// runError returns what Run returned, and fails t if it has not returned
// within the time of the tests.
func runError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Run did not return within %v", within)
		return nil
	}
}

// TestCloseEndsRun: Close ends a Run that waits for its next poll, waits to
// start again, or waits for the server's answer (contract rows L3 and
// L16). Run returns ErrClosed, the agent sends nothing more, and none of
// its goroutines is left.
func TestCloseEndsRun(t *testing.T) {
	for _, tt := range []struct {
		name string
		// arrange sets f up, and returns the function that returns once
		// Run waits, and one to call when Run has returned.
		arrange func(t *testing.T, f *fakeServer) (waiting func(), after func())
	}{
		{"waiting for the next poll", func(t *testing.T, f *fakeServer) (func(), func()) {
			return func() {
				if !leakcheck.WaitBlocked("agent.(*Agent).sleepOrWake(", within) {
					t.Fatal("Run did not come to wait for its next poll")
				}
			}, func() {}
		}},
		{"waiting to start again", func(t *testing.T, f *fakeServer) (func(), func()) {
			f.failWith("GET /dcap", 503)
			return func() {
				if !leakcheck.WaitBlocked("agent.sleep(", within) {
					t.Fatal("Run did not come to wait before it starts again")
				}
			}, func() {}
		}},
		{"waiting for an answer", func(t *testing.T, f *fakeServer) (func(), func()) {
			release := f.holdBack("GET /dcap")
			t.Cleanup(release) // before the server closes, which waits for the request
			return func() { f.await(t, "GET /dcap") }, release
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leakcheck.Check(t)
			f := newFake(t)
			waiting, after := tt.arrange(t, f)
			a := newTestAgent(t, f, Config{Backoff: ieee20305.ExponentialBackoff{Initial: time.Hour, Max: time.Hour}})
			done := startRun(a, context.Background())
			waiting()
			a.Close()
			err := runError(t, done)
			after()
			if !errors.Is(err, ieee20305.ErrClosed) {
				t.Fatalf("Run after Close returned %v, want ErrClosed", err)
			}
			sent := f.total()
			if err := a.Start(context.Background()); !errors.Is(err, ieee20305.ErrClosed) {
				t.Fatalf("Start after Close: %v", err)
			}
			if err := a.Run(context.Background()); !errors.Is(err, ieee20305.ErrClosed) {
				t.Fatalf("Run after Close: %v", err)
			}
			if n := f.total(); n != sent {
				t.Fatalf("%d requests after Close", n-sent)
			}
		})
	}
}

// TestRunEndsWithContext: the end of its context still ends Run with the
// context's error, also when the agent is closed afterwards.
func TestRunEndsWithContext(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	a := newTestAgent(t, f, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(a, ctx)
	if !leakcheck.WaitBlocked("agent.(*Agent).sleepOrWake(", within) {
		t.Fatal("Run did not come to wait for its next poll")
	}
	cancel()
	if err := runError(t, done); !errors.Is(err, context.Canceled) || errors.Is(err, ieee20305.ErrClosed) {
		t.Fatalf("Run returned %v, want the context's error", err)
	}
}

// TestCloseTwice: Close may be called more than once, and from several
// goroutines at once, before, during and after Run (contract rows L1 and
// L2).
func TestCloseTwice(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%t", running), func(t *testing.T) {
			leakcheck.Check(t)
			f := newFake(t)
			a := newTestAgent(t, f, Config{})
			var done <-chan error
			if running {
				done = startRun(a, context.Background())
				if !leakcheck.WaitBlocked("agent.(*Agent).sleepOrWake(", within) {
					t.Fatal("Run did not come to wait for its next poll")
				}
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			for range 8 {
				wg.Go(func() {
					<-start
					a.Close()
					a.Close()
				})
			}
			close(start)
			closed := make(chan struct{})
			go func() { wg.Wait(); close(closed) }()
			returned(t, closed, "Close from 8 goroutines")
			if running {
				if err := runError(t, done); !errors.Is(err, ieee20305.ErrClosed) {
					t.Fatalf("Run returned %v, want ErrClosed", err)
				}
			}
			a.Close()
			if evs := a.Events(DER); len(evs) != 0 {
				t.Fatalf("events after Close: %v", evs)
			}
		})
	}
}

// frResponses is the FlowReservationResponseList of the device of the fake
// server, with one response per mRID, starting in an hour.
func frResponses(mrids ...string) string {
	s := fmt.Sprintf(`<FlowReservationResponseList `+ns+` href="/edev/1/frp" all="%d" results="%d">`, len(mrids), len(mrids))
	for _, mrid := range mrids {
		s += fmt.Sprintf(`<FlowReservationResponse href="/edev/1/frp/%s"><mRID>%s</mRID><creationTime>0</creationTime>
			<EventStatus><currentStatus>0</currentStatus><dateTime>0</dateTime><potentiallySuperseded>false</potentiallySuperseded></EventStatus>
			<interval><duration>3600</duration><start>%d</start></interval>
			<energyAvailable><multiplier>0</multiplier><value>1</value></energyAvailable>
			<powerAvailable><multiplier>0</multiplier><value>1</value></powerAvailable>
			<subject>%s</subject></FlowReservationResponse>`, mrid, mrid, time.Now().Add(time.Hour).Unix(), mrid)
	}
	return s + `</FlowReservationResponseList>`
}

// withFlowReservations gives the device of f a FlowReservationResponseList
// with the responses mrids.
func withFlowReservations(f *fakeServer, mrids ...string) {
	f.setEndDevice(`<RegistrationLink href="/edev/1/rg"/><FunctionSetAssignmentsListLink href="/edev/1/fsa" all="1"/>
		<FlowReservationResponseListLink href="/edev/1/frp" all="1"/>`)
	f.set("/edev/1/frp", frResponses(mrids...))
}

// TestCloseFromHandler: a handler of events, and a Metrics method called
// for an event, may close the agent (contract row L10). That Close returns
// while its handler runs, the loops end as the handlers return, and a
// Close on another goroutine then returns too.
func TestCloseFromHandler(t *testing.T) {
	t.Run("handler", func(t *testing.T) {
		leakcheck.Check(t)
		f := newFake(t)
		f.set("/derp/1/derc", derControls(derControl("B1", "")))
		var a *Agent
		closed := make(chan struct{})
		a = newTestAgent(t, f, Config{OnDERControl: func(context.Context, schedule.Transition) error {
			a.Close()
			a.Close()
			close(closed)
			return nil
		}})
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		returned(t, closed, "Close called from a handler")
		a.Close()
	})
	t.Run("metrics", func(t *testing.T) {
		leakcheck.Check(t)
		f := newFake(t)
		f.set("/derp/1/derc", derControls(derControl("B1", "")))
		m := &closingMetrics{closed: make(chan struct{})}
		a := newTestAgent(t, f, Config{Metrics: m})
		m.a = a
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		returned(t, m.closed, "Close called from Metrics.OnEvent")
		a.Close()
	})
	// The handlers of two function sets close the agent at once: neither
	// waits for the other.
	t.Run("two handlers", func(t *testing.T) {
		leakcheck.Check(t)
		f := newFake(t)
		f.set("/derp/1/derc", derControls(derControl("B1", "")))
		withFlowReservations(f, "C1")
		var a *Agent
		var entered, closed sync.WaitGroup
		entered.Add(2)
		closed.Add(2)
		both := make(chan struct{})
		closing := func(context.Context, schedule.Transition) error {
			entered.Done()
			<-both
			a.Close()
			closed.Done()
			return nil
		}
		a = newTestAgent(t, f, Config{OnDERControl: closing, OnFlowReservation: closing})
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		entered.Wait()
		close(both)
		done := make(chan struct{})
		go func() { closed.Wait(); close(done) }()
		returned(t, done, "Close called from two handlers at once")
		a.Close()
	})
}

// closingMetrics closes the agent from OnEvent.
type closingMetrics struct {
	NopMetrics
	a      *Agent
	once   sync.Once
	closed chan struct{}
}

func (m *closingMetrics) OnEvent(FunctionSet, schedule.Transition) {
	m.once.Do(func() {
		m.a.Close()
		close(m.closed)
	})
}

// TestCloseWaitsForHandler: a Close on another goroutine waits for a
// handler in progress, and returns when it has returned.
func TestCloseWaitsForHandler(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	f.set("/derp/1/derc", derControls(derControl("B1", "")))
	entered, release := make(chan struct{}), make(chan struct{})
	a := newTestAgent(t, f, Config{OnDERControl: func(context.Context, schedule.Transition) error {
		close(entered)
		<-release
		return nil
	}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	returned(t, entered, "the handler was not called: it")
	closed := make(chan struct{})
	go func() {
		a.Close()
		close(closed)
	}()
	if !leakcheck.WaitBlocked("agent.(*Agent).closeLoops(", within) {
		t.Fatal("Close did not come to wait for the handler")
	}
	select {
	case <-closed:
		t.Fatal("Close returned while the handler ran")
	default:
	}
	close(release)
	returned(t, closed, "Close, after the handler returned,")
}

// entries is a Logger that keeps its Warn and Error entries.
type entries struct {
	mu            sync.Mutex
	warns, errors []string
}

func (l *entries) Debugf(string, ...any) {}
func (l *entries) Infof(string, ...any)  {}
func (l *entries) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *entries) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

// reported fails t unless l holds one Error entry, of the callback name
// with its value and a stack that names the test's function.
func (l *entries) reported(t *testing.T, name string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.errors) != 1 || !strings.HasPrefix(l.errors[0], name+" did not return normally: ended by the test\n") ||
		!strings.Contains(l.errors[0], "agent.endsAbnormally") {
		t.Fatalf("Error entries %q, want one of %s with its stack", l.errors, name)
	}
}

// endsAbnormally is a function of the application that does not return
// normally.
func endsAbnormally() { panic("ended by the test") }

// TestHandlerDoesNotReturnNormally: a handler of events that does not
// return normally is stopped there and logged with its stack; the agent
// posts the Response of that transition and goes on with the next.
func TestHandlerDoesNotReturnNormally(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	f.set("/derp/1/derc", derControls(derControl("B1", `replyTo="/rsp" responseRequired="01"`), derControl("B2", "")))
	log := &entries{}
	got := make(chan string, 4)
	a := newTestAgent(t, f, Config{Logger: log, OnDERControl: func(_ context.Context, tr schedule.Transition) error {
		if tr.Event.MRID == "B1" {
			endsAbnormally()
		}
		got <- tr.Event.MRID
		return nil
	}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case mrid := <-got:
		if mrid != "B2" {
			t.Fatalf("the handler got %s after B1, want B2", mrid)
		}
	case <-time.After(within):
		t.Fatal("the handler was not called again")
	}
	f.await(t, "POST /rsp")
	log.reported(t, "Config.OnDERControl")
	// The agent goes on: the next poll schedules a new event.
	f.set("/derp/1/derc", derControls(derControl("B1", ""), derControl("B2", ""), derControl("B3", "")))
	if err := a.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case mrid := <-got:
		if mrid != "B3" {
			t.Fatalf("the handler got %s, want B3", mrid)
		}
	case <-time.After(within):
		t.Fatal("the handler was not called after the next poll")
	}
}

// failingMetrics does not return normally from the method named fail,
// once.
type failingMetrics struct {
	NopMetrics
	fail string
	once sync.Once
}

func (m *failingMetrics) OnPoll(time.Duration, error) {
	if m.fail == "OnPoll" {
		m.once.Do(endsAbnormally)
	}
}

func (m *failingMetrics) OnEvent(FunctionSet, schedule.Transition) {
	if m.fail == "OnEvent" {
		m.once.Do(endsAbnormally)
	}
}

func (m *failingMetrics) OnResponse(FunctionSet, uint8, error) {
	if m.fail == "OnResponse" {
		m.once.Do(endsAbnormally)
	}
}

// TestMetricsDoNotReturnNormally: a Metrics method that does not return
// normally is stopped there and logged; the handler is called and the
// Response posted all the same, and Run goes on polling.
func TestMetricsDoNotReturnNormally(t *testing.T) {
	for _, method := range []string{"OnEvent", "OnResponse", "OnPoll"} {
		t.Run(method, func(t *testing.T) {
			leakcheck.Check(t)
			f := newFake(t)
			f.set("/derp/1/derc", derControls(derControl("B1", `replyTo="/rsp" responseRequired="01"`)))
			log := &entries{}
			handled := make(chan struct{}, 8)
			a := newTestAgent(t, f, Config{Logger: log, Metrics: &failingMetrics{fail: method}, PollRate: time.Millisecond,
				OnDERControl: func(context.Context, schedule.Transition) error {
					handled <- struct{}{}
					return nil
				}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := startRun(a, ctx)
			select {
			case <-handled:
			case <-time.After(within):
				t.Fatal("the handler was not called")
			}
			f.await(t, "POST /rsp")
			// Run polls on: three more reads of the assignments.
			for range 3 {
				f.await(t, "GET /edev/1/fsa")
			}
			cancel()
			if err := runError(t, done); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run returned %v", err)
			}
			log.reported(t, "Metrics."+method)
		})
	}
}

// TestPollCallbacksDoNotReturnNormally: OnAssignments, OnFile and
// OnDefaultDERControl that do not return normally fail the poll with an
// error that names them, as when they return an error, and are logged.
func TestPollCallbacksDoNotReturnNormally(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  func(fail func()) Config
	}{
		{"Config.OnAssignments", func(fail func()) Config {
			return Config{OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error { fail(); return nil }}
		}},
		{"Config.OnFile", func(fail func()) Config {
			return Config{OnFile: func(context.Context, *ieee20305.File) error { fail(); return nil }}
		}},
		{"Config.OnDefaultDERControl", func(fail func()) Config {
			return Config{OnDefaultDERControl: func(context.Context, any) error { fail(); return nil }}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leakcheck.Check(t)
			f := newFake(t)
			f.set("/file", fileList("/file/1"))
			f.set("/derp/1/dderc", `<DefaultDERControl `+ns+` href="/derp/1/dderc"><mRID>D1</mRID><DERControlBase/></DefaultDERControl>`)
			log := &entries{}
			var once sync.Once
			cfg := tt.cfg(func() { once.Do(endsAbnormally) })
			cfg.Logger = log
			a := newTestAgent(t, f, cfg)
			err := a.Start(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.name+" did not return normally: ended by the test") {
				t.Fatalf("Start returned %v, want the error of %s", err, tt.name)
			}
			log.reported(t, tt.name)
			if err := a.Start(context.Background()); err != nil {
				t.Fatalf("Start again: %v", err)
			}
		})
	}
}

// TestReadingsDoNotReturnNormally: a Readings that does not return normally
// is logged, and the metering goes on at the next interval.
func TestReadingsDoNotReturnNormally(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	f.location("POST /mup", "/mup/1")
	log := &entries{}
	var once sync.Once
	a := newTestAgent(t, f, Config{
		Logger:           log,
		MirrorUsagePoint: &ieee20305.MirrorUsagePoint{},
		PostRate:         time.Millisecond,
		Readings: func(context.Context) ([]ieee20305.MirrorMeterReading, error) {
			once.Do(endsAbnormally)
			return readings(1), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(a, ctx)
	f.await(t, "POST /mup/1")
	cancel()
	if err := runError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	log.reported(t, "Config.Readings")
}

// brokenLogger is a Logger none of whose methods returns normally.
type brokenLogger struct{}

func (brokenLogger) Debugf(string, ...any) { endsAbnormally() }
func (brokenLogger) Infof(string, ...any)  { endsAbnormally() }
func (brokenLogger) Warnf(string, ...any)  { endsAbnormally() }
func (brokenLogger) Errorf(string, ...any) { endsAbnormally() }

// TestLoggerDoesNotReturnNormally: the entries of a Logger that does not
// return normally are lost, and the agent works as without a Logger, also
// where it logs a handler that fails or does not return normally.
func TestLoggerDoesNotReturnNormally(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	f.set("/derp/1/derc", derControls(derControl("B1", ""), derControl("B2", ""), derControl("B3", "")))
	got := make(chan string, 4)
	a := newTestAgent(t, f, Config{Logger: brokenLogger{}, PollRate: time.Millisecond,
		OnDERControl: func(_ context.Context, tr schedule.Transition) error {
			switch tr.Event.MRID {
			case "B1":
				return errors.New("the handler fails")
			case "B2":
				endsAbnormally()
			}
			got <- tr.Event.MRID
			return nil
		}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Run logs that it started, and each poll that fails.
	f.failTimes("GET /edev/1/fsa", 503, 2)
	done := startRun(a, ctx)
	select {
	case mrid := <-got:
		if mrid != "B3" {
			t.Fatalf("the handler got %s, want B3", mrid)
		}
	case <-time.After(within):
		t.Fatal("the handler was not called for B3")
	}
	cancel()
	if err := runError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestHandlerCallsAgent: a handler of events may call the methods of the
// agent that read its state (contract row L9).
func TestHandlerCallsAgent(t *testing.T) {
	leakcheck.Check(t)
	f := newFake(t)
	f.set("/derp/1/derc", derControls(derControl("B1", "")))
	var a *Agent
	type seen struct {
		events, active int
		started        bool
		stats          schedule.Stats
	}
	got := make(chan seen, 1)
	a = newTestAgent(t, f, Config{OnDERControl: func(context.Context, schedule.Transition) error {
		got <- seen{events: len(a.Events(DER)), active: len(a.Active(DER)) + len(a.ActiveDERControls()),
			started: a.State().EndDevice != nil, stats: a.EventStats(DER)}
		return nil
	}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s.events != 1 || s.active != 0 || !s.started || s.stats != (schedule.Stats{}) {
			t.Fatalf("the handler saw %+v", s)
		}
	case <-time.After(within):
		t.Fatal("the handler did not return from the methods of the agent")
	}
}

// TestSlowHandler: a handler that does not return holds back the events of
// its function set only. The polls go on, the transitions that wait for it
// stay within their bound, and what the bound leaves out is counted
// (contract rows L11 and L12).
func TestSlowHandler(t *testing.T) {
	leakcheck.Check(t)
	ctx := context.Background()
	f := newFake(t)
	withFlowReservations(f, "C001")
	listControls(f, "B000", "B001")
	entered, release := make(chan struct{}), make(chan struct{})
	reservations := make(chan string, 16)
	var once sync.Once
	// MaxEvents 2: at most 8 transitions wait.
	a := newTestAgent(t, f, Config{MaxEvents: 2,
		OnDERControl: func(context.Context, schedule.Transition) error {
			once.Do(func() { close(entered) })
			<-release
			return nil
		},
		OnFlowReservation: func(_ context.Context, tr schedule.Transition) error {
			reservations <- tr.Event.MRID
			return nil
		}})
	defer close(release)
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	returned(t, entered, "the handler was not called: it")
	// Each poll replaces the two controls: four transitions and more wait
	// for the handler, of events that come and go.
	for i := 1; i <= 20; i++ {
		listControls(f, fmt.Sprintf("B%02d0", i), fmt.Sprintf("B%02d1", i))
		withFlowReservations(f, "C001", fmt.Sprintf("C%03d", i+1))
		if err := a.poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		// The other function set is not held back.
		for {
			var mrid string
			select {
			case mrid = <-reservations:
			case <-time.After(within):
				t.Fatalf("poll %d: the handler of the flow reservations was not called", i)
			}
			if mrid == fmt.Sprintf("C%03d", i+1) {
				break
			}
		}
	}
	if evs := a.Events(DER); len(evs) != 2 {
		t.Fatalf("%d DER events, want 2", len(evs))
	}
	st := a.EventStats(DER)
	if st.Coalesced+st.Dropped == 0 || st.Refused != 0 {
		t.Fatalf("after 20 polls with a handler that does not return: %+v", st)
	}
	if other := a.EventStats(FlowReservation); other.Coalesced+other.Dropped != 0 {
		t.Fatalf("the flow reservations: %+v", other)
	}
}
