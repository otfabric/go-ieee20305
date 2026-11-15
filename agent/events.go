// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"reflect"
	"sync/atomic"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/goid"
	"github.com/otfabric/go-ieee20305/schedule"
)

// FunctionSet is a function set of events an agent takes part in.
type FunctionSet int

const (
	// DER is Distributed Energy Resources: DERControls of DERPrograms.
	DER FunctionSet = iota
	// DRLC is Demand Response and Load Control: EndDeviceControls of
	// DemandResponsePrograms.
	DRLC
	// Messaging is TextMessages of MessagingPrograms.
	Messaging
	// Pricing is TimeTariffIntervals of the RateComponents of
	// TariffProfiles.
	Pricing
	// Prepayment is SupplyInterruptionOverrides of Prepayments.
	Prepayment
	// FlowReservation is the FlowReservationResponses the server sends to
	// the device's requests.
	FlowReservation
	functionSets
)

func (fs FunctionSet) String() string {
	switch fs {
	case DER:
		return "DER"
	case DRLC:
		return "DRLC"
	case Messaging:
		return "Messaging"
	case Pricing:
		return "Pricing"
	case Prepayment:
		return "Prepayment"
	case FlowReservation:
		return "FlowReservation"
	case functionSets:
	}
	return "FunctionSet(?)"
}

// eventSet is how the agent follows the events of a function set.
type eventSet struct {
	fs FunctionSet
	// programs returns the href of the list of the programs of an FSA.
	programs func(fsa *ieee20305.FunctionSetAssignments) string
	// own returns the hrefs of the lists of events of the EndDevice
	// itself, of primacy 0.
	own func(ed *ieee20305.EndDevice) []string
	// events returns the hrefs of the lists of events of a program, and
	// its primacy.
	events func(ctx context.Context, a *Agent, program any) (lists []string, primacy uint8, ok bool, err error)
	// conflicts is the Conflicts of the scheduler.
	conflicts func(a, b schedule.Event) bool
	// response returns a new Response of the function set, or nil for a
	// function set without them.
	response func() any
	// handler returns the handler of the agent's configuration, and name
	// names it, for the log.
	handler func(cfg *Config) func(ctx context.Context, t schedule.Transition) error
	name    string
}

