// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/internal/callback"
	"github.com/otfabric/go-ieee20305/schedule"
)

// ErrNotStarted is matched by a call that needs what Start reads from the
// server (the EndDevice, DeviceCapability) before the agent has it.
var ErrNotStarted = errors.New("agent: not started")

// Config configures an Agent.
//
// The functions of the application in it are called on these goroutines:
// OnAssignments, OnFile and OnDefaultDERControl on that of Start or Run,
// during a poll; the handler of the events of a function set on a goroutine
// of the agent, one per function set, one transition at a time; Readings on
// another goroutine of Run. The agent waits for each: the poll for those it
// calls during a poll, the next transition and the Responses of a function
// set for its handler. Those of a poll, and Readings, get the context of
// Start or of Run, which also ends when the agent is closed; a handler of
// events gets one that ends when the agent is closed. They may call the
// methods of the Agent, Close included (see Close). One that does not
// return normally is stopped there: the agent logs that at Error level
// with the stack, and goes on as if the function had returned an error.
type Config struct {
	// Client talks to the server, with the certificate of the device.
	// Required.
	Client *client.Client
	// Identity is the identity of the device, from its certificate: see
	// client.IdentityOf. Required.
	Identity client.Identity
	// PIN is the PIN the device was given out of band; with VerifyPIN, the
	// agent refuses a server whose Registration has another (fatal for
	// Run).
	PIN       uint32
	VerifyPIN bool
	// DeviceInformation, when set, is put to the device's
	// DeviceInformation once it is registered.
	DeviceInformation *ieee20305.DeviceInformation
	// PollRate overrides the pollRate of the server's resources.
	PollRate time.Duration
	// Backoff spaces the attempts after failures (to start, to poll, to
	// mirror metering): its Delay doubles from Initial (1 s when zero) up to
	// Max (5 min when zero), with jitter. Its Attempts is not used: the
	// agent keeps trying, except after errors that waiting does not fix
	// (see Run).
	Backoff ieee20305.ExponentialBackoff
	// OnAssignments is called with the device's FunctionSetAssignments each
	// time the agent reads them. Its error fails the poll, which Run
	// tries again after Backoff.
	OnAssignments func(ctx context.Context, fsas []*ieee20305.FunctionSetAssignments) error
	// NotificationURI makes the agent subscribe to the lists it polls (its
	// FunctionSetAssignments, programs and events): the https URL of its
	// NotificationHandler, which the application serves over TLS. The
	// agent polls as soon as a notification comes, and while subscribed
	// otherwise every SubscribedPollRate (4 times the poll rate when
	// zero). A server that takes no subscriptions is polled as usual.
	NotificationURI    string
	SubscribedPollRate time.Duration
	// OnDERControl is called with every transition of the DERControls of
	// the device's DERPrograms: their receipt, start, end, cancellation
	// and supersession, as a schedule.Scheduler with randomization and
	// DER conflicts works them out. The agent posts the Responses the
	// controls ask for, also when the handler returns an error, which it
	// logs.
	OnDERControl func(ctx context.Context, t schedule.Transition) error
	// OnEndDeviceControl, OnTextMessage, OnTimeTariffInterval and
	// OnSupplyInterruptionOverride are called with the transitions of the
	// events of DRLC, messaging, pricing and prepayment, as OnDERControl
	// is for DER: EndDeviceControls conflict by deviceCategory, the
	// TimeTariffIntervals of a RateComponent with each other, and
	// TextMessages and SupplyInterruptionOverrides never.
	OnEndDeviceControl           func(ctx context.Context, t schedule.Transition) error
	OnTextMessage                func(ctx context.Context, t schedule.Transition) error
	OnTimeTariffInterval         func(ctx context.Context, t schedule.Transition) error
	OnSupplyInterruptionOverride func(ctx context.Context, t schedule.Transition) error
	// OnFlowReservation is called with the transitions of the
	// FlowReservationResponses to the device's requests.
	OnFlowReservation func(ctx context.Context, t schedule.Transition) error
	// OnFile is called with each File of the device's FSAs the agent has
	// not seen (by href and mfVer); the device loads it and reports with
	// PutFileStatus.
	OnFile func(ctx context.Context, f *ieee20305.File) error
	// MirrorUsagePoint and Readings make the agent mirror metering: it
	// creates the MirrorUsagePoint and posts what Readings returns at
	// PostRate (the postRate of the MirrorUsagePoint, else 900 s, when
	// zero).
	MirrorUsagePoint *ieee20305.MirrorUsagePoint
	Readings         func(ctx context.Context) ([]ieee20305.MirrorMeterReading, error)
	PostRate         time.Duration
	// OnDefaultDERControl is called with the DefaultDERControl of the
	// DERProgram of highest precedence when it changes: an
	// *ieee20305.DefaultDERControl, or that of the profile of the codec of
	// the Client, such as *csipaus.DefaultDERControl.
	OnDefaultDERControl func(ctx context.Context, d any) error
	// Clock is the clock of the events: the local clock corrected to the
	// server's when nil.
	Clock schedule.Clock
	// Seed seeds the randomization of the events: the SFDI when zero.
	Seed uint64
	// MaxEvents bounds the events the agent keeps of one function set:
	// schedule.DefaultMaxEvents (1000) when zero, no bound when negative.
	// Of the events a server lists beyond it, the agent keeps those it
	// has and leaves out the new ones, in the order of the lists: it
	// logs that and counts them (EventStats). The transitions that wait
	// for a handler are bounded with it: see schedule.Options.QueueSize.
	MaxEvents int
	// Metrics receives the agent's polls, events and Responses; none when
	// nil.
	Metrics Metrics
	// Logger receives the agent's log (see ieee20305.Logger); none when
	// nil.
	Logger ieee20305.Logger
}

