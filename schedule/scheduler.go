// SPDX-License-Identifier: MIT

package schedule

import (
	"cmp"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Status is the state of an event: the currentStatus codes of an
// IEEE 2030.5 EventStatus. Completed is new in IEEE 2030.5-2023, and
// servers of earlier revisions do not send it.
type Status uint8

// The states of an event.
const (
	// Scheduled is an event that has not started yet.
	Scheduled Status = 0
	// Active is an event that is in effect.
	Active Status = 1
	// Cancelled is an event the server cancelled.
	Cancelled Status = 2
	// CancelledRandom is an event the server cancelled with
	// randomization: an active event ends after a random delay.
	CancelledRandom Status = 3
	// Superseded is an event that lost to a conflicting, overlapping
	// event of higher precedence. IEEE 2030.5-2023 deprecates it for
	// servers, which leave supersession to the clients.
	Superseded Status = 4
	// Completed is an event whose interval has ended, or that was
	// received after its interval ended.
	Completed Status = 5
)

// String returns the name of s.
func (s Status) String() string {
	switch s {
	case Scheduled:
		return "Scheduled"
	case Active:
		return "Active"
	case Cancelled:
		return "Cancelled"
	case CancelledRandom:
		return "CancelledRandom"
	case Superseded:
		return "Superseded"
	case Completed:
		return "Completed"
	default:
		return "Status(" + strconv.Itoa(int(s)) + ")"
	}
}

// Terminal reports whether s is a final state: an event in it never
// changes state again.
func (s Status) Terminal() bool {
	switch s {
	case Cancelled, CancelledRandom, Superseded, Completed:
		return true
	default:
		return false
	}
}

// Event is an event of any IEEE 2030.5 Event function set, as the
// Scheduler sees it. FromEvent makes one from a resource.
type Event struct {
	// MRID identifies the event; the Scheduler keys events by it.
	MRID string
	// Program identifies the program the event belongs to.
	Program string
	// Primacy is the primacy of the program: lower values take
	// precedence.
	Primacy uint8
	// Created is the creationTime of the event.
	Created time.Time
	// Start and Duration are the interval of the event, before
	// randomization.
	Start    time.Time
	Duration time.Duration
	// RandStart and RandDuration are the randomizeStart and
	// randomizeDuration bounds, in signed seconds.
	RandStart, RandDuration int32
	// Status is the state of the event. In what a Scheduler returns it
	// is the current state; Upsert only reads the terminal states from
	// it.
	Status Status
	// Value is the resource the event came from, for Conflicts and for
	// the user of the Scheduler.
	Value any
}

// End returns the end of the interval of e, before randomization.
func (e Event) End() time.Time { return e.Start.Add(e.Duration) }

// Precedes reports whether a takes precedence over b: it has a lower
// primacy value, or the same primacy and a later creation time, or the same
// primacy and creation time and a lower MRID, so that the order is total.
func Precedes(a, b Event) bool { return compare(a, b) < 0 }

func compare(a, b Event) int {
	if c := cmp.Compare(a.Primacy, b.Primacy); c != 0 {
		return c
	}
	if c := b.Created.Compare(a.Created); c != 0 {
		return c
	}
	return cmp.Compare(a.MRID, b.MRID)
}

// Transition is a change of state of an event. The receipt of a new event
// is a Transition with From equal to To: its initial state, which is
// Scheduled, Completed for an event received after it ended, or the
// cancelled or superseded state the event arrived with. Transitions out of
// the initial state follow it.
type Transition struct {
	// Event is the event, with Status To.
	Event    Event
	From, To Status
	// At is the time of the transition by the Clock.
	At time.Time
}

// Received reports whether t is the receipt of a new event.
func (t Transition) Received() bool { return t.From == t.To }

// Segment is a span of time in which the same events are active.
type Segment struct {
	Start, End time.Time
	// Events are the active events, in order of precedence; none when
	// no event is active.
	Events []Event
}

// Options configure a Scheduler.
type Options struct {
	// Clock is the time source; SystemClock when nil.
	Clock Clock
	// Seed seeds the randomization. Each event draws its random
	// offsets from Seed and its MRID, so they do not depend on the
	// order in which events arrive. Schedulers with the same Seed draw
	// the same offsets for an event: give each device its own, such as
	// its SFDI (as agent.Config does when its Seed is zero).
	Seed uint64
	// Randomize applies the randomizeStart and randomizeDuration of the
	// events, as a client does. A server leaves it off.
	Randomize bool
	// Conflicts reports whether two events compete for the same thing,
	// so that one can supersede the other. When nil, all events
	// conflict. See DERConflicts and DRLCConflicts.
	Conflicts func(a, b Event) bool
	// NoConflicts says that no two events conflict, so that none
	// supersedes another, as on a server: Conflicts is not called. Events
	// that all overlap are then resolved without comparing them with each
	// other.
	NoConflicts bool
	// MaxEvents bounds the events the Scheduler keeps: DefaultMaxEvents
	// when zero, none when negative. A new event beyond it is refused:
	// Upsert and UpsertAll leave it out, and Stats counts it. An update
	// of an event the Scheduler keeps is never refused, and an event
	// removed makes room.
	MaxEvents int
	// QueueSize bounds the transitions queued for a subscriber that has
	// not read them yet: four times MaxEvents when zero (none when
	// MaxEvents is negative), none when negative. A call queues at most
	// two transitions per event, so a subscriber that reads what one call
	// queues before the next sees every transition. When the queue is
	// full, it keeps only the latest transition of each event, and when
	// that leaves more than three quarters of QueueSize it drops the
	// oldest down to that; Stats counts both.
	QueueSize int
}

// DefaultMaxEvents is the number of events a Scheduler keeps when
// Options.MaxEvents is zero.
const DefaultMaxEvents = 1000

// Stats are counters of a Scheduler, from New on.
type Stats struct {
	// Refused counts the new events Upsert and UpsertAll refused because
	// the Scheduler kept MaxEvents events.
	Refused uint64
	// Coalesced counts the transitions a full subscriber queue left out
	// because a later transition of the same event followed them, and
	// Dropped those it left out to make room otherwise (the oldest).
	Coalesced, Dropped uint64
}

// Scheduler tracks the events of one function set and resolves them into
// what is active when.
//
// Events that overlap in time and conflict are resolved by precedence (see
// Precedes): an event that would overlap one of higher precedence is
// Superseded. An active event that a new event of higher precedence
// overlaps stays active until that event starts, and is then Superseded.
// Supersession is final: an event does not come back when the event that
// superseded it is cancelled or removed. Overlap is judged on the
// randomized intervals.
//
// The Scheduler moves events through their states on its Clock and reports
// each change to its subscribers. It is safe for concurrent use.
type Scheduler struct {
	clock     Clock
	seed      uint64
	randomize bool
	conflicts func(a, b Event) bool
	// independent is Options.NoConflicts.
	independent bool
	maxEvents   int // 0: none
	queueSize   int // 0: none

	mu      sync.Mutex
	events  map[string]*entry
	subs    map[*subscriber]struct{}
	timer   Stopper
	timerAt time.Time
	gen     uint64
	closed  bool
	wg      sync.WaitGroup
	stats   Stats
	// evaluations counts the calls of evaluate, and checks the windows it
	// compared an event with: the cost the tests bound. scanAll makes
	// evaluate compare each event with every window, for the tests to
	// compare the plans.
	evaluations, checks uint64
	scanAll             bool
}

// entry is the state of one event.
type entry struct {
	ev Event
	// start and end are the randomized interval of the event.
	start, end time.Time
	// cancelAt is when a randomized cancellation of the active event
	// takes effect; zero when none is pending.
	cancelAt time.Time
	// ws and we are the window in which the event is planned to be
	// active; empty when it is not.
	ws, we time.Time
}

// New returns a Scheduler with options o.
func New(o Options) *Scheduler {
	s := &Scheduler{
		clock:       o.Clock,
		seed:        o.Seed,
		randomize:   o.Randomize,
		conflicts:   o.Conflicts,
		independent: o.NoConflicts,
		events:      make(map[string]*entry),
		subs:        make(map[*subscriber]struct{}),
	}
	if s.clock == nil {
		s.clock = SystemClock
	}
	if s.conflicts == nil {
		s.conflicts = AllConflict
	}
	switch {
	case o.MaxEvents == 0:
		s.maxEvents = DefaultMaxEvents
	case o.MaxEvents > 0:
		s.maxEvents = o.MaxEvents
	}
	switch {
	case o.QueueSize > 0:
		s.queueSize = o.QueueSize
	case o.QueueSize == 0 && s.maxEvents > 0:
		s.queueSize = 4 * min(s.maxEvents, math.MaxInt/4)
	}
	return s
}

// rng returns the random source of the event mrid.
func (s *Scheduler) rng(mrid string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(mrid)) // a hash.Hash never returns an error
	return rand.New(rand.NewPCG(s.seed, h.Sum64()))
}

