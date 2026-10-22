// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/internal/chaos"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/server"
)

// counts counts the calls of the hooks of the client, server and agent.
type counts struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counts) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[key]++
}

func (c *counts) get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[key]
}

type clientMetrics struct{ *counts }

func (m clientMetrics) OnRequest(string, string)                          { m.add("client.request") }
func (m clientMetrics) OnResponse(string, string, int, time.Duration)     { m.add("client.response") }
func (m clientMetrics) OnError(string, string, time.Duration, error)      { m.add("client.error") }
func (m clientMetrics) OnRetry(string, string, int, error, time.Duration) { m.add("client.retry") }

type serverMetrics struct{ *counts }

func (m serverMetrics) OnRequest(string) { m.add("server.request") }
func (m serverMetrics) OnResponse(_, resource string, status int, _ time.Duration) {
	m.add("server.response")
	m.add(fmt.Sprintf("server.%s.%d", resource, status))
}
func (m serverMetrics) OnNotification(resource string, _ time.Duration, err error) {
	m.add(fmt.Sprintf("server.notification.%s.%v", resource, err == nil))
}
func (m serverMetrics) OnSubscriptionCancelled(string, uint8) { m.add("server.cancelled") }
func (m serverMetrics) OnEventTransition(element string, _, to schedule.Status) {
	m.add(fmt.Sprintf("server.event.%s.%v", element, to))
}

type agentMetrics struct{ *counts }

func (m agentMetrics) OnPoll(time.Duration, error) { m.add("agent.poll") }
func (m agentMetrics) OnEvent(fs agent.FunctionSet, t schedule.Transition) {
	m.add(fmt.Sprintf("agent.event.%v.%v", fs, t.To))
}
func (m agentMetrics) OnResponse(_ agent.FunctionSet, status uint8, err error) {
	m.add(fmt.Sprintf("agent.response.%d.%v", status, err == nil))
}

// lines is a FieldLogger that keeps its lines with their fields.
type lines struct {
	mu     *sync.Mutex
	out    *[]string
	fields string
}

func newLines() lines { return lines{mu: &sync.Mutex{}, out: &[]string{}} }

func (l lines) add(level, f string, a []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.out = append(*l.out, level+" "+fmt.Sprintf(f, a...)+l.fields)
}
func (l lines) Debugf(f string, a ...any) { l.add("DEBUG", f, a) }
func (l lines) Infof(f string, a ...any)  { l.add("INFO", f, a) }
func (l lines) Warnf(f string, a ...any)  { l.add("WARN", f, a) }
func (l lines) Errorf(f string, a ...any) { l.add("ERROR", f, a) }
func (l lines) With(kv ...any) ieee20305.FieldLogger {
	return lines{mu: l.mu, out: l.out, fields: l.fields + fmt.Sprintf(" %v", kv)}
}
func (l lines) has(substrings ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range *l.out {
		ok := true
		for _, s := range substrings {
			ok = ok && strings.Contains(line, s)
		}
		if ok {
			return true
		}
	}
	return false
}

// The hooks of the client, server and agent see every call, under faults,
// and the loggers name their components.
func TestObservability(t *testing.T) {
	c := &counts{}
	log := newLines()
	s := newChaosSite(t, server.Config{Metrics: serverMetrics{c}, Logger: log}, func(next http.Handler) http.Handler {
		return &chaos.Handler{Next: next, Faults: chaos.Faults{Seed: 5, Unavailable: 0.1}}
	})
	s.control(t, s.prog, 1, time.Now(), 3600)
	a, lfdi := chaosAgent(t, s.env, "device-1", nil, agent.Config{Metrics: agentMetrics{c}, Logger: log})
	// The agent's client, with metrics and retries of its own.
	dev := s.env.pki.device(t, "device-1")
	cl, err := client.New(client.Config{Server: s.env.url, HTTPClient: client.NewHTTPClient(dev, s.env.pki.roots),
		Metrics: clientMetrics{c}, Logger: log, RetryPolicy: ieee20305.ExponentialBackoff{Initial: time.Millisecond, Attempts: 5}})
	if err != nil {
		t.Fatal(err)
	}
	run(t, a)
	s.admit(t, lfdi)
	s.converges(t, a, 1)
	for i := 0; i < 50; i++ {
		_, _ = cl.DeviceCapability(context.Background())
	}

	if c.get("client.request") != 50 || c.get("client.response")+c.get("client.error") != 50 {
		t.Errorf("client: %d requests, %d responses, %d errors", c.get("client.request"), c.get("client.response"), c.get("client.error"))
	}
	if c.get("client.retry") == 0 {
		t.Error("no retry of the client under 503s")
	}
	eventually(t, "the server's responses", func() bool { return c.get("server.request") == c.get("server.response") })
	// The faults are injected in front of the server: it counts what it
	// answers itself.
	if c.get("server.DeviceCapability.200") == 0 || c.get("server.ResponseList.201") != 2 {
		t.Errorf("server counts %v", c.n)
	}
	if c.get("server.event.DERControl.Active") != 1 {
		t.Errorf("server event transitions %v", c.n)
	}
	eventually(t, "a poll", func() bool { return c.get("agent.poll") > 0 })
	if c.get("agent.event.DER.Active") != 1 ||
		c.get(fmt.Sprintf("agent.response.%d.true", schedule.ResponseReceived)) != 1 {
		t.Errorf("agent counts %v", c.n)
	}
	eventually(t, "the agent's log", func() bool { return log.has("INFO started as EndDevice", "[component agent]") })
	for _, want := range [][]string{
		{"DEBUG GET /dcap", "[component server]"},
		{"INFO device", "registered", "[component server]"},
		{"INFO started as EndDevice", "[component agent]"},
		{"DEBUG GET /dcap: 200", "[component client]"},
	} {
		if !log.has(want...) {
			t.Errorf("no line with %q", want)
		}
	}
}

// The notifications of a server are counted, with the subscriptions it
// cancels.
func TestNotificationMetrics(t *testing.T) {
	c := &counts{}
	env := newEnv(t, server.Config{Metrics: serverMetrics{c}, NotificationRetry: ieee20305.NoRetry()})
	ctx := context.Background()
	cl, ed, lfdi := env.provisioned(t, "device-1", ieee20305.Edition2023)
	box := &inbox{}
	url := env.listen(t, notificationHandler(t, box.handle, client.NotificationConfig{}))
	if _, err := cl.Subscribe(ctx, ed+"/sub", ed+"/fsa", url, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.Admin().AssignFSA(ctx, lfdi, nil); err == nil {
		t.Fatal("AssignFSA of nothing")
	}
	if _, err := env.srv.Admin().AssignFSA(ctx, lfdi, &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{1}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the notification", func() bool { return c.get("server.notification.FunctionSetAssignmentsList.true") == 1 })
	// A subscription to a receiver that is gone is cancelled.
	if _, err := cl.Subscribe(ctx, ed+"/sub", ed+"/fsa", "https://127.0.0.1:1/ntfy", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.Admin().AssignFSA(ctx, lfdi, &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{2}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the cancellation", func() bool { return c.get("server.cancelled") == 1 })
	if c.get("server.notification.FunctionSetAssignmentsList.false") != 1 {
		t.Errorf("counts %v", c.n)
	}
}
