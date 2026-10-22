// SPDX-License-Identifier: MIT

package client

import "time"

// Metrics receives the requests of a client, to count and time them: set
// it as Config.Metrics. Each method is called once per call of the client
// (a Get, a Post, ...), whatever its retries: OnRequest when it starts, and
// OnResponse or OnError when it ends, with its duration including retries.
// path is the path of the request, an href of the server; aggregate it
// (by WADL resource, say) before using it as a label.
//
// The methods are called synchronously from the goroutines of the calls:
// they must be safe for concurrent use, quick, and must not block. Embed
// NopMetrics to implement only some.
type Metrics interface {
	OnRequest(method, path string)
	OnResponse(method, path string, status int, d time.Duration)
	OnError(method, path string, d time.Duration, err error)
}

// AttemptMetrics is a Metrics that is also told of each retry of a call:
// the attempt that failed (from 0), its error, and the wait before the
// next.
type AttemptMetrics interface {
	Metrics
	OnRetry(method, path string, attempt int, err error, wait time.Duration)
}

// NopMetrics implements Metrics and AttemptMetrics with methods that do
// nothing; embed it.
type NopMetrics struct{}

func (NopMetrics) OnRequest(string, string)                          {}
func (NopMetrics) OnResponse(string, string, int, time.Duration)     {}
func (NopMetrics) OnError(string, string, time.Duration, error)      {}
func (NopMetrics) OnRetry(string, string, int, error, time.Duration) {}