// draw returns a random whole number of seconds between 0 and bound,
// inclusive, with the sign of bound.
func draw(r *rand.Rand, bound int32) time.Duration {
	n := int64(bound)
	switch {
	case n > 0:
		return time.Duration(r.Int64N(n+1)) * time.Second
	case n < 0:
		return -time.Duration(r.Int64N(-n+1)) * time.Second
	default:
		return 0
	}
}

// place sets the randomized interval of en from its event. The interval is
// in wall-clock time, without the monotonic reading a time.Now carries:
// the planned windows are ordered by their start, and that order must not
// depend on how the times of the events were made.
func (s *Scheduler) place(en *entry) {
	start := en.ev.Start.Round(0)
	en.start, en.end = start, start.Add(en.ev.Duration)
	if !s.randomize {
		return
	}
	r := s.rng(en.ev.MRID)
	rs := draw(r, en.ev.RandStart)
	d := en.ev.Duration + draw(r, en.ev.RandDuration)
	en.start = start.Add(rs)
	en.end = en.start.Add(max(d, 0))
}

// cancelDelay returns the random delay of a randomized cancellation of the
// event of en: within the larger magnitude of its randomizeStart and
// randomizeDuration, as IEEE 2030.5 asks.
func (s *Scheduler) cancelDelay(en *entry) time.Duration {
	r := s.rng(en.ev.MRID)
	draw(r, en.ev.RandStart)
	draw(r, en.ev.RandDuration)
	return draw(r, max(abs(en.ev.RandStart), abs(en.ev.RandDuration)))
}

