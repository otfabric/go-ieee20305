// SPDX-License-Identifier: MIT

package ieee20305

import (
	"math/rand/v2"
	"time"
)

// RetryPolicy decides whether a failed call is made again, and when: the
// clients and servers of this module take one for their requests. attempt
// is the number of the attempt that failed, from 0. A policy is called from
// many goroutines.
type RetryPolicy interface {
	ShouldRetry(attempt int, err error) (retry bool, wait time.Duration)
}

// NoRetry returns the RetryPolicy that makes no call twice.
func NoRetry() RetryPolicy { return noRetry{} }

type noRetry struct{}

func (noRetry) ShouldRetry(int, error) (bool, time.Duration) { return false, 0 }

// ExponentialBackoff is the RetryPolicy that makes a call again when
// Retryable(err) says it may succeed, after a delay that doubles from
// Initial up to Max, with jitter: the wait is drawn at random between half
// of the delay and the delay, so that devices that failed together do not
// come back together. A server's RetryAfter is waited when it is longer.
//
// The zero value retries up to 3 times from 100 ms (Attempts 3, Initial
// 100 ms, Max 30 s).
type ExponentialBackoff struct {
	// Initial is the delay after the first failure; 100 ms when zero.
	Initial time.Duration
	// Max caps the delay; 30 s when zero.
	Max time.Duration
	// Attempts is the number of retries; 3 when zero, unbounded when
	// negative.
	Attempts int
	// NoJitter waits the delay itself, for tests.
	NoJitter bool
}

// ShouldRetry implements RetryPolicy.
func (b ExponentialBackoff) ShouldRetry(attempt int, err error) (bool, time.Duration) {
	attempts := b.Attempts
	if attempts == 0 {
		attempts = 3
	}
	if !Retryable(err) || (attempts > 0 && attempt >= attempts) {
		return false, 0
	}
	return true, max(b.Delay(attempt), RetryAfter(err))
}

// Delay returns the wait after the failure of attempt, regardless of the
// error and of Attempts.
func (b ExponentialBackoff) Delay(attempt int) time.Duration {
	initial, ceiling := b.Initial, b.Max
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	if ceiling <= 0 {
		ceiling = 30 * time.Second
	}
	d := initial
	for i := 0; i < attempt && d < ceiling; i++ {
		d *= 2
	}
	d = min(d, ceiling)
	if b.NoJitter || d < 2 {
		return d
	}
	return d/2 + rand.N(d/2)
}
