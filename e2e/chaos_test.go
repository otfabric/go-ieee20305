// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/internal/chaos"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// chaosSite is a server with a DER program assigned to every device it
// registers, a control active now, and a ResponseSet, for agents that
// register in band.
type chaosSite struct {
	*derSite
	prog string
}

func newChaosSite(t *testing.T, cfg server.Config, wrap func(http.Handler) http.Handler) *chaosSite {
	t.Helper()
	d := &derSite{statuses: map[string][]uint8{}}
	cfg.InBandRegistration = true
	cfg.OnResponse = func(_ context.Context, _ string, r any) {
		resp := r.(*ieee20305.DERControlResponse)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.statuses[string(resp.Subject)] = append(d.statuses[string(resp.Subject)], uint8(*resp.Status))
	}
	d.env = newEnvWrapped(t, cfg, wrap)
	d.admin = d.env.srv.Admin()
	ctx := context.Background()
	if err := d.admin.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	if err := d.admin.Root(ctx, "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{}); err != nil {
		t.Fatal(err)
	}
	set, err := d.admin.Create(ctx, "/rsps", examples.ResponseSet(0), "")
	if err != nil {
		t.Fatal(err)
	}
	d.responses = set + "/rsp"
	s := &chaosSite{derSite: d}
	prog := examples.DERProgram(0)
	prog.Primacy = 1
	if s.prog, err = d.admin.Create(ctx, "/derp", prog, ""); err != nil {
		t.Fatal(err)
	}
	return s
}

