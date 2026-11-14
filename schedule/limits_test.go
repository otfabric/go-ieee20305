// SPDX-License-Identifier: MIT

package schedule

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/leakcheck"
)

// TestNewLimits: the bounds New derives from Options, from zero, one,
// negative values and the ends of int, where four times MaxEvents does not
// fit.
func TestNewLimits(t *testing.T) {
	tests := []struct {
		maxEvents, queueSize int
		wantMax, wantQueue   int
	}{
		{0, 0, DefaultMaxEvents, 4 * DefaultMaxEvents},
		{1, 0, 1, 4},
		{2, 0, 2, 8},
		{DefaultMaxEvents - 1, 0, DefaultMaxEvents - 1, 4 * (DefaultMaxEvents - 1)},
		{DefaultMaxEvents + 1, 0, DefaultMaxEvents + 1, 4 * (DefaultMaxEvents + 1)},
		{-1, 0, 0, 0},
		{math.MinInt, 0, 0, 0},
		{-1, 5, 0, 5},
		{0, -1, DefaultMaxEvents, 0},
		{0, math.MinInt, DefaultMaxEvents, 0},
		{3, 1, 3, 1},
		{math.MaxInt32, 7, math.MaxInt32, 7},
		{math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt},
	}
	for _, tt := range tests {
		s := New(Options{MaxEvents: tt.maxEvents, QueueSize: tt.queueSize})
		if s.maxEvents != tt.wantMax || s.queueSize != tt.wantQueue {
			t.Errorf("MaxEvents %d, QueueSize %d: bounds %d, %d; want %d, %d",
				tt.maxEvents, tt.queueSize, s.maxEvents, s.queueSize, tt.wantMax, tt.wantQueue)
		}
		s.Close()
	}
	// Four times the largest MaxEvents does not fit in an int: the queue is
	// bounded all the same.
	for _, n := range []int{math.MaxInt32, math.MaxInt / 4, math.MaxInt/4 + 1, math.MaxInt} {
		s := New(Options{MaxEvents: n})
		if s.queueSize < min(n, math.MaxInt/4) || s.queueSize <= 0 {
			t.Errorf("MaxEvents %d: queue size %d", n, s.queueSize)
		}
		s.Close()
	}
}

// fillEvents upserts n new events that start from seconds after t0 and do
// not overlap, and returns the number refused.
func fillEvents(s *Scheduler, prefix string, from, n int) int {
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = ev(fmt.Sprintf("%s%d", prefix, i), 1, i, from+60*i, 30)
	}
	return s.UpsertAll(evs)
}

