// SPDX-License-Identifier: MIT

package schedule

import (
	"cmp"
	"hash/fnv"
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
	// order in which events arrive.
	Seed uint64
	// Randomize applies the randomizeStart and randomizeDuration of the
	// events, as a client does. A server leaves it off.
	Randomize bool
	// Conflicts reports whether two events compete for the same thing,
	// so that one can supersede the other. When nil, all events
	// conflict. See DERConflicts and DRLCConflicts.
	Conflicts func(a, b Event) bool
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

	mu      sync.Mutex
	events  map[string]*entry
	subs    map[*subscriber]struct{}
	timer   Stopper
	timerAt time.Time
	gen     uint64
	closed  bool
	wg      sync.WaitGroup
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
		clock:     o.Clock,
		seed:      o.Seed,
		randomize: o.Randomize,
		conflicts: o.Conflicts,
		events:    make(map[string]*entry),
		subs:      make(map[*subscriber]struct{}),
	}
	if s.clock == nil {
		s.clock = SystemClock
	}
	if s.conflicts == nil {
		s.conflicts = AllConflict
	}
	return s
}

// rng returns the random source of the event mrid.
func (s *Scheduler) rng(mrid string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(mrid))
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

// place sets the randomized interval of en from its event.
func (s *Scheduler) place(en *entry) {
	en.start, en.end = en.ev.Start, en.ev.End()
	if !s.randomize {
		return
	}
	r := s.rng(en.ev.MRID)
	rs := draw(r, en.ev.RandStart)
	d := en.ev.Duration + draw(r, en.ev.RandDuration)
	en.start = en.ev.Start.Add(rs)
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
// state.
func (s *Scheduler) Upsert(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	now := s.clock.Now()
	var out []Transition
	in := e.Status
	en, ok := s.events[e.MRID]
	if ok {
		e.Status = en.ev.Status
		en.ev = e
		s.place(en)
		if e.Status.Terminal() {
			return
		}
		switch in {
		case Cancelled:
			s.cancel(en, false, now, &out)
		case CancelledRandom:
			s.cancel(en, true, now, &out)
		case Superseded, Completed:
			s.set(en, in, now, &out)
		default:
		}
	} else {
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
		out = append(out, Transition{Event: en.ev, From: init, To: init, At: now})
	}
	s.evaluate(now, &out)
	s.publish(out)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.events[mrid]; s.closed || !ok {
		return false
	}
	delete(s.events, mrid)
	var out []Transition
	s.evaluate(s.clock.Now(), &out)
	s.publish(out)
	return true
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
func (s *Scheduler) evaluate(now time.Time, out *[]Transition) {
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

	accepted := make([]*entry, 0, len(live))
	for _, en := range live {
		start, end := en.start, en.end
		if !en.cancelAt.IsZero() {
			end = earlier(end, en.cancelAt)
		}
		en.ws, en.we = time.Time{}, time.Time{}
		running := !now.Before(start) && now.Before(end)
		lost := false
		var cutoff time.Time
		for _, w := range accepted {
			if !overlap(start, end, w.ws, w.we) || !s.conflicts(w.ev, en.ev) {
				continue
			}
			if !running {
				lost = true
				break
			}
			if c := later(w.ws, now); cutoff.IsZero() || c.Before(cutoff) {
				cutoff = c
			}
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
			accepted = append(accepted, en)
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
// late.
func (s *Scheduler) refresh() {
	if s.closed {
		return
	}
	var out []Transition
	s.evaluate(s.clock.Now(), &out)
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
// them. Close ends every subscription.
func (s *Scheduler) Subscribe() (<-chan Transition, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		ch := make(chan Transition)
		close(ch)
		return ch, func() {}
	}
	sub := &subscriber{
		ch:     make(chan Transition),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	s.subs[sub] = struct{}{}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sub.run()
	}()
	return sub.ch, func() {
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
		sub.stop()
	}
}

func (s *Scheduler) publish(out []Transition) {
	if len(out) == 0 {
		return
	}
	for sub := range s.subs {
		sub.push(out)
	}
}

// Close stops the Scheduler: it ends every subscription, dropping the
// transitions not yet read, and waits for their goroutines to finish.
// Afterwards Upsert, Cancel and Remove do nothing.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
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

// subscriber delivers transitions to one channel from an unbounded queue.
type subscriber struct {
	ch     chan Transition
	mu     sync.Mutex
	queue  []Transition
	wake   chan struct{}
	done   chan struct{}
	once   sync.Once
	exited chan struct{}
}

func (sub *subscriber) push(ts []Transition) {
	sub.mu.Lock()
	sub.queue = append(sub.queue, ts...)
	sub.mu.Unlock()
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (sub *subscriber) run() {
	defer close(sub.exited)
	defer close(sub.ch)
	for {
		sub.mu.Lock()
		q := sub.queue
		sub.queue = nil
		sub.mu.Unlock()
		for _, t := range q {
			select {
			case sub.ch <- t:
			case <-sub.done:
				return
			}
		}
		select {
		case <-sub.wake:
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
