// SPDX-License-Identifier: MIT

package schedule

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock is a Clock that only moves when a test advances it. It calls
// the functions of due timers from Advance, in order of their time, with
// the clock set to that time.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c  *fakeClock
	at time.Time
	f  func()
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	i := slices.Index(t.c.timers, t)
	if i < 0 {
		return false
	}
	t.c.timers = slices.Delete(t.c.timers, i, i+1)
	return true
}

// Advance moves the clock d ahead, firing the timers due on the way.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.timers = slices.DeleteFunc(c.timers, func(t *fakeTimer) bool { return t == next })
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

// pending returns the number of armed timers.
func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func TestFakeClock(t *testing.T) {
	c := newFakeClock(t0)
	var got []int
	c.AfterFunc(2*time.Second, func() { got = append(got, 2) })
	s := c.AfterFunc(3*time.Second, func() { got = append(got, 3) })
	c.AfterFunc(time.Second, func() { got = append(got, 1) })
	if !s.Stop() || s.Stop() {
		t.Fatal("Stop: want true, then false")
	}
	c.Advance(5 * time.Second)
	if !slices.Equal(got, []int{1, 2}) || !c.Now().Equal(t0.Add(5*time.Second)) {
		t.Fatalf("got %v at %v", got, c.Now())
	}
}

func TestSystemClock(t *testing.T) {
	if d := time.Since(SystemClock.Now()); d < 0 || d > time.Minute {
		t.Fatalf("SystemClock.Now is %v off", d)
	}
	fired := make(chan struct{})
	SystemClock.AfterFunc(time.Millisecond, func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("AfterFunc did not fire")
	}
	if SystemClock.AfterFunc(time.Hour, func() {}).Stop() != true {
		t.Fatal("Stop of a pending timer: want true")
	}
}