func abs(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

// Upsert adds the event e, or updates the event with its MRID.
//
// A new event is reported as received, then resolved against the others.
// An e with Status Cancelled or CancelledRandom cancels the event as Cancel
// does, and one with Status Superseded or Completed moves it to that state;
// Scheduled and Active are ignored, since the Scheduler works out when the
// event is active itself. An
// update of an event in a terminal state changes its fields but not its
// state. A new event is refused when the Scheduler keeps MaxEvents events
// (see Stats).
func (s *Scheduler) Upsert(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	now := s.clock.Now()
	var out []Transition
	if r := s.upsert(e, now, &out); r != upserted {
		return
	}
	s.evaluate(now, &out)
	s.publish(out)
}

// UpsertAll upserts the events of evs as Upsert does, in order, and then
// resolves them together, once: as a client takes the events of a poll. The
// outcome does not depend on the order of evs, but it can differ from that
// of upserting them one by one, each resolved as it comes: an event is
// resolved against the events of higher precedence that are planned to be
// active with all of evs known, not against one that a later event of evs
// supersedes. It returns the number of new events it refused because the
// Scheduler kept MaxEvents events.
func (s *Scheduler) UpsertAll(evs []Event) (refused int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	now := s.clock.Now()
	var out []Transition
	for _, e := range evs {
		if s.upsert(e, now, &out) == rejected {
			refused++
		}
	}
	s.evaluate(now, &out)
	s.publish(out)
	return refused
}

// The outcomes of upsert.
const (
	upserted  = iota // added or updated; to resolve
	unchanged        // an update of an event in a terminal state
	rejected         // a new event beyond MaxEvents
)

// upsert adds or updates the entry of e, without resolving it.
func (s *Scheduler) upsert(e Event, now time.Time, out *[]Transition) int {
	in := e.Status
	en, ok := s.events[e.MRID]
	if ok {
		e.Status = en.ev.Status
		en.ev = e
		s.place(en)
		if e.Status.Terminal() {
			return unchanged
		}
		switch in {
		case Cancelled:
			s.cancel(en, false, now, out)
		case CancelledRandom:
			s.cancel(en, true, now, out)
		case Superseded, Completed:
			s.set(en, in, now, out)
		default:
		}
		return upserted
	}
	if s.maxEvents > 0 && len(s.events) >= s.maxEvents {
		s.stats.Refused++
		return rejected
	}
	en = &entry{ev: e}
	s.place(en)
	init := Scheduled
	switch in {
	case Cancelled, CancelledRandom, Superseded, Completed:
		init = in
	default:
		if !en.end.After(now) {
			init = Completed
		}
	}
	en.ev.Status = init
	s.events[e.MRID] = en
	*out = append(*out, Transition{Event: en.ev, From: init, To: init, At: now})
	return upserted
}

// Cancel cancels the event mrid, as the server does with the status
// Cancelled, or CancelledRandom when randomized is true. With Randomize on,
// an active event cancelled with randomization stays active for a random
// delay within the larger magnitude of its randomizeStart and
// randomizeDuration, but not past its end. Cancel does nothing to an unknown event or one in a terminal state.
func (s *Scheduler) Cancel(mrid string, randomized bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	en, ok := s.events[mrid]
	if s.closed || !ok {
		return
	}
	now := s.clock.Now()
	var out []Transition
	s.evaluate(now, &out)
	s.cancel(en, randomized, now, &out)
	s.evaluate(now, &out)
	s.publish(out)
}

func (s *Scheduler) cancel(en *entry, randomized bool, now time.Time, out *[]Transition) {
	if en.ev.Status.Terminal() {
		return
	}
	if !randomized {
		en.cancelAt = time.Time{}
		s.set(en, Cancelled, now, out)
		return
	}
	if !en.cancelAt.IsZero() {
		return // already pending: a repeat of the same cancellation
	}
	if s.randomize && en.ev.Status == Active {
		at := earlier(now.Add(s.cancelDelay(en)), en.end)
		if at.After(now) {
			en.cancelAt = at
			return
		}
	}
	s.set(en, CancelledRandom, now, out)
}

// Remove forgets the event mrid without a transition, and reports whether
// there was one. The events it superseded stay superseded.
func (s *Scheduler) Remove(mrid string) bool {
	return s.RemoveAll([]string{mrid}) == 1
}

// RemoveAll forgets the events of mrids as Remove does, and then resolves
// the others once. It returns the number of events it forgot.
func (s *Scheduler) RemoveAll(mrids []string) (removed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	for _, mrid := range mrids {
		if _, ok := s.events[mrid]; ok {
			delete(s.events, mrid)
			removed++
		}
	}
	if removed == 0 {
		return 0
	}
	var out []Transition
	s.evaluate(s.clock.Now(), &out)
	s.publish(out)
	return removed
}

// set moves the event of en to state to.
func (s *Scheduler) set(en *entry, to Status, now time.Time, out *[]Transition) {
	from := en.ev.Status
	if from == to {
		return
	}
	en.ev.Status = to
	if to.Terminal() {
		en.ws, en.we = time.Time{}, time.Time{}
	}
	*out = append(*out, Transition{Event: en.ev, From: from, To: to, At: now})
}

// evaluate brings every event to its state at now, plans the active
// windows, and arms the timer for the next change.
//
// The events are planned in order of precedence. The windows accepted so
// far are kept ordered by their start, so an event is compared only with
// the windows that can overlap its own: those that start before it ends
// and no longer than the longest window before it starts. For events that
// do not overlap, that makes an evaluation O(n log n), not O(n²).
func (s *Scheduler) evaluate(now time.Time, out *[]Transition) {
	s.evaluations++
	live := make([]*entry, 0, len(s.events))
	for _, en := range s.events {
		if !en.cancelAt.IsZero() && !now.Before(en.cancelAt) {
			en.cancelAt = time.Time{}
			s.set(en, CancelledRandom, now, out)
		}
		if !en.ev.Status.Terminal() {
			live = append(live, en)
		}
	}
	slices.SortFunc(live, func(a, b *entry) int { return compare(a.ev, b.ev) })

	// accepted are the windows planned so far, by start; longest is the
	// longest of them.
	var accepted []*entry
	if !s.independent {
		accepted = make([]*entry, 0, len(live))
	}
	var longest time.Duration
	for _, en := range live {
		start, end := en.start, en.end
		if !en.cancelAt.IsZero() {
			end = earlier(end, en.cancelAt)
		}
		en.ws, en.we = time.Time{}, time.Time{}
		running := !now.Before(start) && now.Before(end)
		lost := false
		var cutoff time.Time
		// The windows that can overlap [start, end) start before end, and
		// after start - longest. Of those that overlap and conflict, the
		// first by start gives the earliest cutoff. Events that never
		// conflict are compared with none.
		lo, hi := 0, 0
		if !s.independent {
			hi, _ = slices.BinarySearchFunc(accepted, end, func(w *entry, t time.Time) int { return w.ws.Compare(t) })
			from := start.Add(-longest)
			lo, _ = slices.BinarySearchFunc(accepted[:hi], from, func(w *entry, t time.Time) int {
				if w.ws.After(t) {
					return 1
				}
				return -1
			})
			if s.scanAll {
				lo, hi = 0, len(accepted)
			}
		}
		for _, w := range accepted[lo:hi] {
			s.checks++
			if !overlap(start, end, w.ws, w.we) || !s.conflicts(w.ev, en.ev) {
				continue
			}
			if !running {
				lost = true
				break
			}
			cutoff = later(w.ws, now)
			break
		}
		if running && !cutoff.IsZero() {
			if !cutoff.After(now) {
				lost = true
			}
			end = cutoff
		}
		switch {
		case lost:
			s.set(en, Superseded, now, out)
		case !end.After(now):
			s.set(en, Completed, now, out)
		default:
			if running {
				s.set(en, Active, now, out)
			} else if en.ev.Status == Active {
				s.set(en, Scheduled, now, out) // its start moved later
			}
			en.ws, en.we = start, end
			if s.independent {
				continue
			}
			i, _ := slices.BinarySearchFunc(accepted, start, func(w *entry, t time.Time) int {
				if w.ws.After(t) {
					return 1
				}
				return -1
			})
			accepted = slices.Insert(accepted, i, en)
			longest = max(longest, end.Sub(start))
		}
	}
	s.arm(now, live)
}

// arm sets the timer for the next instant after now at which an event of
// live may change state.
func (s *Scheduler) arm(now time.Time, live []*entry) {
	var next time.Time
	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, en := range live {
		if en.ev.Status.Terminal() {
			continue
		}
		consider(en.start)
		consider(en.end)
		consider(en.cancelAt)
		consider(en.we)
	}
	if s.timer != nil && next.Equal(s.timerAt) {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.timerAt = next
	if next.IsZero() {
		return
	}
	s.gen++
	g := s.gen
	s.timer = s.clock.AfterFunc(next.Sub(now), func() { s.fire(g) })
}

// fire is the timer of generation g going off.
func (s *Scheduler) fire(g uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || g != s.gen {
		return
	}
	s.timer, s.timerAt = nil, time.Time{}
	var out []Transition
	s.evaluate(s.clock.Now(), &out)
	s.publish(out)
}

// refresh brings the events up to the current time, in case the timer is
// late. Until the instant the timer is set for, no event changes state, so
// the events are not resolved again before it: a query costs no
// resolution.
func (s *Scheduler) refresh() {
	if s.closed || s.timerAt.IsZero() {
		return
	}
	now := s.clock.Now()
	if now.Before(s.timerAt) {
		return
	}
	var out []Transition
	s.evaluate(now, &out)
	s.publish(out)
}

// Get returns the event mrid in its current state.
func (s *Scheduler) Get(mrid string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	en, ok := s.events[mrid]
	if !ok {
		return Event{}, false
	}
	return en.ev, true
}

// Events returns every event the Scheduler knows, in order of precedence.
func (s *Scheduler) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	evs := make([]Event, 0, len(s.events))
	for _, en := range s.events {
		evs = append(evs, en.ev)
	}
	slices.SortFunc(evs, compare)
	return evs
}

// Window returns the span in which the event mrid is planned to be active:
// its randomized interval, cut short by an event that supersedes it or by
// a randomized cancellation. ok is false when the event is not planned to
// be active, because it is unknown or in a terminal state.
func (s *Scheduler) Window(mrid string) (start, end time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	en, found := s.events[mrid]
	if !found || en.ev.Status.Terminal() {
		return time.Time{}, time.Time{}, false
	}
	return en.ws, en.we, true
}

// Active returns the events planned to be active at at, in order of
// precedence, with their current state. For a time in the future, this is
// the plan from the events known now.
func (s *Scheduler) Active(at time.Time) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	return s.activeAt(at)
}

