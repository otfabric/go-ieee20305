// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// eventually waits until cond holds, for at most 10 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventuallyWithin(t, 10*time.Second, what, cond)
}

// eventuallyWithin waits until cond holds, for at most d.
func eventuallyWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// run runs the agent until the test ends, and returns what Run returned on
// a channel.
func run(t *testing.T, a *agent.Agent) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done, fin := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(fin)
		done <- a.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-fin
	})
	return done
}

func newAgent(t *testing.T, env *env, name string, cfg agent.Config) (*agent.Agent, security.LFDI) {
	t.Helper()
	return newAgentIn(t, env, name, ieee20305.Edition2023, cfg)
}

// newAgentIn is newAgent with a client of an edition.
func newAgentIn(t *testing.T, env *env, name string, edition ieee20305.Edition, cfg agent.Config) (*agent.Agent, security.LFDI) {
	t.Helper()
	dev := env.pki.device(t, name)
	cfg.Client = env.client(t, dev, edition.Codec())
	cfg.Identity = client.IdentityOf(dev.Leaf)
	if cfg.PollRate == 0 {
		cfg.PollRate = 20 * time.Millisecond
	}
	cfg.Backoff = ieee20305.ExponentialBackoff{Initial: 10 * time.Millisecond, Max: 100 * time.Millisecond}
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a, cfg.Identity.LFDI
}

// The device registers in band, waits for the operator to register it,
// checks its PIN, reads its assignments and reports.
func TestAgentInBand(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	ctx := context.Background()
	assigned := make(chan int, 100)
	di := examples.DeviceInformation(0)
	a, lfdi := newAgent(t, env, "device-1", agent.Config{PIN: 123455, VerifyPIN: true, DeviceInformation: di,
		OnAssignments: func(_ context.Context, fsas []*ieee20305.FunctionSetAssignments) error {
			select {
			case assigned <- len(fsas):
			default:
			}
			return nil
		}})
	run(t, a)
	admin := env.srv.Admin()
	eventually(t, "the EndDevice", func() bool { return admin.EndDevice(lfdi) != "" })
	if a.State().Registration != nil {
		t.Fatal("registered before the operator registered it")
	}
	if err := admin.Register(ctx, lfdi, 123455); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the registration", func() bool { return a.State().Registration != nil })
	if _, err := admin.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the assignment", func() bool { return len(a.State().Assignments) == 1 })
	for n := range assigned {
		if n == 1 {
			break
		}
	}
	// What the device put and posted is on the server.
	ed := admin.EndDevice(lfdi)
	n, err := admin.Get(ctx, ed+"/di")
	if err != nil || n.Value.(*ieee20305.DeviceInformation).MfModel != di.MfModel {
		t.Errorf("DeviceInformation %+v, %v", n, err)
	}
	loc, err := a.Log(ctx, examples.LogEvent(0))
	if err != nil || loc != ed+"/lel/1" {
		t.Errorf("Log = %q, %v", loc, err)
	}
	if err := a.PutDeviceStatus(ctx, examples.DeviceStatus(0)); err != nil {
		t.Error(err)
	}
	if err := a.PutPowerStatus(ctx, examples.PowerStatus(0)); err != nil {
		t.Error(err)
	}
	if st := a.State(); st.DeviceCapability == nil || st.Polls < 1 || st.EndDevice.GetHref() != ed {
		t.Errorf("state %+v", st)
	}
}

func TestAgentPINMismatch(t *testing.T) {
	env := newEnv(t, server.Config{})
	a, lfdi := newAgent(t, env, "device-1", agent.Config{PIN: 111115, VerifyPIN: true})
	if _, err := env.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: lfdi, Register: true, PIN: 222220}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-run(t, a):
		if !errors.Is(err, client.ErrPINMismatch) {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on a PIN mismatch")
	}
}

// A provisioned device of IEEE 2030.5-2018 starts at once.
func TestAgentProvisioned2018(t *testing.T) {
	env := newEnv(t, server.Config{})
	dev := env.pki.device(t, "device-1")
	lfdi := security.CertificateLFDI(dev.Leaf)
	if _, err := env.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: lfdi, Edition: ieee20305.Edition2018, Register: true, PIN: 5}); err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Config{Client: env.client(t, dev, ieee20305.Edition2018.Codec()), Identity: client.IdentityOf(dev.Leaf), PIN: 5, VerifyPIN: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.State().EndDevice.GetHref() != "/edev/1" {
		t.Errorf("EndDevice %q", a.State().EndDevice.GetHref())
	}
}