// TestMaxEvents: a Scheduler keeps MaxEvents events, refuses and counts a
// new one beyond them, still takes updates of those it keeps, and has room
// again after a removal.
func TestMaxEvents(t *testing.T) {
	for _, tt := range []struct {
		name      string
		maxEvents int
		keeps     int
	}{
		{"default", 0, DefaultMaxEvents},
		{"one", 1, 1},
		{"two", 2, 2},
		{"three", 3, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakeClock(t0)
			s := New(Options{Clock: c, MaxEvents: tt.maxEvents})
			defer s.Close()
			// max-1 and max: kept.
			fillEvents(s, "E", 3600, tt.keeps-1)
			if n := len(s.Events()); n != tt.keeps-1 || s.Stats().Refused != 0 {
				t.Fatalf("max-1: %d events, %+v", n, s.Stats())
			}
			fillEvents(s, "F", 1000000, 1)
			if n := len(s.Events()); n != tt.keeps || s.Stats().Refused != 0 {
				t.Fatalf("max: %d events, %+v", n, s.Stats())
			}
			// max+1: refused and counted, with no transition.
			r := record(s)
			s.Upsert(ev("X", 1, 0, 0, 30))
			if _, ok := s.Get("X"); ok || len(s.Events()) != tt.keeps || s.Stats().Refused != 1 {
				t.Fatalf("max+1: kept %t, %d events, %+v", ok, len(s.Events()), s.Stats())
			}
			// An update at the cap is taken.
			s.Upsert(ev("F0", 1, 0, 0, 30))
			if e, _ := s.Get("F0"); e.Status != Active {
				t.Fatalf("update at the cap: F0 is %s", e.Status)
			}
			if got := describe(r.wait(t, 1)); got != "F0:Scheduled>Active@0" {
				t.Fatalf("transitions %s", got)
			}
			// UpsertAll refuses each new event beyond the cap.
			if n := s.UpsertAll([]Event{ev("Y", 1, 0, 0, 30), ev("F0", 1, 0, 0, 40), ev("Z", 1, 0, 0, 30)}); n != 2 || s.Stats().Refused != 3 {
				t.Fatalf("UpsertAll refused %d, %+v", n, s.Stats())
			}
			// A removal makes room for one.
			if !s.Remove("F0") {
				t.Fatal("Remove F0")
			}
			if n := s.UpsertAll([]Event{ev("Y", 1, 0, 100, 30), ev("Z", 1, 0, 200, 30)}); n != 1 || len(s.Events()) != tt.keeps {
				t.Fatalf("after Remove: refused %d, %d events", n, len(s.Events()))
			}
			if _, ok := s.Get("Y"); !ok {
				t.Fatal("Y not kept after Remove")
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		c := newFakeClock(t0)
		s := New(Options{Clock: c, MaxEvents: -1})
		defer s.Close()
		fillEvents(s, "E", 3600, DefaultMaxEvents+1)
		if n := len(s.Events()); n != DefaultMaxEvents+1 || s.Stats().Refused != 0 {
			t.Fatalf("%d events, %+v", n, s.Stats())
		}
	})
}

// countingClock counts the reads of the time.
type countingClock struct {
	*fakeClock
	nows atomic.Int64
}

func (c *countingClock) Now() time.Time {
	c.nows.Add(1)
	return c.fakeClock.Now()
}

// TestUpsertAllResolvesOnce: UpsertAll takes the events of a poll at one
// time and resolves them once, not once per event.
func TestUpsertAllResolvesOnce(t *testing.T) {
	const n = 4000
	c := &countingClock{fakeClock: newFakeClock(t0)}
	s := New(Options{Clock: c, MaxEvents: n})
	defer s.Close()
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = ev(fmt.Sprintf("E%d", i), 1, i, 3600+60*i, 30)
	}
	if refused := s.UpsertAll(evs); refused != 0 {
		t.Fatalf("refused %d", refused)
	}
	if got := c.nows.Load(); got != 1 {
		t.Fatalf("UpsertAll of %d events read the clock %d times, want 1", n, got)
	}
	if s.evaluations != 1 {
		t.Fatalf("UpsertAll of %d events resolved them %d times, want 1", n, s.evaluations)
	}
	if got := len(s.Events()); got != n {
		t.Fatalf("%d events", got)
	}
}

// TestEvaluationCost: resolving n events compares each with the windows
// that can overlap it, not with every window: for events that do not
// overlap, or overlap only their neighbours, at most two comparisons per
// event, whatever the order of precedence.
func TestEvaluationCost(t *testing.T) {
	const n = 4000
	never := func(a, b Event) bool { return false }
	for _, tt := range []struct {
		name      string
		dur       int
		conflicts func(a, b Event) bool
		planned   int
	}{
		{"apart", 30, nil, n},
		{"neighbours, no conflicts", 90, never, n},
		{"neighbours, conflicts", 90, nil, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakeClock(t0)
			s := New(Options{Clock: c, MaxEvents: n, Conflicts: tt.conflicts})
			defer s.Close()
			evs := make([]Event, n)
			for i := range evs {
				// creation times in an order unrelated to the start times
				created := (i * 7919) % n
				evs[i] = ev(fmt.Sprintf("E%d", i), 1, created, 3600+60*i, tt.dur)
			}
			s.UpsertAll(evs)
			if s.evaluations != 1 || s.checks > 2*n {
				t.Fatalf("%d evaluations, %d comparisons for %d events; want 1 and at most %d", s.evaluations, s.checks, n, 2*n)
			}
			planned := 0
			s.mu.Lock()
			for _, en := range s.events {
				if !en.ws.IsZero() {
					planned++
				}
			}
			s.mu.Unlock()
			if tt.planned >= 0 && planned != tt.planned {
				t.Fatalf("%d events planned, want %d", planned, tt.planned)
			}
			if planned == 0 {
				t.Fatal("no event planned")
			}
		})
	}
}

