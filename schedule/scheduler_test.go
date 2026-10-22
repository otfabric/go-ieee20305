// SPDX-License-Identifier: MIT

package schedule

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// at returns t0 plus sec seconds.
func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func secs(n int) time.Duration { return time.Duration(n) * time.Second }

// ev returns an event mrid of the given primacy, created at created, over
// [start, start+dur), in seconds from t0.
func ev(mrid string, primacy uint8, created, start, dur int) Event {
	return Event{MRID: mrid, Primacy: primacy, Created: at(created), Start: at(start), Duration: secs(dur)}
}

// recorder collects the transitions of a subscription.
type recorder struct {
	mu sync.Mutex
	ts []Transition
}

func record(s *Scheduler) *recorder {
	r := &recorder{}
	ch, _ := s.Subscribe()
	go func() {
		for t := range ch {
			r.mu.Lock()
			r.ts = append(r.ts, t)
			r.mu.Unlock()
		}
	}()
	return r
}

// wait returns the transitions once there are n of them, and fails if more
// arrive shortly after.
func (r *recorder) wait(t *testing.T, n int) []Transition {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.ts)
		r.mu.Unlock()
		if got >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d transitions, want %d: %s", got, n, r.String())
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ts) != n {
		t.Fatalf("got %d transitions, want %d: %s", len(r.ts), n, r.string())
	}
	return slices.Clone(r.ts)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.string()
}

func (r *recorder) string() string { return describe(r.ts) }

func describe(ts []Transition) string {
	var b strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&b, "%s:%s>%s@%d ", t.Event.MRID, t.From, t.To, int(t.At.Sub(t0)/time.Second))
	}
	return strings.TrimSpace(b.String())
}

func statuses(s *Scheduler) string {
	var b strings.Builder
	evs := s.Events()
	slices.SortFunc(evs, func(a, b Event) int { return strings.Compare(a.MRID, b.MRID) })
	for _, e := range evs {
		fmt.Fprintf(&b, "%s=%s ", e.MRID, e.Status)
	}
	return strings.TrimSpace(b.String())
}

func mrids(evs []Event) string {
	var ids []string
	for _, e := range evs {
		ids = append(ids, e.MRID)
	}
	return strings.Join(ids, ",")
}

func TestStatus(t *testing.T) {
	for st, want := range map[Status]string{
		Scheduled: "Scheduled", Active: "Active", Cancelled: "Cancelled",
		CancelledRandom: "CancelledRandom", Superseded: "Superseded",
		Completed: "Completed", 9: "Status(9)",
	} {
		if st.String() != want {
			t.Errorf("%d: got %q, want %q", st, st.String(), want)
		}
	}
	for _, st := range []Status{Scheduled, Active, 9} {
		if st.Terminal() {
			t.Errorf("%s is terminal", st)
		}
	}
	for _, st := range []Status{Cancelled, CancelledRandom, Superseded, Completed} {
		if !st.Terminal() {
			t.Errorf("%s is not terminal", st)
		}
	}
}

func TestLifecycle(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	defer s.Close()
	r := record(s)
	s.Upsert(ev("A", 1, 0, 10, 60))
	c.Advance(secs(9))
	if e, _ := s.Get("A"); e.Status != Scheduled {
		t.Fatalf("at 9: %s", e.Status)
	}
	c.Advance(secs(1))
	if e, _ := s.Get("A"); e.Status != Active {
		t.Fatalf("at 10: %s", e.Status)
	}
	c.Advance(secs(100))
	got := describe(r.wait(t, 3))
	if want := "A:Scheduled>Scheduled@0 A:Scheduled>Active@10 A:Active>Completed@70"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if c.pending() != 0 {
		t.Fatalf("%d timers left", c.pending())
	}
	if _, ok := s.Get("B"); ok {
		t.Fatal("Get of an unknown event")
	}
}

