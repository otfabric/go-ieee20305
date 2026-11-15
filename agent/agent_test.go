// SPDX-License-Identifier: MIT

package agent

import (
	"math"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func TestPollRate(t *testing.T) {
	a := &Agent{}
	if a.pollRate() != 900*time.Second {
		t.Errorf("default %v", a.pollRate())
	}
	rate := ieee20305.UInt32(60)
	a.state.DeviceCapability = &ieee20305.DeviceCapability{}
	a.state.DeviceCapability.PollRate = &rate
	if a.pollRate() != time.Minute {
		t.Errorf("from DeviceCapability %v", a.pollRate())
	}
	a.cfg.PollRate = time.Second
	if a.pollRate() != time.Second {
		t.Errorf("configured %v", a.pollRate())
	}
}

// A pollRate of the server is bounded, and so is four times it while
// subscribed: no value of the uint32 wraps to a short or negative wait.
func TestPollRateBounded(t *testing.T) {
	a := &Agent{subs: map[string]string{}}
	a.state.DeviceCapability = &ieee20305.DeviceCapability{}
	for _, v := range []uint32{86400, 86401, 2305843009, 2305843010, math.MaxUint32} {
		rate := ieee20305.UInt32(v)
		a.state.DeviceCapability.PollRate = &rate
		if a.pollRate() != maxPollRate || a.subscribedPollRate() != 4*maxPollRate {
			t.Errorf("pollRate %d: %v, subscribed %v", v, a.pollRate(), a.subscribedPollRate())
		}
	}
	a.cfg.PollRate = math.MaxInt64 / 2
	if a.subscribedPollRate() != math.MaxInt64 {
		t.Errorf("configured: subscribed %v", a.subscribedPollRate())
	}
}
