// SPDX-License-Identifier: MIT

package agent

import (
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/callback"
	"github.com/otfabric/go-ieee20305/schedule"
)

// Metrics receives what an agent does, to count and time it: set it as
// Config.Metrics. OnPoll is called after each poll of the device's
// resources, OnEvent with each transition of an event of a function set,
// and OnResponse after each attempt to post a Response (with the Response's
// status, and the error of the attempt).
//
// The methods are called synchronously from the agent's goroutines: they
// must be safe for concurrent use, quick, and must not block. OnPoll is
// called on the goroutine of Run; OnEvent on the goroutine that calls the
// handler of the function set, before it; OnResponse on that goroutine, or
// on Run's for a Response sent again. They may call the methods of the
// Agent, Close included. A method that does not return normally is stopped
// there: the agent logs that at Error level with the stack and goes on.
// Embed NopMetrics to implement only some.
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

// guardedMetrics calls the Metrics of the application: a method that does
// not return normally is stopped where the agent calls it, and logged. The
// agent holds none of its locks when it calls them.
type guardedMetrics struct {
	m   Metrics
	log ieee20305.Logger
}

func (g guardedMetrics) OnPoll(d time.Duration, err error) {
	defer callback.Rescue(g.log, "Metrics.OnPoll")
	g.m.OnPoll(d, err)
}

func (g guardedMetrics) OnEvent(fs FunctionSet, t schedule.Transition) {
	defer callback.Rescue(g.log, "Metrics.OnEvent")
	g.m.OnEvent(fs, t)
}

func (g guardedMetrics) OnResponse(fs FunctionSet, status uint8, err error) {
	defer callback.Rescue(g.log, "Metrics.OnResponse")
	g.m.OnResponse(fs, status, err)
}