// op is a step of a scenario.
type op struct {
	upsert  *Event
	cancel  string
	random  bool
	remove  string
	advance int
}

func up(e Event) op                  { return op{upsert: &e} }
func upStatus(e Event, st Status) op { e.Status = st; return op{upsert: &e} }
func cancel(m string) op             { return op{cancel: m} }
func cancelRandom(m string) op       { return op{cancel: m, random: true} }
func remove(m string) op             { return op{remove: m} }
func advance(sec int) op             { return op{advance: sec} }

func (o op) apply(c *fakeClock, s *Scheduler) {
	switch {
	case o.upsert != nil:
		s.Upsert(*o.upsert)
	case o.cancel != "":
		s.Cancel(o.cancel, o.random)
	case o.remove != "":
		s.Remove(o.remove)
	default:
		c.Advance(secs(o.advance))
	}
}

// groups returns a Conflicts in which events conflict when they share a
// letter of the group of their MRID: "a1" and "ab2" conflict, "a1" and
// "b1" do not.
func groups(a, b Event) bool {
	ga := strings.TrimRight(a.MRID, "0123456789")
	gb := strings.TrimRight(b.MRID, "0123456789")
	return strings.ContainsAny(ga, gb)
}

func TestScenarios(t *testing.T) {
	tests := []struct {
		name      string
		conflicts func(a, b Event) bool
		ops       []op
		want      string // statuses by MRID after the ops
		trans     string // all transitions, when not empty
	}{
		{
			name: "lower primacy wins",
			ops:  []op{up(ev("A", 2, 0, 10, 60)), up(ev("B", 1, 0, 30, 60)), advance(200)},
			want: "A=Superseded B=Completed",
		},
		{
			name: "lower primacy wins in either order",
			ops:  []op{up(ev("B", 1, 0, 30, 60)), up(ev("A", 2, 5, 10, 60)), advance(200)},
			want: "A=Superseded B=Completed",
		},
		{
			name: "same primacy: the newer wins",
			ops:  []op{up(ev("B", 1, 5, 30, 60)), up(ev("A", 1, 0, 10, 60)), advance(200)},
			want: "A=Superseded B=Completed",
		},
		{
			name: "tie on creation time: the lower MRID wins",
			ops:  []op{up(ev("B", 1, 0, 10, 60)), up(ev("A", 1, 0, 30, 60)), advance(200)},
			want: "A=Completed B=Superseded",
		},
		{
			name: "tie on creation time, other order",
			ops:  []op{up(ev("A", 1, 0, 30, 60)), up(ev("B", 1, 0, 10, 60)), advance(200)},
			want: "A=Completed B=Superseded",
		},
		{
			name: "no overlap",
			ops:  []op{up(ev("A", 2, 0, 10, 20)), up(ev("B", 1, 0, 40, 20)), advance(200)},
			want: "A=Completed B=Completed",
		},
		{
			name: "touching intervals do not overlap",
			ops:  []op{up(ev("A", 2, 0, 10, 20)), up(ev("B", 1, 0, 30, 20)), advance(200)},
			want: "A=Completed B=Completed",
		},
		{
			name:      "overlap without conflict",
			conflicts: groups,
			ops:       []op{up(ev("a1", 2, 0, 10, 60)), up(ev("b1", 1, 0, 30, 60)), advance(200)},
			want:      "a1=Completed b1=Completed",
		},
		{
			name: "an active event runs until the winner starts",
			ops:  []op{up(ev("A", 2, 0, 0, 100)), up(ev("B", 1, 1, 50, 100)), advance(300)},
			want: "A=Superseded B=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 B:Scheduled>Scheduled@0 " +
				"B:Scheduled>Active@50 A:Active>Superseded@50 B:Active>Completed@150",
		},
		{
			name: "an active event yields at once to a running winner",
			ops:  []op{up(ev("A", 2, 0, 0, 100)), advance(20), up(ev("B", 1, 1, 10, 100)), advance(300)},
			want: "A=Superseded B=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 B:Scheduled>Scheduled@20 " +
				"B:Scheduled>Active@20 A:Active>Superseded@20 B:Active>Completed@110",
		},
		{
			name: "supersession is final",
			ops:  []op{up(ev("A", 2, 0, 10, 60)), up(ev("B", 1, 0, 30, 60)), cancel("B"), remove("B"), advance(200)},
			want: "A=Superseded",
		},
		{
			name: "a superseded event supersedes nothing",
			ops: []op{
				up(ev("C", 3, 0, 0, 30)),  // overlaps B only
				up(ev("B", 2, 0, 20, 30)), // overlaps C and A; loses to A
				up(ev("A", 1, 0, 40, 30)),
				advance(200),
			},
			want: "A=Completed B=Superseded C=Completed",
		},
		{
			name: "supersession along a chain",
			ops: []op{
				up(ev("C", 3, 0, 0, 30)),
				up(ev("B", 2, 0, 20, 30)),
				advance(25), // C was superseded by B at 20
				up(ev("A", 1, 0, 40, 30)),
				advance(200),
			},
			want: "A=Completed B=Superseded C=Superseded",
		},
		{
			name: "removing the winner lets the active event run",
			ops:  []op{up(ev("A", 2, 0, 0, 100)), up(ev("B", 1, 1, 50, 100)), advance(20), remove("B"), advance(300)},
			want: "A=Completed",
		},
		{
			name: "cancelling the winner lets the active event run",
			ops:  []op{up(ev("A", 2, 0, 0, 100)), up(ev("B", 1, 1, 50, 100)), advance(20), cancel("B"), advance(300)},
			want: "A=Completed B=Cancelled",
		},
		{
			name:  "cancel during active",
			ops:   []op{up(ev("A", 1, 0, 0, 100)), advance(20), cancel("A"), advance(300)},
			want:  "A=Cancelled",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 A:Active>Cancelled@20",
		},
		{
			name:  "cancel with randomization, without Randomize, is at once",
			ops:   []op{up(ev("A", 1, 0, 0, 100)), advance(20), cancelRandom("A"), advance(300)},
			want:  "A=CancelledRandom",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 A:Active>CancelledRandom@20",
		},
		{
			name: "cancel of a scheduled event",
			ops:  []op{up(ev("A", 1, 0, 50, 100)), cancel("A"), cancel("A"), cancel("X"), advance(300)},
			want: "A=Cancelled",
		},
		{
			name: "cancel through Upsert",
			ops:  []op{up(ev("A", 1, 0, 0, 100)), upStatus(ev("A", 1, 0, 0, 100), Cancelled)},
			want: "A=Cancelled",
		},
		{
			name: "randomized cancel through Upsert",
			ops:  []op{up(ev("A", 1, 0, 0, 100)), upStatus(ev("A", 1, 0, 0, 100), CancelledRandom)},
			want: "A=CancelledRandom",
		},
		{
			name: "superseded by the server",
			ops:  []op{up(ev("A", 1, 0, 0, 100)), upStatus(ev("A", 1, 0, 0, 100), Superseded)},
			want: "A=Superseded",
		},
		{
			name: "completed by the server",
			ops:  []op{up(ev("A", 1, 0, 0, 100)), upStatus(ev("A", 1, 0, 0, 100), Completed)},
			want: "A=Completed",
		},
		{
			name:  "received cancelled",
			ops:   []op{upStatus(ev("A", 1, 0, 0, 100), Cancelled), advance(10)},
			want:  "A=Cancelled",
			trans: "A:Cancelled>Cancelled@0",
		},
		{
			name: "a server status of Active is ignored",
			ops:  []op{upStatus(ev("A", 1, 0, 50, 100), Active)},
			want: "A=Scheduled",
		},
		{
			name:  "an update of a terminal event keeps its state",
			ops:   []op{up(ev("A", 1, 0, 0, 10)), advance(20), up(ev("A", 1, 0, 50, 100)), advance(100)},
			want:  "A=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 A:Active>Completed@10",
		},
		{
			name:  "a repeated upsert changes nothing",
			ops:   []op{up(ev("A", 1, 0, 0, 100)), advance(10), up(ev("A", 1, 0, 0, 100)), advance(200)},
			want:  "A=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 A:Active>Completed@100",
		},
		{
			name: "an active event moved later is scheduled again",
			ops:  []op{up(ev("A", 1, 0, 0, 100)), advance(10), up(ev("A", 1, 0, 50, 100)), advance(200)},
			want: "A=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Active@0 A:Active>Scheduled@10 " +
				"A:Scheduled>Active@50 A:Active>Completed@150",
		},
		{
			name:  "an event in the past is received completed",
			ops:   []op{advance(500), up(ev("A", 1, 0, 0, 100)), advance(10)},
			want:  "A=Completed",
			trans: "A:Completed>Completed@500",
		},
		{
			name:  "an event received during its interval starts at once",
			ops:   []op{advance(50), up(ev("A", 1, 0, 0, 100)), advance(100)},
			want:  "A=Completed",
			trans: "A:Scheduled>Scheduled@50 A:Scheduled>Active@50 A:Active>Completed@100",
		},
		{
			name:  "zero duration completes at its start without being active",
			ops:   []op{up(ev("A", 1, 0, 10, 0)), advance(100)},
			want:  "A=Completed",
			trans: "A:Scheduled>Scheduled@0 A:Scheduled>Completed@10",
		},
		{
			name: "zero duration neither supersedes nor is superseded",
			ops:  []op{up(ev("A", 1, 0, 10, 0)), up(ev("B", 2, 0, 0, 100)), up(ev("C", 3, 0, 50, 0)), advance(200)},
			want: "A=Completed B=Completed C=Completed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakeClock(t0)
			s := New(Options{Clock: c, Conflicts: tt.conflicts})
			defer s.Close()
			r := record(s)
			for _, o := range tt.ops {
				o.apply(c, s)
			}
			if got := statuses(s); got != tt.want {
				t.Errorf("statuses: got %s, want %s", got, tt.want)
			}
			if tt.trans != "" {
				n := strings.Count(tt.trans, " ") + 1
				if got := describe(r.wait(t, n)); got != tt.trans {
					t.Errorf("transitions:\ngot  %s\nwant %s", got, tt.trans)
				}
			}
		})
	}
}

