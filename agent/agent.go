// SPDX-License-Identifier: MIT

// Package agent is the behaviour of an IEEE 2030.5 device on top of package
// client: it finds the server's resources from DeviceCapability, keeps its
// clock in step with the server's, registers its EndDevice and checks the
// PIN of its Registration, and then polls its FunctionSetAssignments at the
// server's pollRate, retrying with backoff when the server fails.
//
// Run does all of this until its context ends. The function sets an agent
// takes part in plug into it as handlers: for DER, DRLC, messaging, pricing
// and prepayment, the agent schedules the events of the device's programs
// with randomization and primacy, passes their transitions to the handler
// of the function set and posts the Responses they ask for.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/schedule"
)

// Config configures an Agent.
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
	// RetryDelay is the first delay after a failure, doubled after each one
	// up to MaxRetryDelay: 1s and 5m when zero.
	RetryDelay, MaxRetryDelay time.Duration
	// OnAssignments is called with the device's FunctionSetAssignments each
	// time the agent reads them.
	OnAssignments func(ctx context.Context, fsas []*ieee20305.FunctionSetAssignments) error
	// OnDERControl is called with every transition of the DERControls of
	// the device's DERPrograms: their receipt, start, end, cancellation
	// and supersession, as a schedule.Scheduler with randomization and
	// DER conflicts works them out. The agent posts the Responses the
	// controls ask for.
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
	// Logger receives the agent's log; none when nil.
	Logger *slog.Logger
}

// Agent is a device that talks to one server. Its methods are safe for
// concurrent use.
type Agent struct {
	cfg Config
	c   *client.Client
	log *slog.Logger

	clock schedule.Clock
	loops [functionSets]*eventLoop

	mu          sync.Mutex
	state       State
	lastDefault []byte
	files       map[string]bool
	pending     []pendingResponse
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
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 5 * time.Minute
	}
	a := &Agent{cfg: cfg, c: cfg.Client, log: cfg.Logger, files: map[string]bool{}}
	if a.log == nil {
		a.log = slog.New(discardHandler{})
	}
	a.clock = cfg.Clock
	if a.clock == nil {
		a.clock = offsetClock{a}
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = uint64(cfg.Identity.SFDI)
	}
	a.newLoops(seed)
	return a, nil
}

// Close stops the scheduling of events and waits for the handlers. Run
// closes the agent when it returns.
func (a *Agent) Close() { a.closeLoops() }

// State returns what the agent knows of the server.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// Start reads DeviceCapability and the server's time, finds or registers
// the device's EndDevice, checks its Registration and reads its
// FunctionSetAssignments. It matches client.ErrNotRegistered when the
// server has not registered the device yet, and client.ErrPINMismatch
// when the PIN is not the device's.
func (a *Agent) Start(ctx context.Context) error {
	dc, err := a.c.DeviceCapability(ctx)
	if err != nil {
		return err
	}
	ts, err := a.c.Time(ctx)
	if err != nil {
		return err
	}
	if dc.EndDeviceListLink == nil {
		return fmt.Errorf("%w: the DeviceCapability has no EndDeviceListLink", ieee20305.ErrProtocol)
	}
	ed, _, err := a.c.Register(ctx, dc.EndDeviceListLink.Href, a.cfg.Identity)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.state.DeviceCapability, a.state.Time, a.state.EndDevice = dc, ts, ed
	a.mu.Unlock()
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

// poll reads the device's FunctionSetAssignments and hands them to
// OnAssignments.
func (a *Agent) poll(ctx context.Context) error {
	ed := a.State().EndDevice
	var fsas []*ieee20305.FunctionSetAssignments
	if ed.FunctionSetAssignmentsListLink != nil {
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
		if err := a.cfg.OnAssignments(ctx, fsas); err != nil {
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
// until ctx ends, which it returns (ctx.Err()). A PIN mismatch stops it.
// Run closes the agent when it returns.
func (a *Agent) Run(ctx context.Context) error {
	defer a.Close()
	delay := a.cfg.RetryDelay
	for {
		err := a.Start(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, client.ErrPINMismatch) {
			return err
		}
		wait := delay
		if errors.Is(err, client.ErrNotRegistered) {
			// Waiting for the operator, not for a failing server.
			wait = a.pollRate()
			a.log.Info("waiting for registration", "retry", wait)
		} else {
			a.log.Warn("start failed", "err", err, "retry", wait)
			delay = min(2*delay, a.cfg.MaxRetryDelay)
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
	a.log.Info("started", "endDevice", a.State().EndDevice.GetHref())
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.mirror(ctx)
	}()
	delay = a.cfg.RetryDelay
	wait := a.pollRate()
	for {
		if err := sleep(ctx, wait); err != nil {
			return err
		}
		if err := a.poll(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.log.Warn("poll failed", "err", err, "retry", delay)
			wait = delay
			delay = min(2*delay, a.cfg.MaxRetryDelay)
			continue
		}
		delay, wait = a.cfg.RetryDelay, a.pollRate()
	}
}

// Log posts a LogEvent of the device to its LogEventList.
func (a *Agent) Log(ctx context.Context, ev *ieee20305.LogEvent) (string, error) {
	ed := a.State().EndDevice
	if ed == nil || ed.LogEventListLink == nil {
		return "", fmt.Errorf("%w: the device has no LogEventList", ieee20305.ErrProtocol)
	}
	return a.c.Post(ctx, ed.LogEventListLink.Href, ev)
}

// PutDeviceStatus puts the DeviceStatus of the device.
func (a *Agent) PutDeviceStatus(ctx context.Context, st *ieee20305.DeviceStatus) error {
	ed := a.State().EndDevice
	if ed == nil || ed.DeviceStatusLink == nil {
		return fmt.Errorf("%w: the device has no DeviceStatus", ieee20305.ErrProtocol)
	}
	return a.c.Put(ctx, ed.DeviceStatusLink.Href, st)
}

// PutPowerStatus puts the PowerStatus of the device.
func (a *Agent) PutPowerStatus(ctx context.Context, st *ieee20305.PowerStatus) error {
	ed := a.State().EndDevice
	if ed == nil || ed.PowerStatusLink == nil {
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

// discardHandler drops the log (slog.DiscardHandler is Go 1.24).
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h discardHandler) WithGroup(string) slog.Handler           { return h }
