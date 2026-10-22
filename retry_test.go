// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func TestRetryPolicies(t *testing.T) {
	unavailable := fmt.Errorf("%w: x", ieee20305.ErrUnavailable)
	if retry, _ := ieee20305.NoRetry().ShouldRetry(0, unavailable); retry {
		t.Error("NoRetry retries")
	}
	b := ieee20305.ExponentialBackoff{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond, Attempts: 3, NoJitter: true}
	for attempt, want := range []time.Duration{10, 20, 40, 50, 50} {
		if got := b.Delay(attempt); got != want*time.Millisecond {
			t.Errorf("Delay(%d) = %v", attempt, got)
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		retry, wait := b.ShouldRetry(attempt, unavailable)
		if retry != (attempt < 3) || (retry && wait != b.Delay(attempt)) {
			t.Errorf("ShouldRetry(%d) = %v, %v", attempt, retry, wait)
		}
	}
	if retry, _ := b.ShouldRetry(0, errors.New("not retryable")); retry {
		t.Error("an error that is not retryable is retried")
	}
	// A server's Retry-After wins when longer.
	busy := &ieee20305.StatusError{StatusCode: 503, RetryAfter: time.Second}
	if _, wait := b.ShouldRetry(0, busy); wait != time.Second {
		t.Errorf("RetryAfter: %v", wait)
	}
	// Jitter stays within half the delay and the delay.
	j := ieee20305.ExponentialBackoff{Initial: 100 * time.Millisecond}
	for i := 0; i < 100; i++ {
		if d := j.Delay(2); d < 200*time.Millisecond || d > 400*time.Millisecond {
			t.Fatalf("jittered delay %v", d)
		}
	}
	// The zero value: 3 attempts from 100 ms; Attempts < 0 never stops.
	var zero ieee20305.ExponentialBackoff
	if retry, _ := zero.ShouldRetry(3, unavailable); retry {
		t.Error("the zero value retries a fourth time")
	}
	if retry, _ := (ieee20305.ExponentialBackoff{Attempts: -1}).ShouldRetry(1000, unavailable); !retry {
		t.Error("unbounded attempts stop")
	}
	if d := zero.Delay(100); d > 30*time.Second {
		t.Errorf("the zero value's ceiling: %v", d)
	}
}