func TestActiveAndTimeline(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c, Conflicts: groups})
	defer s.Close()
	s.Upsert(ev("a1", 2, 0, 0, 100))  // cut short by a2 at 50
	s.Upsert(ev("a2", 1, 0, 50, 100)) // [50, 150)
	s.Upsert(ev("b1", 1, 0, 20, 40))  // [20, 60), no conflict with a*
	s.Upsert(ev("b2", 2, 0, 200, 10)) // [200, 210)

	for sec, want := range map[int]string{0: "a1", 19: "a1", 20: "b1,a1", 50: "a2,b1", 60: "a2", 150: "", 205: "b2", 210: ""} {
		if got := mrids(s.Active(at(sec))); got != want {
			t.Errorf("Active(%d): got %q, want %q", sec, got, want)
		}
	}
	if st, en, ok := s.Window("a1"); !ok || !st.Equal(at(0)) || !en.Equal(at(50)) {
		t.Errorf("Window(a1): %v %v %v", st, en, ok)
	}
	if _, _, ok := s.Window("x"); ok {
		t.Error("Window of an unknown event")
	}

	var b strings.Builder
	for _, seg := range s.Timeline(at(-10), at(300)) {
		fmt.Fprintf(&b, "[%d,%d)%s ", int(seg.Start.Sub(t0)/time.Second), int(seg.End.Sub(t0)/time.Second), mrids(seg.Events))
	}
	want := "[-10,0) [0,20)a1 [20,50)b1,a1 [50,60)a2,b1 [60,150)a2 [150,200) [200,210)b2 [210,300)"
	if got := strings.TrimSpace(b.String()); got != want {
		t.Errorf("Timeline:\ngot  %s\nwant %s", got, want)
	}
	if segs := s.Timeline(at(10), at(10)); segs != nil {
		t.Errorf("empty Timeline: %v", segs)
	}
	segs := s.Timeline(at(25), at(45))
	if len(segs) != 1 || mrids(segs[0].Events) != "b1,a1" {
		t.Errorf("Timeline inside a segment: %v", segs)
	}

	c.Advance(secs(50))
	if _, _, ok := s.Window("a1"); ok {
		t.Error("Window of a superseded event")
	}
	if got := mrids(s.Events()); got != "a2,b1,a1,b2" {
		t.Errorf("Events: %s", got)
	}
}