// TestUpsertAllOrder: UpsertAll resolves the events of a call together, in
// any order. One by one, an event can lose to an event that a later one
// supersedes; together, it is resolved against those that remain.
func TestUpsertAllOrder(t *testing.T) {
	// A overlaps B, and B overlaps C; A does not overlap C. A precedes B,
	// and B precedes C.
	a, b, cc := ev("A", 1, 0, 100, 60), ev("B", 2, 0, 150, 60), ev("C", 3, 0, 200, 60)
	together := "A=Scheduled B=Superseded C=Scheduled"
	for _, order := range [][]Event{{a, b, cc}, {cc, b, a}, {b, cc, a}} {
		c := newFakeClock(t0)
		s := New(Options{Clock: c})
		if s.UpsertAll(order); statuses(s) != together {
			t.Errorf("UpsertAll(%s): %s, want %s", mrids(order), statuses(s), together)
		}
		s.Close()
	}
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	defer s.Close()
	for _, e := range []Event{cc, b, a} {
		s.Upsert(e)
	}
	if got, want := statuses(s), "A=Scheduled B=Superseded C=Superseded"; got != want {
		t.Errorf("one by one: %s, want %s", got, want)
	}
}

// TestRemoveAll: RemoveAll forgets the events it knows, counts them, and
// resolves the rest once.
func TestRemoveAll(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	s.UpsertAll([]Event{ev("A", 1, 0, 100, 60), ev("B", 2, 0, 150, 60), ev("C", 3, 0, 300, 60)})
	before := s.evaluations
	if n := s.RemoveAll([]string{"A", "X", "C", "A"}); n != 2 {
		t.Fatalf("RemoveAll = %d, want 2", n)
	}
	if s.evaluations != before+1 {
		t.Fatalf("%d evaluations", s.evaluations-before)
	}
	if got := statuses(s); got != "B=Superseded" {
		t.Fatalf("after RemoveAll: %s", got)
	}
	before = s.evaluations
	if n := s.RemoveAll([]string{"X"}); n != 0 || s.evaluations != before {
		t.Fatalf("RemoveAll of unknown events = %d, %d evaluations", n, s.evaluations-before)
	}
	if n := s.RemoveAll(nil); n != 0 {
		t.Fatalf("RemoveAll(nil) = %d", n)
	}
	s.Close()
	if n := s.RemoveAll([]string{"B"}); n != 0 {
		t.Fatalf("RemoveAll after Close = %d", n)
	}
	if n := s.UpsertAll([]Event{ev("D", 1, 0, 0, 10)}); n != 0 || statuses(s) != "B=Superseded" {
		t.Fatalf("UpsertAll after Close: %d, %s", n, statuses(s))
	}
}

// tr is a transition of event mrid to state to, at t0.
func tr(mrid string, to Status) Transition {
	return Transition{Event: Event{MRID: mrid, Status: to}, From: Scheduled, To: to, At: t0}
}

func queued(sub *subscriber) string {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	var ids []string
	for _, t := range sub.queue {
		ids = append(ids, t.Event.MRID+"="+t.To.String())
	}
	return strings.Join(ids, " ")
}

