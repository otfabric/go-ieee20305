// SPDX-License-Identifier: MIT

package schedule

import (
	"fmt"
	"testing"
)

// The results of the benchmarks are kept here, so that the work that makes
// them is not removed as unused.
var (
	sinkEvents []Event
	sinkInt    int
)

// apart returns n events of one program that do not overlap.
func apart(n int) []Event {
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = ev(fmt.Sprintf("E%d", i), 1, i, 3600+60*i, 30)
	}
	return evs
}

// BenchmarkUpsertAll takes the events of a poll, DefaultMaxEvents of them,
// into a new Scheduler and resolves them.
func BenchmarkUpsertAll(b *testing.B) {
	evs := apart(DefaultMaxEvents)
	b.ReportAllocs()
	for b.Loop() {
		s := New(Options{Clock: newFakeClock(t0)})
		if refused := s.UpsertAll(evs); refused != 0 {
			b.Fatalf("%d events refused", refused)
		}
		sinkInt = len(s.events)
		s.Close()
	}
}

// BenchmarkUpsert updates one event of a Scheduler that holds
// DefaultMaxEvents, which resolves them all again.
func BenchmarkUpsert(b *testing.B) {
	evs := apart(DefaultMaxEvents)
	s := New(Options{Clock: newFakeClock(t0)})
	defer s.Close()
	s.UpsertAll(evs)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		e := evs[i%len(evs)]
		e.Duration = secs(10 + i%20)
		s.Upsert(e)
		i++
	}
	if sinkInt = len(s.Events()); sinkInt != len(evs) {
		b.Fatalf("%d events", sinkInt)
	}
}

// BenchmarkActive asks a Scheduler that holds DefaultMaxEvents what is
// active: a query resolves nothing while no change is due.
func BenchmarkActive(b *testing.B) {
	c := newFakeClock(t0)
	s := New(Options{Clock: c})
	defer s.Close()
	s.UpsertAll(apart(DefaultMaxEvents))
	c.Advance(secs(3610))
	b.ReportAllocs()
	for b.Loop() {
		sinkEvents = s.Active(c.Now())
	}
	if len(sinkEvents) != 1 {
		b.Fatalf("%d events active, want 1", len(sinkEvents))
	}
}