// Agent is a device that talks to one server. Its methods are safe for
// concurrent use.
type Agent struct {
	cfg Config
	c   *client.Client
	log ieee20305.Logger

	clock schedule.Clock
	loops [functionSets]*eventLoop
	// maxEvents is the bound of Config.MaxEvents: 0 for none.
	maxEvents int
	// ctx is the context of the agent's own work (handlers, Responses),
	// which Close cancels.
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	state       State
	lastDefault []byte
	files       map[string]bool
	pending     []pendingResponse

	// watched are the lists the last poll read, which the agent subscribes
	// to; subs are its subscriptions, by subscribed href.
	watched     map[string]bool
	subs        map[string]string
	noSubscribe bool
	wake        chan struct{}
}

// State is what an agent knows of the server.
type State struct {
	// DeviceCapability is the server's, as of the last read.
	DeviceCapability *ieee20305.DeviceCapability
	// Time is the server's clock.
	Time client.TimeSync
	// EndDevice is the device's, and Registration its registration.
	EndDevice    *ieee20305.EndDevice
	Registration *ieee20305.Registration
	// Assignments are the FunctionSetAssignments of the device.
	Assignments []*ieee20305.FunctionSetAssignments
	// Polls counts the reads of the assignments.
	Polls int
	// Responses counts the Responses the agent posted.
	Responses int
	// MirrorUsagePoint is the href of the device's mirror, and Posts
	// counts the posts of readings to it.
	MirrorUsagePoint string
	Posts            int
}

// New returns an Agent for cfg.
func New(cfg Config) (*Agent, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("%w: agent needs a Client", ieee20305.ErrConfiguration)
	}
	if cfg.Identity.SFDI == 0 {
		return nil, fmt.Errorf("%w: agent needs the Identity of the device", ieee20305.ErrConfiguration)
	}
	if cfg.Backoff.Initial <= 0 {
		cfg.Backoff.Initial = time.Second
	}
	if cfg.Backoff.Max <= 0 {
		cfg.Backoff.Max = 5 * time.Minute
	}
	a := &Agent{cfg: cfg, c: cfg.Client, files: map[string]bool{},
		subs: map[string]string{}, wake: make(chan struct{}, 1)}
	a.log = callback.Guard(ieee20305.ComponentLogger(cfg.Logger, "agent"))
	if a.cfg.Metrics == nil {
		a.cfg.Metrics = NopMetrics{}
	}
	a.cfg.Metrics = guardedMetrics{m: a.cfg.Metrics, log: a.log}
	switch {
	case cfg.MaxEvents == 0:
		a.maxEvents = schedule.DefaultMaxEvents
	case cfg.MaxEvents > 0:
		a.maxEvents = cfg.MaxEvents
	}
	a.clock = cfg.Clock
	if a.clock == nil {
		a.clock = offsetClock{a}
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = uint64(cfg.Identity.SFDI)
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.newLoops(seed)
	return a, nil
}

// Close stops the agent: a Run in progress returns an error that matches
// ieee20305.ErrClosed, the scheduling of events stops, and Close waits for
// the handlers of events that are running. Run closes the agent when it
// returns.
//
// Close may be called more than once, and from several goroutines at once.
// A handler of events, and a Metrics method the agent calls for an event or
// a Response, may call it too: that Close does not wait for the handlers,
// which end as they return, while a Close on another goroutine waits for
// them.
func (a *Agent) Close() {
	a.cancel()
	a.closeLoops()
}

// errClosed is the error of the use of a closed agent.
var errClosed = fmt.Errorf("%w: agent", ieee20305.ErrClosed)

