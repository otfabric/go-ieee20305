// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/csipaus/csipclient"
	"github.com/otfabric/go-ieee20305/csipaus/csipserver"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/server"
)

// A CSIP-AUS site: the device registers in band, puts its connection point,
// and follows a dynamic operating envelope and the default limits.
func TestCSIPAUS(t *testing.T) {
	var mu sync.Mutex
	var responses []uint8
	env := newEnv(t, csipserver.Configure(server.Config{
		InBandRegistration: true,
		OnResponse: func(_ context.Context, _ string, r any) {
			mu.Lock()
			defer mu.Unlock()
			responses = append(responses, uint8(*r.(*ieee20305.DERControlResponse).Status))
		},
	}))
	ctx := context.Background()
	a := env.srv.Admin()
	for _, root := range []struct{ resource, href string }{
		{"DERProgramList", "/derp"}, {"ResponseSetList", "/rsps"}, {"MirrorUsagePointList", "/mup"},
	} {
		if err := a.Root(ctx, root.resource, root.href, a.Document(root.resource)); err != nil {
			t.Fatal(err)
		}
	}
	set, err := a.Create(ctx, "/rsps", &ieee20305.ResponseSet{}, "")
	if err != nil {
		t.Fatal(err)
	}

	dev := env.pki.device(t, "inverter")
	c := env.client(t, dev, csipaus.Codec{})
	var mu2 sync.Mutex
	var defaults []csipaus.Limits
	ag, err := agent.New(agent.Config{
		Client: c, Identity: client.IdentityOf(dev.Leaf), PollRate: 20 * time.Millisecond, Backoff: ieee20305.ExponentialBackoff{Initial: 10 * time.Millisecond},
		OnDefaultDERControl: func(_ context.Context, d any) error {
			l, ok := csipaus.LimitsOf(d)
			if !ok {
				t.Errorf("a DefaultDERControl of %T", d)
			}
			mu2.Lock()
			defer mu2.Unlock()
			defaults = append(defaults, l)
			return nil
		},
		OnDERControl: func(context.Context, schedule.Transition) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	run(t, ag)
	lfdi := client.IdentityOf(dev.Leaf).LFDI
	eventually(t, "the EndDevice", func() bool { return a.EndDevice(lfdi) != "" })
	if err := a.Register(ctx, lfdi, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the registration", func() bool { return ag.State().Registration != nil })

	// The connection point.
	ed, err := csipclient.EndDevice(ctx, c, a.EndDevice(lfdi))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := csipclient.ConnectionPoint(ctx, c, ed); err == nil {
		t.Error("a ConnectionPoint before the device put it")
	}
	if err := csipclient.SetConnectionPoint(ctx, c, ed, "4123456789"); err != nil {
		t.Fatal(err)
	}
	cp, err := csipclient.ConnectionPoint(ctx, c, ed)
	if err != nil || cp.ConnectionPointId != "4123456789" {
		t.Errorf("ConnectionPoint %+v, %v", cp, err)
	}

	// A program with default limits and an envelope that applies now.
	fsa, err := a.AssignFSA(ctx, lfdi, &ieee20305.FunctionSetAssignments{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String()); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{Primacy: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddMember(ctx, fsa+"/derp", prog); err != nil {
		t.Fatal(err)
	}
	if err := csipserver.SetDefaultLimits(ctx, a, prog, csipaus.Limits{Export: csipserver.Watts(5000)}); err != nil {
		t.Fatal(err)
	}
	doe := csipserver.Envelope(ieee20305.MRIDType{9}, time.Now(), time.Hour, csipaus.Limits{Export: csipserver.Watts(1500), Generation: csipserver.Watts(3000)})
	reply, flags := set+"/rsp", ieee20305.HexBinary8{schedule.RespondReceived | schedule.RespondSpecific}
	doe.ReplyTo, doe.ResponseRequired = &reply, &flags
	if _, err := a.Create(ctx, prog+"/derc", doe, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the envelope to apply", func() bool { return len(ag.Active(agent.DER)) == 1 })
	l, ok := csipaus.LimitsOf(ag.Active(agent.DER)[0])
	if !ok || l.Export == nil || l.Export.Value != 1500 || l.Generation.Value != 3000 {
		t.Errorf("limits %+v", l)
	}
	if len(ag.ActiveDERControls()) != 0 {
		t.Error("a CSIP-AUS control among the IEEE 2030.5 ones")
	}
	eventually(t, "the default limits", func() bool {
		mu2.Lock()
		defer mu2.Unlock()
		return len(defaults) == 1 && defaults[0].Export != nil && defaults[0].Export.Value == 5000
	})
	eventually(t, "the Responses", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return has(responses, schedule.ResponseReceived, schedule.ResponseStarted)
	})
	// The server lists the envelope as active, with its limits.
	var active csipaus.DERControlList
	if err := c.GetList(ctx, prog+"/actderc", client0(), &active); err != nil || len(active.DERControl) != 1 ||
		active.DERControl[0].DERControlBase.OpModExpLimW == nil {
		t.Errorf("ActiveDERControlList %+v, %v", active.DERControl, err)
	}
}

func TestCSIPClientErrors(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t, server.Config{})
	c := env.client(t, env.pki.device(t, "device-1"), csipaus.Codec{})
	ed := &csipaus.EndDevice{}
	if _, err := csipclient.ConnectionPoint(ctx, c, ed); err == nil {
		t.Error("ConnectionPoint without a link")
	}
	if err := csipclient.SetConnectionPoint(ctx, c, ed, "1"); err == nil {
		t.Error("SetConnectionPoint without a link")
	}
	if _, err := csipclient.EndDevice(ctx, c, "/nope"); err == nil {
		t.Error("EndDevice of nothing")
	}
	if _, ok := csipaus.LimitsOf(42); ok {
		t.Error("LimitsOf a number")
	}
	if err := csipserver.SetDefaultLimits(ctx, env.srv.Admin(), "/nope", csipaus.Limits{}); err == nil {
		t.Error("SetDefaultLimits of no program")
	}
}
