// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// Many agents register at once with one server, each once, and all apply
// the control of their program and respond to it.
func TestManyAgents(t *testing.T) {
	n := 500
	if testing.Short() {
		n = 50
	}
	s := newChaosSite(t, server.Config{}, nil)
	s.control(t, s.prog, 1, time.Now(), 3600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agents := make([]*agent.Agent, n)
	ids := make([]security.LFDI, n)
	var wg sync.WaitGroup
	for i := range agents {
		agents[i], ids[i] = chaosAgent(t, s.env, fmt.Sprintf("many-%d", i), nil, agent.Config{PollRate: time.Second})
		wg.Add(1)
		go func(a *agent.Agent) {
			defer wg.Done()
			_ = a.Run(ctx)
		}(agents[i])
	}
	defer wg.Wait()
	defer cancel()
	// The operator admits them as they come, in parallel.
	var admit sync.WaitGroup
	errs := make(chan error, n)
	for _, id := range ids {
		admit.Add(1)
		go func(id security.LFDI) {
			defer admit.Done()
			errs <- s.admitWithin(id, 4*time.Minute)
		}(id)
	}
	admit.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	eventuallyWithin(t, 4*time.Minute, "every agent to apply its control", func() bool {
		for _, a := range agents {
			if len(a.Active(agent.DER)) != 1 {
				return false
			}
		}
		return true
	})
	eventuallyWithin(t, 4*time.Minute, "every Response", func() bool {
		received, started := 0, 0
		for _, st := range s.responsesOf(1) {
			switch st {
			case schedule.ResponseReceived:
				received++
			case schedule.ResponseStarted:
				started++
			}
		}
		return received == n && started == n
	})
	ms, err := s.admin.Members(context.Background(), "/edev")
	if err != nil || len(ms) != n {
		t.Errorf("%d EndDevices for %d agents (%v)", len(ms), n, err)
	}
}

// The operator changes the tree while devices read and write it: no
// request fails with 5xx, every read is consistent, and the race detector
// finds nothing.
func TestConcurrentOperatorAndDevices(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	const devices = 8
	var clients []*client.Client
	var eds []string
	for i := 0; i < devices; i++ {
		dev := env.pki.device(t, fmt.Sprintf("dev-%d", i))
		ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: security.CertificateLFDI(dev.Leaf), Register: true})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, env.client(t, dev, nil))
		eds = append(eds, ed)
	}
	deadline := time.Now().Add(2 * time.Second)
	var failures atomic.Int64
	var wg sync.WaitGroup
	// The operator: creates, changes and deletes programs and controls.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(deadline); i++ {
			prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
			if err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < 3; j++ {
				if _, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), ""); err != nil {
					t.Error(err)
					return
				}
			}
			if i%2 == 1 {
				if err := a.Delete(ctx, prog); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	// The devices: walk the tree, page through the programs, post log
	// events.
	for i, c := range clients {
		wg.Add(1)
		go func(c *client.Client, ed string) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				err := c.Walk(ctx, "/dcap", client.WalkOptions{PageSize: 3}, func(s client.Step) error {
					if l, ok := s.Doc.(ieee20305.Lister); ok {
						for _, it := range l.ListItems() {
							if h, ok := it.(ieee20305.Hrefer); !ok || h.GetHref() == "" {
								return fmt.Errorf("an item of %s without href", s.Href)
							}
						}
					}
					return nil
				})
				var se *ieee20305.StatusError
				if err != nil && (!errors.As(err, &se) || se.StatusCode >= 500) {
					failures.Add(1)
					t.Errorf("walk: %v", err)
				}
				if _, err := c.Post(ctx, ed+"/lel", examples.LogEvent(0)); err != nil {
					failures.Add(1)
					t.Errorf("log event: %v", err)
				}
			}
		}(c, eds[i])
	}
	wg.Wait()
	if failures.Load() > 0 {
		t.Errorf("%d failures", failures.Load())
	}
	// Each device has its log events, and only its own.
	for i, ed := range eds {
		var l ieee20305.LogEventList
		if err := clients[i].GetList(ctx, ed+"/lel", client.ListOptions{Limit: 1}, &l); err != nil || l.All == 0 {
			t.Errorf("device %d: %d log events, %v", i, l.All, err)
		}
		other := clients[(i+1)%devices]
		var none ieee20305.LogEventList
		if err := other.GetList(ctx, ed+"/lel", client.ListOptions{}, &none); err == nil {
			t.Errorf("device %d read the log events of device %d", (i+1)%devices, i)
		}
	}
}