func (s *Scheduler) activeAt(at time.Time) []Event {
	var evs []Event
	for _, en := range s.events {
		if !at.Before(en.ws) && at.Before(en.we) {
			evs = append(evs, en.ev)
		}
	}
	slices.SortFunc(evs, compare)
	return evs
}

// Timeline divides [from, to) into the segments in which the same events
// are planned to be active, as Active reports them.
func (s *Scheduler) Timeline(from, to time.Time) []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !from.Before(to) {
		return nil
	}
	s.refresh()
	cuts := []time.Time{from, to}
	for _, en := range s.events {
		for _, t := range []time.Time{en.ws, en.we} {
			if t.After(from) && t.Before(to) {
				cuts = append(cuts, t)
			}
		}
	}
	slices.SortFunc(cuts, time.Time.Compare)
	cuts = slices.CompactFunc(cuts, time.Time.Equal)

	var segs []Segment
	for i := 0; i+1 < len(cuts); i++ {
		evs := s.activeAt(cuts[i])
		if n := len(segs); n > 0 && sameEvents(segs[n-1].Events, evs) {
			segs[n-1].End = cuts[i+1]
			continue
		}
		segs = append(segs, Segment{Start: cuts[i], End: cuts[i+1], Events: evs})
	}
	return segs
}