var eventSets = [functionSets]eventSet{
	DER: {
		fs: DER,
		programs: func(f *ieee20305.FunctionSetAssignments) string {
			if f.DERProgramListLink == nil {
				return ""
			}
			return f.DERProgramListLink.Href
		},
		events: func(_ context.Context, _ *Agent, v any) ([]string, uint8, bool, error) {
			p, ok := v.(*ieee20305.DERProgram)
			if !ok {
				return nil, 0, false, nil
			}
			// A program without controls keeps its primacy: it ranks the
			// DefaultDERControls of the programs.
			if p.DERControlListLink == nil {
				return nil, uint8(p.Primacy), true, nil
			}
			return []string{p.DERControlListLink.Href}, uint8(p.Primacy), true, nil
		},
		conflicts: schedule.DERConflicts,
		response:  func() any { return &ieee20305.DERControlResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnDERControl },
		name:      "Config.OnDERControl",
	},
	DRLC: {
		fs: DRLC,
		programs: func(f *ieee20305.FunctionSetAssignments) string {
			if f.DemandResponseProgramListLink == nil {
				return ""
			}
			return f.DemandResponseProgramListLink.Href
		},
		events: func(_ context.Context, _ *Agent, v any) ([]string, uint8, bool, error) {
			p, ok := v.(*ieee20305.DemandResponseProgram)
			if !ok || p.EndDeviceControlListLink == nil {
				return nil, 0, ok, nil
			}
			return []string{p.EndDeviceControlListLink.Href}, uint8(p.Primacy), true, nil
		},
		conflicts: schedule.DRLCConflicts,
		response:  func() any { return &ieee20305.DrResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnEndDeviceControl },
		name:      "Config.OnEndDeviceControl",
	},
	Messaging: {
		fs: Messaging,
		programs: func(f *ieee20305.FunctionSetAssignments) string {
			if f.MessagingProgramListLink == nil {
				return ""
			}
			return f.MessagingProgramListLink.Href
		},
		events: func(_ context.Context, _ *Agent, v any) ([]string, uint8, bool, error) {
			p, ok := v.(*ieee20305.MessagingProgram)
			if !ok || p.TextMessageListLink == nil {
				return nil, 0, ok, nil
			}
			return []string{p.TextMessageListLink.Href}, uint8(p.Primacy), true, nil
		},
		// Messages do not compete: several are shown at once.
		conflicts: func(a, b schedule.Event) bool { return false },
		response:  func() any { return &ieee20305.TextResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnTextMessage },
		name:      "Config.OnTextMessage",
	},
	Pricing: {
		fs: Pricing,
		programs: func(f *ieee20305.FunctionSetAssignments) string {
			if f.TariffProfileListLink == nil {
				return ""
			}
			return f.TariffProfileListLink.Href
		},
		events: func(ctx context.Context, a *Agent, v any) ([]string, uint8, bool, error) {
			p, ok := v.(*ieee20305.TariffProfile)
			if !ok || p.RateComponentListLink == nil {
				return nil, 0, ok, nil
			}
			var lists []string
			err := a.c.Iterate(ctx, p.RateComponentListLink.Href, 0, func(it any) error {
				if rc, ok := it.(*ieee20305.RateComponent); ok && rc.TimeTariffIntervalListLink.Href != "" {
					lists = append(lists, rc.TimeTariffIntervalListLink.Href)
				}
				return nil
			})
			return lists, uint8(p.Primacy), true, err
		},
		// The intervals of a rate component are its tiers in time; those
		// of different rate components (or profiles) apply together.
		conflicts: func(a, b schedule.Event) bool { return a.Program == b.Program },
		response:  func() any { return &ieee20305.PriceResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnTimeTariffInterval },
		name:      "Config.OnTimeTariffInterval",
	},
	Prepayment: {
		fs: Prepayment,
		programs: func(f *ieee20305.FunctionSetAssignments) string {
			if f.PrepaymentListLink == nil {
				return ""
			}
			return f.PrepaymentListLink.Href
		},
		events: func(_ context.Context, _ *Agent, v any) ([]string, uint8, bool, error) {
			p, ok := v.(*ieee20305.Prepayment)
			if !ok || p.SupplyInterruptionOverrideListLink.Href == "" {
				return nil, 0, ok, nil
			}
			return []string{p.SupplyInterruptionOverrideListLink.Href}, 0, true, nil
		},
		conflicts: func(a, b schedule.Event) bool { return false },
		handler: func(c *Config) func(context.Context, schedule.Transition) error {
			return c.OnSupplyInterruptionOverride
		},
		name: "Config.OnSupplyInterruptionOverride",
	},
	FlowReservation: {
		fs:       FlowReservation,
		programs: func(*ieee20305.FunctionSetAssignments) string { return "" },
		events:   func(context.Context, *Agent, any) ([]string, uint8, bool, error) { return nil, 0, false, nil },
		own: func(ed *ieee20305.EndDevice) []string {
			if ed.FlowReservationResponseListLink == nil {
				return nil
			}
			return []string{ed.FlowReservationResponseListLink.Href}
		},
		conflicts: func(a, b schedule.Event) bool { return false },
		response:  func() any { return &ieee20305.FlowReservationResponseResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnFlowReservation },
		name:      "Config.OnFlowReservation",
	},
}