// admit registers the device lfdi once it posted its EndDevice, and assigns
// it the program.
func (s *chaosSite) admit(t *testing.T, lfdi security.LFDI) {
	t.Helper()
	if err := s.admitWithin(lfdi, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// admitWithin is admit for any goroutine: it waits for the EndDevice for at
// most d, and returns what fails.
func (s *chaosSite) admitWithin(lfdi security.LFDI, d time.Duration) error {
	ctx := context.Background()
	deadline := time.Now().Add(d)
	for s.admin.EndDevice(lfdi) == "" {
		if time.Now().After(deadline) {
			return fmt.Errorf("no EndDevice of %s within %v", lfdi, d)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.admin.Register(ctx, lfdi, 0); err != nil {
		return err
	}
	fsa, err := s.admin.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0))
	if err != nil {
		return err
	}
	if err := s.admin.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String()); err != nil {
		return err
	}
	return s.admin.AddMember(ctx, fsa+"/derp", s.prog)
}

// chaosAgent returns an agent of the device name whose requests go through
// rt (the default transport when nil).
func chaosAgent(t *testing.T, env *env, name string, rt func(http.RoundTripper) http.RoundTripper, cfg agent.Config) (*agent.Agent, security.LFDI) {
	t.Helper()
	dev := env.pki.device(t, name)
	hc := client.NewHTTPClient(dev, env.pki.roots)
	if rt != nil {
		hc.Transport = rt(hc.Transport)
	}
	env.clients = append(env.clients, hc)
	c, err := client.New(client.Config{Server: env.url, HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Client, cfg.Identity = c, client.IdentityOf(dev.Leaf)
	if cfg.PollRate == 0 {
		cfg.PollRate = 20 * time.Millisecond
	}
	cfg.Backoff = ieee20305.ExponentialBackoff{Initial: 5 * time.Millisecond, Max: 50 * time.Millisecond}
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a, cfg.Identity.LFDI
}

// converges checks the invariants after faults: the device has one
// EndDevice, the control is active for the agent, and the server has its
// Responses, each once.
func (s *chaosSite) converges(t *testing.T, a *agent.Agent, mrid byte) {
	t.Helper()
	eventually(t, "the control to apply", func() bool { return len(a.Active(agent.DER)) == 1 })
	eventually(t, "the Responses", func() bool {
		return has(s.responsesOf(mrid), schedule.ResponseReceived, schedule.ResponseStarted)
	})
	ms, err := s.admin.Members(context.Background(), "/edev")
	if err != nil || len(ms) != 1 {
		t.Errorf("%d EndDevices (%v)", len(ms), err)
	}
	rs, err := s.admin.Members(context.Background(), s.responses)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range rs {
		r := m.Value.(*ieee20305.DERControlResponse)
		key := string(r.Subject) + string(rune(*r.Status))
		if seen[key] {
			t.Errorf("the Response %d of %X is stored twice", *r.Status, []byte(r.Subject))
		}
		seen[key] = true
	}
}

// An agent whose requests fail, are lost, are answered 503 or truncated,
// and whose POSTs are replayed, registers once and applies its control.
func TestChaosClient(t *testing.T) {
	s := newChaosSite(t, server.Config{}, nil)
	s.control(t, s.prog, 1, time.Now(), 3600)
	var tr *chaos.Transport
	a, lfdi := chaosAgent(t, s.env, "device-1", func(base http.RoundTripper) http.RoundTripper {
		tr = &chaos.Transport{Base: base, Faults: chaos.Faults{Seed: 1, Latency: 2 * time.Millisecond,
			Reset: 0.1, Lost: 0.1, Unavailable: 0.1, Truncate: 0.1, Replay: 0.3}}
		return tr
	}, agent.Config{})
	run(t, a)
	s.admit(t, lfdi)
	s.converges(t, a, 1)
	if c := tr.Counts(); c["lost"]+c["replay"]+c["truncate"]+c["reset"]+c["unavailable"] == 0 {
		t.Errorf("faults %v", c)
	}
}

// An agent of a server that answers 503 and aborts responses converges.
func TestChaosServer(t *testing.T) {
	var h *chaos.Handler
	s := newChaosSite(t, server.Config{}, func(next http.Handler) http.Handler {
		h = &chaos.Handler{Next: next, Faults: chaos.Faults{Seed: 2, Latency: time.Millisecond, Unavailable: 0.15, Abort: 0.15}}
		return h
	})
	s.control(t, s.prog, 1, time.Now(), 3600)
	a, lfdi := chaosAgent(t, s.env, "device-1", nil, agent.Config{})
	run(t, a)
	s.admit(t, lfdi)
	s.converges(t, a, 1)
	if c := h.Counts(); c["unavailable"]+c["abort"] == 0 {
		t.Errorf("faults %v", c)
	}
}

// An agent behind a proxy that resets connections, during the TLS
// handshake and after, converges.
func TestChaosProxy(t *testing.T) {
	s := newChaosSite(t, server.Config{}, nil)
	s.control(t, s.prog, 1, time.Now(), 3600)
	p := &chaos.Proxy{Target: strings.TrimPrefix(s.env.url, "https://"), Reset: 0.3, ResetWithin: 3000, Seed: 3}
	addr, err := p.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	dev := s.env.pki.device(t, "device-1")
	hc := client.NewHTTPClient(dev, s.env.pki.roots)
	// A new connection per request, so that every request meets the
	// proxy's chances.
	hc.Transport.(*http.Transport).DisableKeepAlives = true
	c, err := client.New(client.Config{Server: "https://" + addr, HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Config{Client: c, Identity: client.IdentityOf(dev.Leaf), PollRate: 20 * time.Millisecond,
		Backoff: ieee20305.ExponentialBackoff{Initial: 5 * time.Millisecond, Max: 50 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	run(t, a)
	s.admit(t, client.IdentityOf(dev.Leaf).LFDI)
	s.converges(t, a, 1)
	if p.Counts()["reset"] == 0 {
		t.Error("no connection reset")
	}
}

// A server whose clock is two hours ahead: the agent works on the server's
// time, so a control that starts now by the server applies now.
func TestClockSkew(t *testing.T) {
	skew := 2 * time.Hour
	s := newChaosSite(t, server.Config{Clock: func() time.Time { return time.Now().Add(skew) }}, nil)
	s.control(t, s.prog, 1, time.Now().Add(skew), 600)
	// And one that, by the local clock, would be active now but by the
	// server's has ended.
	s.control(t, s.prog, 2, time.Now(), 600)
	a, lfdi := chaosAgent(t, s.env, "device-1", nil, agent.Config{})
	run(t, a)
	s.admit(t, lfdi)
	eventually(t, "the control to apply", func() bool { return len(a.Active(agent.DER)) == 1 })
	if d := a.State().Time.Offset - skew; d > 2*time.Second || d < -2*time.Second {
		t.Errorf("offset %v", a.State().Time.Offset)
	}
	if got := a.Active(agent.DER)[0].(*ieee20305.DERControl).MRID[0]; got != 1 {
		t.Errorf("active control %d", got)
	}
}

// A client whose responses are corrupted or truncated gets errors or
// documents, and never panics.
func TestCorruptResponses(t *testing.T) {
	env := newEnv(t, server.Config{})
	tree(t, env.srv, security.CertificateLFDI(env.pki.device(t, "device-1").Leaf).String())
	dev := env.pki.device(t, "device-1")
	hc := client.NewHTTPClient(dev, env.pki.roots)
	tr := &chaos.Transport{Base: hc.Transport, Faults: chaos.Faults{Seed: 4, Corrupt: 0.5, Truncate: 0.2}}
	hc.Transport = tr
	c, err := client.New(client.Config{Server: env.url, HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	failures := 0
	for i := 0; i < 200; i++ {
		err := c.Walk(ctx, client.DefaultDeviceCapabilityPath, client.WalkOptions{PageSize: 2}, func(client.Step) error { return nil })
		if err != nil {
			failures++
			if !errors.Is(err, ieee20305.ErrProtocol) && !strings.Contains(err.Error(), "ieee20305") {
				t.Fatalf("an error of no kind: %v", err)
			}
		}
	}
	if failures == 0 || tr.Counts()["corrupt"] == 0 {
		t.Errorf("%d failed walks, faults %v", failures, tr.Counts())
	}
}

// Malformed requests get 4xx, never 5xx, and the server keeps serving.
func TestMalformedRequests(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	dev := env.pki.device(t, "device-1")
	tree(t, env.srv, security.CertificateLFDI(dev.Leaf).String())
	hc := client.NewHTTPClient(dev, env.pki.roots)
	valid, err := ieee20305.Marshal(examples.LogEvent(0))
	if err != nil {
		t.Fatal(err)
	}
	bodies := [][]byte{nil, []byte("<"), []byte("<?xml version=\"1.0\"?><x/>"), valid[:len(valid)/2],
		[]byte(strings.Repeat("<a>", 10000)), append([]byte(nil), valid...)}
	bodies[len(bodies)-1][len(valid)/3] = 0xFF
	paths := []string{"/edev", "/edev/1", "/edev/1/lel", "/dcap", "/edev/1/fsa?s=x", "/derp/1/derc?l=-1", "/%zz", "/edev/1/sub"}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodGet, http.MethodDelete, "PATCH"} {
		for _, p := range paths {
			for _, b := range bodies {
				req, err := http.NewRequest(method, env.url+p, strings.NewReader(string(b)))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", server.MediaType)
				resp, err := hc.Do(req)
				if err != nil {
					t.Fatalf("%s %s: %v", method, p, err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode >= 500 {
					t.Errorf("%s %s with %q: %d", method, p, truncate(b), resp.StatusCode)
				}
			}
		}
	}
}

func truncate(b []byte) string {
	if len(b) > 20 {
		return string(b[:20]) + "..."
	}
	return string(b)
}

// Nothing of a server and its agents outlives them.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	func() {
		s := newChaosSite(t, server.Config{}, nil)
		s.control(t, s.prog, 1, time.Now(), 3600)
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			a, lfdi := chaosAgent(t, s.env, "leak-"+string(rune('a'+i)), nil, agent.Config{})
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = a.Run(ctx)
			}()
			s.admit(t, lfdi)
			eventually(t, "the control to apply", func() bool { return len(a.Active(agent.DER)) == 1 })
		}
		cancel()
		wg.Wait()
		for _, c := range s.env.clients {
			c.CloseIdleConnections()
		}
		s.env.close()
	}()
	eventually(t, "the goroutines to end", func() bool { return runtime.NumGoroutine() <= base+3 })
}
