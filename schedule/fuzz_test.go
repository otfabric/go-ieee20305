// SPDX-License-Identifier: MIT

package schedule

import (
	"fmt"
	"testing"
)

// maskConflicts is the Conflicts of FuzzResolve: events conflict when the
// uint8 masks in their Values intersect.
func maskConflicts(a, b Event) bool { return a.Value.(uint8)&b.Value.(uint8) != 0 }

// FuzzResolve runs a sequence of operations decoded from the input and
// checks after each one that no two conflicting events are active at the
// same instant, now or in the plan, and that every superseded event lost to
// a conflicting, overlapping event of higher precedence.
func FuzzResolve(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 0, 2, 1, 0, 9, 9, 9, 2, 3, 0, 0, 0, 0, 30, 0, 0})
	f.Add([]byte{0, 0, 1, 0, 0, 20, 3, 0, 0, 1, 0, 0, 5, 8, 3, 0, 1, 0, 2, 0, 0, 10, 0, 0, 1, 1, 0, 0, 0, 0})
	f.Add([]byte{0, 0, 3, 1, 0, 50, 1, 0, 0, 1, 2, 2, 10, 50, 1, 0, 0, 2, 1, 3, 20, 40, 2, 0, 2, 0, 0, 0, 0, 0, 3, 1, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		c := newFakeClock(t0)
		s := New(Options{Clock: c, Seed: 1, Randomize: true, Conflicts: maskConflicts})
		defer s.Close()
		prev := map[string]Status{}
		for len(data) >= 6 {
			b := data[:6]
			data = data[6:]
			mrid := fmt.Sprintf("E%d", b[1]%8)
			switch b[0] % 5 {
			case 0, 1:
				s.Upsert(Event{
					MRID: mrid, Primacy: b[2] % 4, Created: at(int(b[3] % 8)),
					Start:     c.Now().Add(secs(int(b[4]%64)*10 - 100)),
					Duration:  secs(int(b[5]%16) * 10),
					RandStart: int32(int8(b[2])) % 60, RandDuration: int32(int8(b[3])) % 60,
					Value: b[5]>>4%7 + 1,
				})
			case 2:
				s.Cancel(mrid, b[2]%2 == 0)
			case 3:
				s.Remove(mrid)
			default:
				c.Advance(secs(int(b[2]) + 1))
			}
			check(t, c, s, prev)
		}
		c.Advance(secs(10000))
		check(t, c, s, prev)
	})
}

func check(t *testing.T, c *fakeClock, s *Scheduler, prev map[string]Status) {
	t.Helper()
	now := c.Now()
	noConflict := func(what string, evs []Event) {
		for i := range evs {
			for j := i + 1; j < len(evs); j++ {
				if maskConflicts(evs[i], evs[j]) {
					t.Fatalf("%s: %s and %s are both active", what, evs[i].MRID, evs[j].MRID)
				}
			}
		}
	}
	evs := s.Events()
	var active []Event
	for _, e := range evs {
		if e.Status == Active {
			active = append(active, e)
			if st, en, ok := s.Window(e.MRID); !ok || now.Before(st) || !now.Before(en) {
				t.Fatalf("%s is active at %v outside its window [%v, %v)", e.MRID, now, st, en)
			}
		}
	}
	noConflict("status", active)
	noConflict("Active", s.Active(now))
	for _, seg := range s.Timeline(now, now.Add(secs(2000))) {
		noConflict("Timeline", seg.Events)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		was, seen := prev[e.MRID]
		prev[e.MRID] = e.Status
		if e.Status != Superseded || (seen && was == Superseded) {
			continue
		}
		en := s.events[e.MRID]
		found := false
		for _, w := range s.events {
			if w != en && Precedes(w.ev, en.ev) && maskConflicts(w.ev, en.ev) && overlap(w.start, w.end, en.start, en.end) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s is superseded, but no conflicting event of higher precedence overlaps it", e.MRID)
		}
	}
	for m := range prev {
		if _, ok := s.events[m]; !ok {
			delete(prev, m)
		}
	}
}