// TestQueueCompaction: a full queue keeps the latest transition of each
// event and, when more than three quarters of its size are left, drops the
// oldest of them; it never holds more than its size, and counts what it
// leaves out.
func TestQueueCompaction(t *testing.T) {
	for _, tt := range []struct {
		name  string
		size  int
		push  []Transition
		want  string
		stats Stats
	}{
		{"none", 0, []Transition{tr("A", Scheduled), tr("A", Active), tr("A", Completed)},
			"A=Scheduled A=Active A=Completed", Stats{}},
		{"below size", 4, []Transition{tr("A", Scheduled), tr("A", Active), tr("B", Scheduled), tr("A", Completed)},
			"A=Scheduled A=Active B=Scheduled A=Completed", Stats{}},
		{"coalesced", 4, []Transition{tr("A", Scheduled), tr("B", Scheduled), tr("A", Active), tr("C", Scheduled), tr("D", Scheduled)},
			"B=Scheduled A=Active C=Scheduled D=Scheduled", Stats{Coalesced: 1}},
		{"dropped", 4, []Transition{tr("A", Scheduled), tr("B", Scheduled), tr("C", Scheduled), tr("D", Scheduled), tr("E", Scheduled)},
			"B=Scheduled C=Scheduled D=Scheduled E=Scheduled", Stats{Dropped: 1}},
		{"coalesced and dropped", 4, []Transition{tr("A", Scheduled), tr("A", Active), tr("B", Scheduled), tr("C", Scheduled), tr("D", Scheduled), tr("E", Scheduled)},
			"B=Scheduled C=Scheduled D=Scheduled E=Scheduled", Stats{Coalesced: 1, Dropped: 1}},
		{"size one", 1, []Transition{tr("A", Scheduled), tr("A", Active), tr("B", Scheduled)},
			"B=Scheduled", Stats{Coalesced: 0, Dropped: 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sub := &subscriber{size: tt.size, wake: make(chan struct{}, 1)}
			var st Stats
			for _, x := range tt.push {
				sub.push([]Transition{x}, &st)
				if tt.size > 0 && len(sub.queue) > tt.size {
					t.Fatalf("%d queued, size %d", len(sub.queue), tt.size)
				}
			}
			if got := queued(sub); got != tt.want || st != tt.stats {
				t.Fatalf("queue %q, %+v; want %q, %+v", got, st, tt.want, tt.stats)
			}
		})
	}
	// One push of more than the size: still bounded.
	sub := &subscriber{size: 2, wake: make(chan struct{}, 1)}
	var st Stats
	sub.push([]Transition{tr("A", Scheduled), tr("B", Scheduled), tr("C", Scheduled), tr("D", Scheduled), tr("E", Scheduled)}, &st)
	if got := queued(sub); got != "D=Scheduled E=Scheduled" || st.Dropped != 3 {
		t.Fatalf("queue %q, %+v", got, st)
	}
}

// TestQueueKeepsUp: a subscriber that reads what each call queues before
// the next sees every transition, with the default queue size, at the cap
// of events.
func TestQueueKeepsUp(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c, MaxEvents: 3})
	defer s.Close()
	r := record(s)
	n := 0
	step := func(want int, f func()) {
		t.Helper()
		f()
		n += want
		r.wait(t, n)
	}
	// The first two queue two transitions per event, the most one call
	// can: received, then active or superseded.
	step(6, func() { s.UpsertAll([]Event{ev("A", 1, 0, 0, 10), ev("B", 2, 0, 0, 10), ev("C", 3, 0, 0, 10)}) })
	step(6, func() {
		s.RemoveAll([]string{"A", "B", "C"})
		s.UpsertAll([]Event{ev("D", 1, 0, 0, 10), ev("E", 2, 0, 0, 10), ev("F", 3, 0, 0, 10)})
	})
	step(1, func() { s.UpsertAll([]Event{upd(ev("D", 1, 0, 0, 10), Cancelled), ev("E", 2, 0, 0, 10)}) })
	step(0, func() { c.Advance(secs(60)) })
	if st := s.Stats(); st != (Stats{}) {
		t.Fatalf("Stats = %+v", st)
	}
}

// upd returns e with Status st.
func upd(e Event, st Status) Event { e.Status = st; return e }

