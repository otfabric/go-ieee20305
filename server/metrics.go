// SPDX-License-Identifier: MIT

package server

import (
	"time"

	"github.com/otfabric/go-ieee20305/schedule"
)

// Metrics receives the requests of a server, to count and time them: set it
// as Config.Metrics. OnRequest is called when a request comes in, and
// OnResponse when it is answered, with the WADL resource ID of its target
// (such as "EndDeviceList"; "" for a path the server does not have), which
// makes a label of few values.
//
// The methods are called synchronously from the goroutines that serve the
// requests: they must be safe for concurrent use, quick, and must not
// block. Embed NopMetrics to implement only some. A Metrics may also be a
// NotificationMetrics and an EventMetrics.
type Metrics interface {
	OnRequest(method string)
	OnResponse(method, resource string, status int, d time.Duration)
}

// NotificationMetrics is a Metrics that is also told of notifications:
// each delivery to a subscriber (the WADL resource ID of the subscribed
// resource, how long it took, and its error, after retries), and each
// subscription the server cancels, with the status of the cancellation it
// sent (or would have).
type NotificationMetrics interface {
	Metrics
	OnNotification(resource string, d time.Duration, err error)
	OnSubscriptionCancelled(resource string, status uint8)
}

// EventMetrics is a Metrics that is also told of the transitions of the
// events of the server (DERControls, EndDeviceControls, ...) that change
// their EventStatus: the document element of the event, and its states.
type EventMetrics interface {
	Metrics
	OnEventTransition(element string, from, to schedule.Status)
}

// NopMetrics implements Metrics, NotificationMetrics and EventMetrics with
// methods that do nothing; embed it.
type NopMetrics struct{}

func (NopMetrics) OnRequest(string)                                           {}
func (NopMetrics) OnResponse(string, string, int, time.Duration)              {}
func (NopMetrics) OnNotification(string, time.Duration, error)                {}
func (NopMetrics) OnSubscriptionCancelled(string, uint8)                      {}
func (NopMetrics) OnEventTransition(string, schedule.Status, schedule.Status) {}
