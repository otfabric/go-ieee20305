// SPDX-License-Identifier: MIT

package schedule

import "time"

// Clock is the time source of a Scheduler. Tests replace SystemClock with a
// clock they advance themselves.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// AfterFunc calls f in its own goroutine once d has elapsed, unless
	// the returned Stopper is stopped first. The Scheduler only asks for
	// d > 0 and calls AfterFunc and Stop with its own lock held, so a
	// clock must not call f from within them; a test clock may call f
	// from the goroutine that advances it.
	AfterFunc(d time.Duration, f func()) Stopper
}

// Stopper cancels a pending call of Clock.AfterFunc.
type Stopper interface {
	// Stop prevents the call if it has not happened yet, and reports
	// whether it did so.
	Stop() bool
}

// SystemClock is the Clock of the operating system: time.Now and
// time.AfterFunc.
var SystemClock Clock = systemClock{}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) AfterFunc(d time.Duration, f func()) Stopper { return time.AfterFunc(d, f) }