// eventLoop is the scheduling of the events of a function set by an
// agent.
type eventLoop struct {
	set   *eventSet
	sched *schedule.Scheduler
	done  chan struct{}
	// gid identifies the goroutine of runLoop, which calls the handler:
	// Close reads it to know that it was called from a handler.
	gid atomic.Uint64
	// best is the program of highest precedence, as of the last poll, and
	// left counts the events of the polls the agent left out
	// (Config.MaxEvents). Both are guarded by Agent.mu.
	best any
	left uint64
}

func (a *Agent) newLoops(seed uint64) {
	for i := range eventSets {
		set := &eventSets[i]
		l := &eventLoop{set: set, done: make(chan struct{})}
		l.sched = schedule.New(schedule.Options{Clock: a.clock, Seed: seed, Randomize: true, Conflicts: set.conflicts,
			MaxEvents: a.cfg.MaxEvents})
		ch, _ := l.sched.Subscribe()
		go a.runLoop(l, ch)
		a.loops[i] = l
	}
}

// closeLoops ends the scheduling of the events and waits for the handlers
// that are running, unless a handler called it: its goroutine is one of
// those to wait for, and so may be another handler's that closes the agent
// too. The loops then end as their handlers return.
func (a *Agent) closeLoops() {
	self := goid.ID()
	inHandler := false
	for _, l := range a.loops {
		l.sched.Close()
		if self != 0 && l.gid.Load() == self {
			inHandler = true
		}
	}
	if inHandler {
		return
	}
	for _, l := range a.loops {
		<-l.done
	}
}

// pollEvents reads the programs of the function set of l in the
// assignments, and schedules their events: all at once, when it has read
// them all, so that they are resolved together. The events the server no
// longer lists go first. After a failure, the events read so far are
// scheduled and none goes.
func (a *Agent) pollEvents(ctx context.Context, l *eventLoop, fsas []*ieee20305.FunctionSetAssignments) error {
	p := &eventPoll{known: map[string]bool{}, listed: map[string]bool{}, max: a.maxEvents}
	for _, ev := range l.sched.Events() {
		p.known[ev.MRID] = true
	}
	best, err := a.readEvents(ctx, l, fsas, p)
	if err == nil {
		var gone []string
		for mrid := range p.known {
			if !p.listed[mrid] {
				gone = append(gone, mrid)
			}
		}
		l.sched.RemoveAll(gone)
	}
	left := p.over + uint64(l.sched.UpsertAll(append(p.kept, p.added...)))
	if left > 0 {
		a.log.Warnf("%v: the server lists %d events beyond the %d the agent keeps (Config.MaxEvents): left out",
			l.set.fs, left, a.maxEvents)
	}
	a.mu.Lock()
	l.left += left
	if err == nil {
		l.best = best
	}
	a.mu.Unlock()
	return err
}

// eventPoll is what a poll read of the events of a function set.
type eventPoll struct {
	// known are the events the agent had before the poll, and listed
	// those the server lists, by mRID.
	known, listed map[string]bool
	// kept are the listed events the agent had, and added the new ones,
	// in the order of the lists: at most max of them (no bound when max
	// is 0), and over counts the others.
	kept, added []schedule.Event
	max         int
	over        uint64
}

// take records an event the server lists.
func (p *eventPoll) take(ev schedule.Event) {
	if ev.MRID == "" || p.listed[ev.MRID] {
		return
	}
	p.listed[ev.MRID] = true
	switch {
	case p.known[ev.MRID]:
		p.kept = append(p.kept, ev)
	case p.max > 0 && len(p.added) >= p.max:
		p.over++
	default:
		p.added = append(p.added, ev)
	}
}