// overlap reports whether [s1, e1) and [s2, e2) share an instant. An empty
// span overlaps nothing.
func overlap(s1, e1, s2, e2 time.Time) bool {
	return s1.Before(e1) && s2.Before(e2) && s1.Before(e2) && s2.Before(e1)
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func sameEvents(a, b []Event) bool {
	return slices.EqualFunc(a, b, func(x, y Event) bool { return x.MRID == y.MRID })
}

// Subscribe returns a channel of every transition from now on, in order,
// and a function that ends the subscription and closes the channel. The
// Scheduler never waits for a subscriber: transitions queue until it reads
// them, up to Options.QueueSize, beyond which the queue keeps the latest
// transition of each event. Close ends every subscription.
func (s *Scheduler) Subscribe() (<-chan Transition, func()) {
	sub, stop := s.subscribe()
	return sub.ch, stop
}

func (s *Scheduler) subscribe() (*subscriber, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &subscriber{
		ch:     make(chan Transition),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
		size:   s.queueSize,
	}
	if s.closed {
		close(sub.ch)
		close(sub.exited)
		return sub, func() {}
	}
	s.subs[sub] = struct{}{}
	s.wg.Go(func() {
		sub.run()
	})
	return sub, func() {
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
		sub.stop()
	}
}

// publish queues out for every subscriber. The caller holds s.mu.
func (s *Scheduler) publish(out []Transition) {
	if len(out) == 0 {
		return
	}
	for sub := range s.subs {
		sub.push(out, &s.stats)
	}
}

// Stats returns the counters of s.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Close stops the Scheduler: it ends every subscription, dropping the
// transitions not yet read, and waits for their goroutines to finish.
// Afterwards Upsert, Cancel and Remove do nothing. Close may be called more
// than once, and from several goroutines at once.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for sub := range subs {
		sub.stop()
	}
	s.wg.Wait()
}