// An agent retries while the server fails, and stops with its context.
func TestAgentRetries(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	_ = env.srv.Close() // 503 from now on
	a, _ := newAgent(t, env, "device-1", agent.Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v", err)
	}
	if a.State().EndDevice != nil {
		t.Error("started against a closed server")
	}
}

// An agent keeps polling when a poll fails, and picks up again.
func TestAgentPollFailure(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	fail := make(chan bool, 1)
	fail <- false
	a, lfdi := newAgent(t, env, "device-1", agent.Config{OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
		select {
		case f := <-fail:
			if !f {
				fail <- true
				return nil
			}
			return errors.New("handler failed")
		default:
			return nil
		}
	}})
	if err := env.srv.Admin().Register(context.Background(), lfdi, 0); err == nil {
		t.Fatal("Register before the device posted its EndDevice")
	}
	run(t, a)
	eventually(t, "the EndDevice", func() bool { return env.srv.Admin().EndDevice(lfdi) != "" })
	if err := env.srv.Admin().Register(context.Background(), lfdi, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, "polls after a failed one", func() bool { return a.State().Polls >= 4 })
}

func TestAgentConfig(t *testing.T) {
	if _, err := agent.New(agent.Config{}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("New without Client: %v", err)
	}
	env := newEnv(t, server.Config{})
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	if _, err := agent.New(agent.Config{Client: c}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("New without Identity: %v", err)
	}
	a, err := agent.New(agent.Config{Client: c, Identity: client.Identity{SFDI: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.Log(ctx, examples.LogEvent(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("Log before Start: %v", err)
	}
	if err := a.PutDeviceStatus(ctx, examples.DeviceStatus(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("PutDeviceStatus before Start: %v", err)
	}
	if err := a.PutPowerStatus(ctx, examples.PowerStatus(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("PutPowerStatus before Start: %v", err)
	}
	if err := a.PutFileStatus(ctx, examples.FileStatus(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("PutFileStatus before Start: %v", err)
	}
	if _, err := a.RequestFlowReservation(ctx, examples.FlowReservationRequest(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("RequestFlowReservation before Start: %v", err)
	}
	if _, err := a.Mirror(ctx, examples.MirrorUsagePoint(0)); !errors.Is(err, agent.ErrNotStarted) {
		t.Errorf("Mirror before Start: %v", err)
	}
	// Without an EndDeviceList the server cannot register the device.
	if err := a.Start(ctx); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Start without EndDeviceList: %v", err)
	}
	a.Close()
	if err := a.Start(ctx); !errors.Is(err, ieee20305.ErrClosed) {
		t.Errorf("Start after Close: %v", err)
	}
	if err := a.Run(ctx); !errors.Is(err, ieee20305.ErrClosed) {
		t.Errorf("Run after Close: %v", err)
	}
}

// An agent that waits for registration reads its EndDevice again; when the
// operator removed it, the agent registers anew.
func TestAgentEndDeviceRemoved(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	a, lfdi := newAgent(t, env, "device-1", agent.Config{})
	run(t, a)
	admin := env.srv.Admin()
	eventually(t, "the EndDevice", func() bool { return admin.EndDevice(lfdi) == "/edev/1" })
	if err := admin.RemoveDevice(context.Background(), lfdi); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a new EndDevice", func() bool { return admin.EndDevice(lfdi) == "/edev/2" })
	if err := admin.Register(context.Background(), lfdi, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the registration", func() bool { return a.State().Registration != nil })
	if a.State().EndDevice.GetHref() != "/edev/2" {
		t.Errorf("EndDevice %q", a.State().EndDevice.GetHref())
	}
}

// An agent whose server's certificate does not verify stops: waiting does
// not fix that.
func TestAgentStopsOnCertificateError(t *testing.T) {
	env := newEnv(t, server.Config{InBandRegistration: true})
	other := newPKI(t)
	dev := env.pki.device(t, "device-1")
	c, err := client.New(client.Config{Server: env.url, HTTPClient: client.NewHTTPClient(dev, other.roots)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Config{Client: c, Identity: client.IdentityOf(dev.Leaf),
		Backoff: ieee20305.ExponentialBackoff{Initial: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Run(ctx); !errors.Is(err, ieee20305.ErrCertificate) {
		t.Errorf("Run = %v", err)
	}
}