// readEvents reads the events of the function set of l into p, and
// returns its program of highest precedence.
func (a *Agent) readEvents(ctx context.Context, l *eventLoop, fsas []*ieee20305.FunctionSetAssignments, p *eventPoll) (any, error) {
	seen := map[string]bool{}
	var best any
	bestPrimacy := -1
	collect := func(list string, primacy uint8, program string) error {
		a.watch(list)
		return a.c.Iterate(ctx, list, 0, func(it any) error {
			ev, err := schedule.FromEvent(it, primacy, program+"|"+list)
			if err != nil {
				a.log.Warnf("%v: skipping an event: %v", l.set.fs, err)
				return nil
			}
			p.take(ev)
			return nil
		})
	}
	if ed := a.State().EndDevice; l.set.own != nil && ed != nil {
		for _, list := range l.set.own(ed) {
			if err := collect(list, 0, ed.GetHref()); err != nil {
				return nil, err
			}
		}
	}
	for _, fsa := range fsas {
		href := l.set.programs(fsa)
		if href == "" || seen[href] {
			continue
		}
		seen[href] = true
		a.watch(href)
		var programs []any
		if err := a.c.Iterate(ctx, href, 0, func(it any) error {
			programs = append(programs, it)
			return nil
		}); err != nil {
			return nil, err
		}
		for _, prog := range programs {
			lists, primacy, ok, err := l.set.events(ctx, a, prog)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if bestPrimacy < 0 || int(primacy) < bestPrimacy {
				best, bestPrimacy = prog, int(primacy)
			}
			for _, list := range lists {
				if err := collect(list, primacy, hrefOf(prog)); err != nil {
					return nil, err
				}
			}
		}
	}
	return best, nil
}

func hrefOf(v any) string {
	if h, ok := v.(ieee20305.Hrefer); ok {
		return h.GetHref()
	}
	return ""
}

// runLoop passes the transitions of the events of l to the handler and
// posts the Responses they ask for, until the scheduler closes. A handler
// that does not return normally is logged (call), and the Response of its
// transition is posted as after a handler that fails.
func (a *Agent) runLoop(l *eventLoop, ch <-chan schedule.Transition) {
	defer close(l.done)
	l.gid.Store(goid.ID())
	handle := l.set.handler(&a.cfg)
	for t := range ch {
		ctx := a.ctx
		a.cfg.Metrics.OnEvent(l.set.fs, t)
		if handle != nil {
			if err := a.call(l.set.name, func() error { return handle(ctx, t) }); err != nil {
				a.log.Warnf("%v: the handler of event %s failed: %v", l.set.fs, t.Event.MRID, err)
			}
		}
		if err := a.respond(ctx, l.set, t); err != nil {
			a.log.Warnf("%v: posting a Response to event %s failed: %v", l.set.fs, t.Event.MRID, err)
		}
	}
}

// respond posts the Response the transition t calls for.
func (a *Agent) respond(ctx context.Context, set *eventSet, t schedule.Transition) error {
	if set.response == nil {
		return nil
	}
	ev := reflect.ValueOf(t.Event.Value)
	if ev.Kind() != reflect.Pointer || ev.IsNil() {
		return nil
	}
	ev = ev.Elem()
	replyTo, _ := field(ev, "ReplyTo").(*string)
	if replyTo == nil || *replyTo == "" {
		return nil
	}
	var flags []byte
	if rr, _ := field(ev, "ResponseRequired").(*ieee20305.HexBinary8); rr != nil {
		flags = *rr
	}
	send, status := schedule.ResponseRequired(flags, t)
	if !send {
		return nil
	}
	doc := set.response()
	rf := reflect.ValueOf(doc).Elem().FieldByName("Response")
	if !rf.IsValid() || !rf.CanAddr() {
		return nil
	}
	r, ok := rf.Addr().Interface().(*ieee20305.Response)
	if !ok {
		return nil
	}
	created, st := ieee20305.TimeType(t.At.Unix()), ieee20305.UInt8(status)
	r.CreatedDateTime, r.Status = &created, &st
	r.Subject, _ = field(ev, "MRID").(ieee20305.MRIDType)
	r.EndDeviceLFDI = ieee20305.HexBinary160(a.cfg.Identity.LFDI[:])
	return a.postResponse(ctx, pendingResponse{href: *replyTo, doc: doc, fs: set.fs, status: status})
}