// call calls f, a function of the application that name names (such as
// "Config.OnFile"), and returns its error. One that does not return
// normally is stopped here and logged, and call returns the error of
// that: the agent holds none of its locks when it calls the application.
func (a *Agent) call(name string, f func() error) (err error) {
	if failed := callback.Do(a.log, name, func() { err = f() }); failed != nil {
		return failed
	}
	return err
}

// State returns what the agent knows of the server.
//
// The documents are copies, which the caller may keep and change.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.state
	st.DeviceCapability = st.DeviceCapability.Clone()
	st.EndDevice = st.EndDevice.Clone()
	st.Registration = st.Registration.Clone()
	st.Time.Time = st.Time.Time.Clone()
	if st.Assignments != nil {
		st.Assignments = make([]*ieee20305.FunctionSetAssignments, len(a.state.Assignments))
		for i, f := range a.state.Assignments {
			st.Assignments[i] = f.Clone()
		}
	}
	return st
}

// Start reads DeviceCapability and the server's time, finds or registers
// the device's EndDevice, checks its Registration and reads its
// FunctionSetAssignments. It matches client.ErrNotRegistered when the
// server has not registered the device yet, and client.ErrPINMismatch
// when the PIN is not the device's.
func (a *Agent) Start(ctx context.Context) error {
	if a.ctx.Err() != nil {
		return errClosed
	}
	ed, err := a.endDevice(ctx)
	if err != nil {
		return err
	}
	reg, err := a.c.Registration(ctx, ed)
	if err != nil {
		return err
	}
	if a.cfg.VerifyPIN {
		if err := client.CheckPIN(reg, a.cfg.PIN); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.state.Registration = reg
	a.mu.Unlock()
	if a.cfg.DeviceInformation != nil && ed.DeviceInformationLink != nil {
		if err := a.c.Put(ctx, ed.DeviceInformationLink.Href, a.cfg.DeviceInformation); err != nil {
			return err
		}
	}
	return a.poll(ctx)
}

// endDevice returns the device's EndDevice: after DeviceCapability, the
// server's time and the registration of the EndDevice, or, when the agent
// has one and waits for the server to register the device, the EndDevice
// read again, so that waiting costs the server one request per poll.
func (a *Agent) endDevice(ctx context.Context) (*ieee20305.EndDevice, error) {
	st := a.State()
	if st.EndDevice != nil && st.Registration == nil && st.EndDevice.GetHref() != "" {
		var ed ieee20305.EndDevice
		err := a.c.Get(ctx, st.EndDevice.GetHref(), &ed)
		if err == nil {
			a.mu.Lock()
			a.state.EndDevice = &ed
			a.mu.Unlock()
			return &ed, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
		// The EndDevice is gone: start over.
	}
	dc, err := a.c.DeviceCapability(ctx)
	if err != nil {
		return nil, err
	}
	ts, err := a.c.Time(ctx)
	if err != nil {
		return nil, err
	}
	if dc.EndDeviceListLink == nil {
		return nil, fmt.Errorf("%w: the DeviceCapability has no EndDeviceListLink", ieee20305.ErrProtocol)
	}
	ed, _, err := a.c.Register(ctx, dc.EndDeviceListLink.Href, a.cfg.Identity)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.state.DeviceCapability, a.state.Time, a.state.EndDevice = dc, ts, ed
	a.mu.Unlock()
	return ed, nil
}

// poll reads the device's FunctionSetAssignments and hands them to
// OnAssignments.
func (a *Agent) poll(ctx context.Context) error {
	ed := a.State().EndDevice
	var fsas []*ieee20305.FunctionSetAssignments
	a.mu.Lock()
	a.watched = map[string]bool{}
	a.mu.Unlock()
	if ed.FunctionSetAssignmentsListLink != nil {
		a.watch(ed.FunctionSetAssignmentsListLink.Href)
		err := a.c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 0, func(it any) error {
			if f, ok := it.(*ieee20305.FunctionSetAssignments); ok {
				fsas = append(fsas, f)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.state.Assignments = fsas
	a.state.Polls++
	a.mu.Unlock()
	if a.cfg.OnAssignments != nil {
		if err := a.call("Config.OnAssignments", func() error { return a.cfg.OnAssignments(ctx, fsas) }); err != nil {
			return err
		}
	}
	for _, l := range a.loops {
		if err := a.pollEvents(ctx, l, fsas); err != nil {
			return err
		}
	}
	if err := a.pollFiles(ctx, fsas); err != nil {
		return err
	}
	a.subscribe(ctx)
	a.retryResponses(ctx)
	return a.pollDefault(ctx)
}

// isNotFound reports whether err is a 404 of the server.
func isNotFound(err error) bool {
	var se *ieee20305.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

// pollRate is the interval between polls: the configured one, else the
// pollRate of DeviceCapability (900 s by default).
func (a *Agent) pollRate() time.Duration {
	if a.cfg.PollRate > 0 {
		return a.cfg.PollRate
	}
	if dc := a.State().DeviceCapability; dc != nil && dc.PollRate != nil && *dc.PollRate > 0 {
		return time.Duration(*dc.PollRate) * time.Second
	}
	return 900 * time.Second
}

// Run starts the agent, retrying until it is registered, and then polls
// until ctx ends, which it returns (ctx.Err()). It keeps trying after a
// failure (with Backoff), except after errors that waiting does not fix,
// which it returns: a PIN mismatch, a certificate that does not verify
// (ErrCertificate), a configuration error, and Close (ErrClosed). Close
// ends a request or a wait in progress, as the end of ctx does. Run closes
// the agent when it returns.
func (a *Agent) Run(ctx context.Context) error {
	defer a.Close()
	// The work of Run ends with ctx, and with the agent.
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(a.ctx, cancel)()
	err := a.run(work)
	if ctx.Err() == nil && a.ctx.Err() != nil {
		return errClosed
	}
	return err
}

// run is Run with a context that ends when the agent is closed.
func (a *Agent) run(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		err := a.Start(ctx)
		if err == nil {
			break
		}
		if permanent(err) {
			a.log.Errorf("cannot start: %v", err)
			return err
		}
		wait := a.cfg.Backoff.Delay(attempt)
		if errors.Is(err, client.ErrNotRegistered) {
			// Waiting for the operator, not for a failing server.
			wait, attempt = a.pollRate(), -1
			a.log.Infof("waiting for the server to register the device; again in %v", wait)
		} else {
			wait = max(wait, ieee20305.RetryAfter(err))
			a.log.Warnf("start failed; again in %v: %v", wait, err)
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
	a.log.Infof("started as EndDevice %s", a.State().EndDevice.GetHref())
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() {
		a.mirror(ctx)
	})
	defer a.unsubscribe()
	wait, failures := a.pollRate(), 0
	for {
		if err := a.sleepOrWake(ctx, wait); err != nil {
			return err
		}
		start := time.Now()
		err := a.poll(ctx)
		a.cfg.Metrics.OnPoll(time.Since(start), err)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if permanent(err) {
				a.log.Errorf("stopping: %v", err)
				return err
			}
			wait = max(a.cfg.Backoff.Delay(failures), ieee20305.RetryAfter(err))
			failures++
			a.log.Warnf("poll failed; again in %v: %v", wait, err)
			continue
		}
		wait, failures = a.pollRate(), 0
		if a.subscribed() {
			wait = a.subscribedPollRate()
		}
	}
}

// permanent reports whether an error is one waiting does not fix.
func permanent(err error) bool {
	return errors.Is(err, client.ErrPINMismatch) || errors.Is(err, ieee20305.ErrClosed) ||
		errors.Is(err, ieee20305.ErrCertificate) || errors.Is(err, ieee20305.ErrConfiguration)
}

// Log posts a LogEvent of the device to its LogEventList.
func (a *Agent) Log(ctx context.Context, ev *ieee20305.LogEvent) (string, error) {
	ed := a.State().EndDevice
	if ed == nil {
		return "", ErrNotStarted
	}
	if ed.LogEventListLink == nil {
		return "", fmt.Errorf("%w: the device has no LogEventList", ieee20305.ErrProtocol)
	}
	return a.c.Post(ctx, ed.LogEventListLink.Href, ev)
}

// PutDeviceStatus puts the DeviceStatus of the device.
func (a *Agent) PutDeviceStatus(ctx context.Context, st *ieee20305.DeviceStatus) error {
	ed := a.State().EndDevice
	if ed == nil {
		return ErrNotStarted
	}
	if ed.DeviceStatusLink == nil {
		return fmt.Errorf("%w: the device has no DeviceStatus", ieee20305.ErrProtocol)
	}
	return a.c.Put(ctx, ed.DeviceStatusLink.Href, st)
}

// PutPowerStatus puts the PowerStatus of the device.
func (a *Agent) PutPowerStatus(ctx context.Context, st *ieee20305.PowerStatus) error {
	ed := a.State().EndDevice
	if ed == nil {
		return ErrNotStarted
	}
	if ed.PowerStatusLink == nil {
		return fmt.Errorf("%w: the device has no PowerStatus", ieee20305.ErrProtocol)
	}
	return a.c.Put(ctx, ed.PowerStatusLink.Href, st)
}

// sleep waits for d or the end of ctx, whose error it returns.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
