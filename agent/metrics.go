// SPDX-License-Identifier: MIT

package agent

import (
	"time"

	"github.com/otfabric/go-ieee20305/schedule"
)

// Metrics receives what an agent does, to count and time it: set it as
// Config.Metrics. OnPoll is called after each poll of the device's
// resources, OnEvent with each transition of an event of a function set,
// and OnResponse after each attempt to post a Response (with the Response's
// status, and the error of the attempt).
//
// The methods are called synchronously from the agent's goroutines: they
// must be safe for concurrent use, quick, and must not block. Embed
// NopMetrics to implement only some.
type Metrics interface {
	OnPoll(d time.Duration, err error)
	OnEvent(fs FunctionSet, t schedule.Transition)
	OnResponse(fs FunctionSet, status uint8, err error)
}

// NopMetrics implements Metrics with methods that do nothing; embed it.
type NopMetrics struct{}

func (NopMetrics) OnPoll(time.Duration, error)              {}
func (NopMetrics) OnEvent(FunctionSet, schedule.Transition) {}
func (NopMetrics) OnResponse(FunctionSet, uint8, error)     {}