func TestRemove(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	defer s.Close()
	s.Upsert(ev("A", 1, 0, 10, 10))
	if !s.Remove("A") || s.Remove("A") {
		t.Fatal("Remove: want true, then false")
	}
	if c.pending() != 0 {
		t.Fatalf("%d timers left", c.pending())
	}
}

func TestRandomization(t *testing.T) {
	const n = 100
	events := make([]Event, n)
	for i := range events {
		rs, rd := int32(i%7*100-300), int32(i%5*150-300)
		events[i] = Event{
			MRID: fmt.Sprintf("E%03d", i), Primacy: 1, Created: at(i),
			Start: at(10000 * (i + 1)), Duration: secs(400 + i), RandStart: rs, RandDuration: rd,
		}
	}
	windows := func(seed uint64, reverse bool) map[string][2]time.Time {
		c := newFakeClock(t0)
		s := New(Options{Clock: c, Seed: seed, Randomize: true})
		defer s.Close()
		order := slices.Clone(events)
		if reverse {
			slices.Reverse(order)
		}
		for _, e := range order {
			s.Upsert(e)
		}
		w := make(map[string][2]time.Time)
		for _, e := range events {
			st, en, ok := s.Window(e.MRID)
			if !ok {
				t.Fatalf("%s has no window", e.MRID)
			}
			w[e.MRID] = [2]time.Time{st, en}
		}
		return w
	}
	a, b, other := windows(42, false), windows(42, true), windows(43, false)
	differs := 0
	for _, e := range events {
		w := a[e.MRID]
		if w != b[e.MRID] {
			t.Errorf("%s: %v in one order, %v in the other", e.MRID, w, b[e.MRID])
		}
		if w != other[e.MRID] {
			differs++
		}
		ds := w[0].Sub(e.Start)
		dd := w[1].Sub(w[0]) - e.Duration
		if ds%time.Second != 0 || dd%time.Second != 0 {
			t.Errorf("%s: offsets %v %v are not whole seconds", e.MRID, ds, dd)
		}
		if !within(ds, e.RandStart) {
			t.Errorf("%s: start offset %v outside %d", e.MRID, ds, e.RandStart)
		}
		if !within(dd, e.RandDuration) {
			t.Errorf("%s: duration offset %v outside %d", e.MRID, dd, e.RandDuration)
		}
	}
	if differs < n/2 {
		t.Errorf("only %d of %d windows depend on the seed", differs, n)
	}
}