// TestQueueBound: the queue of a subscriber that does not read stays
// within its size, and what it leaves out is counted: once read, it has
// delivered or counted every transition of a Scheduler whose queue has no
// bound, and the last transition of each event is the event's state.
func TestQueueBound(t *testing.T) {
	c, cref := newFakeClock(t0), newFakeClock(t0)
	s := New(Options{Clock: c, MaxEvents: 3, QueueSize: 4})
	ref := New(Options{Clock: cref, MaxEvents: 3, QueueSize: -1})
	defer s.Close()
	defer ref.Close()
	sub, stop := s.subscribe()
	defer stop()
	all, stopAll := ref.subscribe()
	defer stopAll()
	for i := range 20 {
		evs := []Event{ev("A", 1, i, 10*i, 5), ev("B", 2, i, 10*i+2, 5), ev("C", 3, i, 10*i+4, 20)}
		s.UpsertAll(evs)
		ref.UpsertAll(evs)
		c.Advance(secs(10))
		cref.Advance(secs(10))
		sub.mu.Lock()
		n := len(sub.queue)
		sub.mu.Unlock()
		if n > 4 {
			t.Fatalf("%d queued, size 4", n)
		}
	}
	got, want := drain(t, sub), drain(t, all)
	st := s.Stats()
	if st.Coalesced == 0 || uint64(len(want)) != uint64(len(got))+st.Coalesced+st.Dropped {
		t.Fatalf("%d transitions, %d delivered, %+v", len(want), len(got), st)
	}
	last := map[string]Status{}
	for _, x := range got {
		last[x.Event.MRID] = x.To
	}
	for _, e := range s.Events() {
		if last[e.MRID] != e.Status {
			t.Errorf("%s: last transition to %s, state %s", e.MRID, last[e.MRID], e.Status)
		}
	}
}

// drain reads the transitions of sub until none is left; the Scheduler's
// calls have all returned.
func drain(t *testing.T, sub *subscriber) []Transition {
	t.Helper()
	timeout := time.After(5 * time.Second)
	var ts []Transition
	for len(ts) == 0 || sub.pending(len(ts)) > 0 {
		select {
		case x := <-sub.ch:
			ts = append(ts, x)
		case <-timeout:
			t.Fatalf("%d transitions, then none", len(ts))
		}
	}
	return ts
}

// TestNoConflicts: with Options.NoConflicts no event supersedes another,
// Conflicts is not called, and events that all overlap are resolved without
// comparing them with each other.
func TestNoConflicts(t *testing.T) {
	const n = 4000
	c := newFakeClock(t0)
	s := New(Options{Clock: c, MaxEvents: -1, NoConflicts: true, Conflicts: func(a, b Event) bool {
		t.Error("Conflicts called")
		return true
	}})
	defer s.Close()
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = ev(fmt.Sprintf("E%d", i), uint8(i%3), i, 3600, 3600)
	}
	if refused := s.UpsertAll(evs); refused != 0 {
		t.Fatalf("refused %d", refused)
	}
	if s.evaluations != 1 || s.checks != 0 {
		t.Fatalf("%d evaluations, %d comparisons for %d events that all overlap; want 1 and 0", s.evaluations, s.checks, n)
	}
	c.Advance(secs(3600))
	if got := len(s.Active(c.Now())); got != n {
		t.Fatalf("%d events active, want %d", got, n)
	}
	if s.checks != 0 {
		t.Fatalf("%d comparisons", s.checks)
	}
	c.Advance(secs(3600))
	for _, e := range s.Events() {
		if e.Status != Completed {
			t.Fatalf("%s is %s after its end", e.MRID, e.Status)
		}
	}
	// The same events, all of which conflict: one is active.
	all := New(Options{Clock: newFakeClock(at(3600)), MaxEvents: -1})
	defer all.Close()
	all.UpsertAll(evs[:100])
	if got := len(all.Active(at(3600))); got != 1 {
		t.Fatalf("%d conflicting events active, want 1", got)
	}
}

// lateClock is a clock whose timers never go off.
type lateClock struct{ *fakeClock }

func (lateClock) AfterFunc(time.Duration, func()) Stopper { return neverFires{} }

type neverFires struct{}

func (neverFires) Stop() bool { return true }