// subscriber delivers transitions to one channel from a queue of at most
// size transitions (no bound when size is 0).
type subscriber struct {
	ch   chan Transition
	size int

	mu    sync.Mutex
	queue []Transition
	// sending is whether run holds a transition it has taken from the
	// queue and not yet delivered; sent counts those it delivered.
	sending bool
	sent    int

	wake   chan struct{}
	done   chan struct{}
	once   sync.Once
	exited chan struct{}
}

// push queues ts, making room as Options.QueueSize says, and counts in st
// what it leaves out. The caller holds the Scheduler's lock.
func (sub *subscriber) push(ts []Transition, st *Stats) {
	sub.mu.Lock()
	for _, t := range ts {
		if sub.size > 0 && len(sub.queue) >= sub.size {
			sub.compact(st)
		}
		sub.queue = append(sub.queue, t)
	}
	sub.mu.Unlock()
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

// compact makes room in a full queue: it keeps the latest transition of
// each event and, when more than three quarters of the queue's size are
// left, the newest of them. The caller holds sub.mu.
func (sub *subscriber) compact(st *Stats) {
	latest := make(map[string]int, len(sub.queue))
	for i, t := range sub.queue {
		latest[t.Event.MRID] = i
	}
	kept := sub.queue[:0]
	for i, t := range sub.queue {
		if latest[t.Event.MRID] == i {
			kept = append(kept, t)
		} else {
			st.Coalesced++
		}
	}
	if over := len(kept) - sub.size*3/4; over > 0 {
		st.Dropped += uint64(over)
		kept = kept[:copy(kept, kept[over:])]
	}
	clear(sub.queue[len(kept):])
	sub.queue = kept
}

func (sub *subscriber) run() {
	defer close(sub.exited)
	defer close(sub.ch)
	for {
		sub.mu.Lock()
		if len(sub.queue) == 0 {
			sub.queue = nil
			sub.mu.Unlock()
			select {
			case <-sub.wake:
				continue
			case <-sub.done:
				return
			}
		}
		t := sub.queue[0]
		sub.queue[0] = Transition{}
		sub.queue = sub.queue[1:]
		sub.sending = true
		sub.mu.Unlock()
		select {
		case sub.ch <- t:
			sub.mu.Lock()
			sub.sending = false
			sub.sent++
			sub.mu.Unlock()
		case <-sub.done:
			return
		}
	}
}

// stop ends the subscription and waits for its goroutine.
func (sub *subscriber) stop() {
	sub.once.Do(func() { close(sub.done) })
	<-sub.exited
}
