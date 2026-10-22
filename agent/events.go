// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"reflect"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
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
	// handler returns the handler of the agent's configuration.
	handler func(cfg *Config) func(ctx context.Context, t schedule.Transition) error
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
			if !ok || p.DERControlListLink == nil {
				return nil, 0, ok, nil
			}
			return []string{p.DERControlListLink.Href}, uint8(p.Primacy), true, nil
		},
		conflicts: schedule.DERConflicts,
		response:  func() any { return &ieee20305.DERControlResponse{} },
		handler:   func(c *Config) func(context.Context, schedule.Transition) error { return c.OnDERControl },
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
	},
}

// eventLoop is the scheduling of the events of a function set by an
// agent.
type eventLoop struct {
	set   *eventSet
	sched *schedule.Scheduler
	done  chan struct{}
	// best is the program of highest precedence, as of the last poll.
	best any
}

func (a *Agent) newLoops(seed uint64) {
	for i := range eventSets {
		set := &eventSets[i]
		l := &eventLoop{set: set, done: make(chan struct{})}
		l.sched = schedule.New(schedule.Options{Clock: a.clock, Seed: seed, Randomize: true, Conflicts: set.conflicts})
		ch, _ := l.sched.Subscribe()
		go a.runLoop(l, ch)
		a.loops[i] = l
	}
}

func (a *Agent) closeLoops() {
	for _, l := range a.loops {
		l.sched.Close()
		<-l.done
	}
}

// pollEvents reads the programs of the function set of l in the
// assignments, and schedules their events.
func (a *Agent) pollEvents(ctx context.Context, l *eventLoop, fsas []*ieee20305.FunctionSetAssignments) error {
	seen := map[string]bool{}
	events := map[string]bool{}
	var best any
	bestPrimacy := -1
	collect := func(list string, primacy uint8, program string) error {
		return a.c.Iterate(ctx, list, 0, func(it any) error {
			ev, err := schedule.FromEvent(it, primacy, program+"|"+list)
			if err != nil {
				a.log.Warnf("%v: skipping an event: %v", l.set.fs, err)
				return nil
			}
			if ev.MRID == "" || events[ev.MRID] {
				return nil
			}
			events[ev.MRID] = true
			l.sched.Upsert(ev)
			return nil
		})
	}
	if ed := a.State().EndDevice; l.set.own != nil && ed != nil {
		for _, list := range l.set.own(ed) {
			if err := collect(list, 0, ed.GetHref()); err != nil {
				return err
			}
		}
	}
	for _, fsa := range fsas {
		href := l.set.programs(fsa)
		if href == "" || seen[href] {
			continue
		}
		seen[href] = true
		var programs []any
		if err := a.c.Iterate(ctx, href, 0, func(it any) error {
			programs = append(programs, it)
			return nil
		}); err != nil {
			return err
		}
		for _, p := range programs {
			lists, primacy, ok, err := l.set.events(ctx, a, p)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if bestPrimacy < 0 || int(primacy) < bestPrimacy {
				best, bestPrimacy = p, int(primacy)
			}
			for _, list := range lists {
				if err := collect(list, primacy, hrefOf(p)); err != nil {
					return err
				}
			}
		}
	}
	for _, ev := range l.sched.Events() {
		if !events[ev.MRID] {
			l.sched.Remove(ev.MRID)
		}
	}
	a.mu.Lock()
	l.best = best
	a.mu.Unlock()
	return nil
}

func hrefOf(v any) string {
	if h, ok := v.(ieee20305.Hrefer); ok {
		return h.GetHref()
	}
	return ""
}

// runLoop passes the transitions of the events of l to the handler and
// posts the Responses they ask for, until the scheduler closes.
func (a *Agent) runLoop(l *eventLoop, ch <-chan schedule.Transition) {
	defer close(l.done)
	handle := l.set.handler(&a.cfg)
	for t := range ch {
		ctx := a.ctx
		a.cfg.Metrics.OnEvent(l.set.fs, t)
		if handle != nil {
			if err := handle(ctx, t); err != nil {
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

// offsetClock is the clock of the server as the agent knows it: the local
// clock corrected by the offset of the last time sync.
type offsetClock struct{ a *Agent }

func (c offsetClock) Now() time.Time { return time.Now().Add(c.a.State().Time.Offset) }

func (offsetClock) AfterFunc(d time.Duration, f func()) schedule.Stopper {
	return time.AfterFunc(d, f)
}