// TestQueriesResolveWhenDue: Get, Events, Window, Active and Timeline
// resolve the events again only when a change is due: not before the
// instant of the next change, once when the timer for it is late, and
// never again when no event can change.
func TestQueriesResolveWhenDue(t *testing.T) {
	c := lateClock{newFakeClock(t0)}
	s := New(Options{Clock: c})
	defer s.Close()
	s.UpsertAll([]Event{ev("A", 1, 0, 100, 60), ev("B", 1, 1, 300, 60)})
	query := func() {
		s.Get("A")
		s.Events()
		s.Window("A")
		s.Active(c.Now())
		s.Timeline(t0, at(1000))
	}
	for _, step := range []struct {
		advance  int
		resolved uint64 // resolutions the queries of the step make
		want     string
	}{
		{0, 0, "A=Scheduled B=Scheduled"},
		{99, 0, "A=Scheduled B=Scheduled"},
		{1, 1, "A=Active B=Scheduled"}, // the start of A
		{59, 0, "A=Active B=Scheduled"},
		{1, 1, "A=Completed B=Scheduled"}, // the end of A
		{500, 1, "A=Completed B=Completed"},
		{500, 0, "A=Completed B=Completed"}, // nothing left to change
	} {
		c.Advance(secs(step.advance))
		before := s.evaluations
		query()
		query()
		if got := s.evaluations - before; got != step.resolved {
			t.Fatalf("at %v: the queries resolved the events %d times, want %d", c.Now().Sub(t0), got, step.resolved)
		}
		if got := statuses(s); got != step.want {
			t.Fatalf("at %v: %s, want %s", c.Now().Sub(t0), got, step.want)
		}
	}
}

// TestCloseTwice: Close may be called more than once, and from several
// goroutines at once, also while subscribers wait and events are upserted
// (contract rows L1 and L2). Each Close returns once the subscriptions have
// ended, and afterwards the Scheduler takes and reports nothing.
func TestCloseTwice(t *testing.T) {
	leakcheck.Check(t)
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	// One subscriber reads, one does not, and one has ended already.
	reading, _ := s.Subscribe()
	idle, _ := s.Subscribe()
	_, stop := s.Subscribe()
	stop()
	stop()
	var readers sync.WaitGroup
	readers.Go(func() {
		for range reading {
		}
	})
	s.UpsertAll([]Event{ev("A", 1, 0, 0, 10), ev("B", 1, 1, 100, 10)})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				s.Upsert(ev(fmt.Sprintf("C%d", i), 1, 2, 200, 10))
			}
			s.Close()
			s.Close()
		})
	}
	close(start)
	wg.Wait()
	readers.Wait()
	if _, ok := <-idle; ok {
		// A transition queued before Close may still be handed over; the
		// channel is closed after it.
		for range idle {
		}
	}
	s.Close()
	before := len(s.Events())
	s.Upsert(ev("D", 1, 3, 300, 10))
	s.Cancel("A", false)
	if s.Remove("A") || s.UpsertAll([]Event{ev("E", 1, 4, 400, 10)}) != 0 || len(s.Events()) != before {
		t.Fatalf("a closed Scheduler changed: %s", statuses(s))
	}
	ch, end := s.Subscribe()
	if _, ok := <-ch; ok {
		t.Fatal("a subscription of a closed Scheduler delivers")
	}
	end()
}

// TestPlaceWallClock: the interval of an event is kept in wall-clock time,
// whether its start carries a monotonic reading (a time.Now) or not, so
// that the order of the planned windows does not depend on how the times
// were made.
func TestPlaceWallClock(t *testing.T) {
	now := time.Now() // with a monotonic reading
	s := New(Options{Seed: 1, Randomize: true})
	defer s.Close()
	s.UpsertAll([]Event{
		{MRID: "A", Start: now.Add(time.Hour), Duration: time.Minute, RandStart: 30, RandDuration: 30},
		{MRID: "B", Start: time.Unix(now.Unix()+7200, 0), Duration: time.Minute},
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	for mrid, en := range s.events {
		for _, at := range []time.Time{en.start, en.end, en.ws, en.we} {
			if at != at.Round(0) {
				t.Errorf("%s: %v carries a monotonic reading", mrid, at)
			}
		}
	}
	if a := s.events["A"]; a.start.Before(now.Add(time.Hour).Round(0)) || a.start.After(now.Add(time.Hour+30*time.Second)) || !a.end.After(a.start) {
		t.Errorf("A is placed at %v to %v", a.start, a.end)
	}
}