// field returns the field name of the struct v (through embedding), or nil
// when it has none: event types of profiles may lack what IEEE 2030.5's
// have.
func field(v reflect.Value, name string) any {
	f := v.FieldByName(name)
	if !f.IsValid() || !f.CanInterface() {
		return nil
	}
	return f.Interface()
}

// pendingResponse is a Response the agent could not post yet.
type pendingResponse struct {
	href   string
	doc    any
	fs     FunctionSet
	status uint8
}

// maxPending bounds the Responses the agent keeps to send again.
const maxPending = 1000

// postResponse posts a Response, or keeps it to send again at the next
// poll (the server stores a Response once, so sending one twice is
// harmless).
func (a *Agent) postResponse(ctx context.Context, r pendingResponse) error {
	_, err := a.c.Post(ctx, r.href, r.doc)
	a.cfg.Metrics.OnResponse(r.fs, r.status, err)
	if err != nil {
		if rejected(err) {
			a.log.Warnf("the server refused a Response to %s, dropped: %v", r.href, err)
			return err
		}
		a.mu.Lock()
		kept := len(a.pending) < maxPending
		if kept {
			a.pending = append(a.pending, r)
		}
		a.mu.Unlock()
		if !kept {
			a.log.Warnf("%d Responses wait to be posted: one to %s dropped", maxPending, r.href)
		}
		return err
	}
	a.mu.Lock()
	a.state.Responses++
	a.mu.Unlock()
	return nil
}

// retryResponses posts the Responses kept after a failure.
func (a *Agent) retryResponses(ctx context.Context) {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, r := range pending {
		_ = a.postResponse(ctx, r)
	}
}

// Active returns the events of the function set fs active now, in order of
// precedence: pointers to their documents, such as *ieee20305.DERControl.
func (a *Agent) Active(fs FunctionSet) []any {
	if fs < 0 || fs >= functionSets {
		return nil
	}
	var out []any
	for _, ev := range a.loops[fs].sched.Active(a.clock.Now()) {
		if ev.Status == schedule.Active {
			out = append(out, ev.Value)
		}
	}
	return out
}

// ActiveDERControls returns the DERControls of IEEE 2030.5 active now, in
// order of precedence. (Those of a profile, such as CSIP-AUS, are in
// Active(DER).)
func (a *Agent) ActiveDERControls() []*ieee20305.DERControl {
	var out []*ieee20305.DERControl
	for _, v := range a.Active(DER) {
		if c, ok := v.(*ieee20305.DERControl); ok {
			out = append(out, c)
		}
	}
	return out
}

// Events returns every event of the function set fs the agent knows, with
// its state.
func (a *Agent) Events(fs FunctionSet) []schedule.Event {
	if fs < 0 || fs >= functionSets {
		return nil
	}
	return a.loops[fs].sched.Events()
}

// EventStats returns the counters of the events of the function set fs,
// from New on: Refused counts the events of the polls the agent left out
// because it kept Config.MaxEvents events (an event the server goes on
// listing is counted at each poll), and Coalesced and Dropped the
// transitions the handler did not get because it had fallen behind (see
// schedule.Stats).
func (a *Agent) EventStats(fs FunctionSet) schedule.Stats {
	if fs < 0 || fs >= functionSets {
		return schedule.Stats{}
	}
	l := a.loops[fs]
	st := l.sched.Stats()
	a.mu.Lock()
	st.Refused = l.left
	a.mu.Unlock()
	return st
}

// offsetClock is the clock of the server as the agent knows it: the local
// clock corrected by the offset of the last time sync.
type offsetClock struct{ a *Agent }

func (c offsetClock) Now() time.Time { return time.Now().Add(c.a.State().Time.Offset) }

func (offsetClock) AfterFunc(d time.Duration, f func()) schedule.Stopper {
	return time.AfterFunc(d, f)
}