func within(d time.Duration, bound int32) bool {
	b := secs(int(bound))
	if bound < 0 {
		return d >= b && d <= 0
	}
	return d >= 0 && d <= b
}

func TestRandomizedDurationNotNegative(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c, Randomize: true})
	defer s.Close()
	for seed := range 20 {
		s.seed = uint64(seed)
		e := Event{MRID: "A", Start: at(10), Duration: secs(1), RandDuration: -3600}
		s.Remove("A")
		s.Upsert(e)
		if st, en, ok := s.Window("A"); !ok || en.Before(st) {
			t.Fatalf("seed %d: window %v %v %v", seed, st, en, ok)
		}
	}
}

func TestRandomizedCancel(t *testing.T) {
	e := Event{MRID: "A", Primacy: 1, Start: at(0), Duration: secs(1000), RandStart: -30, RandDuration: 120}
	run := func(seed uint64) (time.Time, string) {
		c := newFakeClock(t0)
		s := New(Options{Clock: c, Seed: seed, Randomize: true})
		defer s.Close()
		r := record(s)
		c.Advance(secs(100))
		s.Upsert(e)
		s.Cancel("A", true)
		e2 := e
		e2.Status = CancelledRandom
		s.Upsert(e2) // the server repeats the status on every poll
		got, _ := s.Get("A")
		c.Advance(secs(1000))
		ts := r.wait(t, 3)
		if ts[2].At.After(at(100)) && got.Status != Active {
			t.Fatalf("seed %d: %s right after a randomized cancel", seed, got.Status)
		}
		return ts[2].At, describe(ts)
	}
	var spread []time.Time
	for seed := range uint64(20) {
		when, got := run(seed)
		if d := when.Sub(at(100)); d < 0 || d > secs(120) {
			t.Errorf("seed %d: cancelled %v after the cancel", seed, d)
		}
		if !strings.HasSuffix(got, "A:Active>CancelledRandom@"+fmt.Sprint(int(when.Sub(t0)/time.Second))) {
			t.Errorf("seed %d: %s", seed, got)
		}
		if again, _ := run(seed); !again.Equal(when) {
			t.Errorf("seed %d: cancelled at %v, then at %v", seed, when, again)
		}
		spread = append(spread, when)
	}
	slices.SortFunc(spread, time.Time.Compare)
	if len(slices.CompactFunc(spread, time.Time.Equal)) < 5 {
		t.Errorf("randomized cancellation hardly varies: %v", spread)
	}

	// Close to its end, an event is cancelled no later than it ends.
	c := newFakeClock(t0)
	s := New(Options{Clock: c, Seed: 7, Randomize: true})
	defer s.Close()
	s.Upsert(Event{MRID: "B", Start: at(0), Duration: secs(10), RandDuration: 3600})
	c.Advance(secs(1))
	s.Cancel("B", true)
	_, en, _ := s.Window("B")
	c.Advance(secs(3600 * 2))
	if got, _ := s.Get("B"); got.Status != CancelledRandom || en.After(at(3601)) {
		t.Fatalf("B: %s, window end %v", got.Status, en)
	}
	// A cancel without randomization overrides a pending one.
	s.Upsert(Event{MRID: "C", Start: at(7200), Duration: secs(1000), RandDuration: 3600})
	c.Advance(secs(10))
	s.Cancel("C", true)
	s.Cancel("C", false)
	if got, _ := s.Get("C"); got.Status != Cancelled {
		t.Fatalf("C: %s", got.Status)
	}
}

func TestPrecedes(t *testing.T) {
	a, b := ev("A", 1, 0, 0, 1), ev("B", 2, 0, 0, 1)
	if !Precedes(a, b) || Precedes(b, a) || Precedes(a, a) {
		t.Fatal("primacy")
	}
	if e := (Event{Start: at(5), Duration: secs(3)}); !e.End().Equal(at(8)) {
		t.Fatal("End")
	}
}

func TestReceived(t *testing.T) {
	if !(Transition{From: Scheduled, To: Scheduled}).Received() || (Transition{From: Scheduled, To: Active}).Received() {
		t.Fatal("Received")
	}
}

func TestSubscribeAndClose(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	ch1, stop1 := s.Subscribe()
	ch2, _ := s.Subscribe()
	s.Upsert(ev("A", 1, 0, 0, 10))
	for _, ch := range []<-chan Transition{ch1, ch2} {
		for range 2 {
			select {
			case <-ch:
			case <-time.After(5 * time.Second):
				t.Fatal("no transition")
			}
		}
	}
	stop1()
	stop1()
	if _, ok := <-ch1; ok {
		t.Fatal("channel open after unsubscribing")
	}
	s.Upsert(ev("B", 1, 0, 100, 10)) // queued for ch2, never read
	s.Close()
	s.Close()
	for range ch2 {
	}
	if c.pending() != 0 {
		t.Fatalf("%d timers left after Close", c.pending())
	}
	// After Close, nothing changes.
	s.Upsert(ev("C", 1, 0, 0, 10))
	s.Cancel("A", false)
	if s.Remove("A") {
		t.Fatal("Remove after Close")
	}
	if got := statuses(s); got != "A=Active B=Scheduled" {
		t.Fatalf("after Close: %s", got)
	}
	ch3, stop3 := s.Subscribe()
	if _, ok := <-ch3; ok {
		t.Fatal("Subscribe after Close: open channel")
	}
	stop3()
	s.fire(s.gen) // a timer that fires after Close does nothing
}

func TestStaleTimer(t *testing.T) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	defer s.Close()
	s.Upsert(ev("A", 1, 0, 10, 10))
	s.fire(s.gen - 1) // superseded generation: no effect
	if e, _ := s.Get("A"); e.Status != Scheduled {
		t.Fatal(e.Status)
	}
}

func TestSystemClockScheduler(t *testing.T) {
	s := New(Options{})
	defer s.Close()
	ch, stop := s.Subscribe()
	defer stop()
	now := time.Now()
	s.Upsert(Event{MRID: "A", Start: now.Add(20 * time.Millisecond), Duration: 20 * time.Millisecond})
	var got []Status
	timeout := time.After(10 * time.Second)
	for len(got) < 3 {
		select {
		case tr := <-ch:
			got = append(got, tr.To)
		case <-timeout:
			t.Fatalf("got %v", got)
		}
	}
	if !slices.Equal(got, []Status{Scheduled, Active, Completed}) {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrency(t *testing.T) {
	before := runtime.NumGoroutine()
	for _, fake := range []bool{true, false} {
		clock := SystemClock
		var fc *fakeClock
		if fake {
			fc = newFakeClock(time.Now())
			clock = fc
		}
		s := New(Options{Clock: clock, Randomize: true, Seed: 1})
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for w := range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 200 {
					now := clock.Now()
					s.Upsert(Event{
						MRID: fmt.Sprintf("W%d-%d", w, i%20), Primacy: uint8(i % 3), Created: now,
						Start: now.Add(time.Duration(i%7) * time.Millisecond), Duration: time.Duration(i%5) * time.Millisecond,
						RandStart: int32(i % 2), RandDuration: -1,
					})
					if i%17 == 0 {
						s.Cancel(fmt.Sprintf("W%d-%d", (w+1)%4, i%20), i%2 == 0)
					}
					if i%23 == 0 {
						s.Remove(fmt.Sprintf("W%d-%d", w, (i+3)%20))
					}
				}
			}()
		}
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 100 {
					ch, unsub := s.Subscribe()
					for range i % 5 {
						select {
						case <-ch:
						case <-time.After(time.Millisecond):
						}
					}
					unsub()
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				now := clock.Now()
				s.Active(now)
				s.Timeline(now, now.Add(time.Second))
				s.Events()
				if fc != nil {
					fc.Advance(time.Millisecond)
				}
			}
		}()
		// A subscriber that never reads, and one that reads until Close.
		s.Subscribe()
		reader, _ := s.Subscribe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range reader {
			}
		}()
		time.Sleep(50 * time.Millisecond)
		close(stop)
		s.Close()
		wg.Wait()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines before, %d after:\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(time.Millisecond)
	}
}
