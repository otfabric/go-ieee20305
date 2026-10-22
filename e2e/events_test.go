// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/server"
)

// eventSite is a server with a program of every event function set
// assigned to a device.
type eventSite struct {
	*derSite
	fsa string
}

// assign creates the list (such as DemandResponseProgramList) of the
// device's FSA, linked from field, and a root list at root, and returns a
// function that creates a program in the root list and adds it to the
// FSA's list.
func (e *eventSite) assign(t *testing.T, resource, root, part string) func(v any) string {
	t.Helper()
	ctx := context.Background()
	a := e.admin
	if err := a.Root(ctx, resource, root, e.env.srv.Admin().Document(resource)); err != nil {
		t.Fatal(err)
	}
	own := e.fsa + "/" + part
	if err := a.NewList(ctx, resource, own, e.fsa, resource+"Link", e.lfdi.String()); err != nil {
		t.Fatal(err)
	}
	return func(v any) string {
		href, err := a.Create(ctx, root, v, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.AddMember(ctx, own, href); err != nil {
			t.Fatal(err)
		}
		return href
	}
}

// event makes v (an Event type: EndDeviceControl, TextMessage, ...) an event
// with mRID mrid from now for duration seconds that asks for every
// Response, and creates it in list.
func (e *eventSite) event(t *testing.T, list string, ev *ieee20305.Event, v any, mrid byte, start time.Time, duration uint32) {
	t.Helper()
	ev.MRID = ieee20305.MRIDType{mrid, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	ev.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(start.Unix()), Duration: ieee20305.UInt32(duration)}
	ev.CreationTime = ieee20305.TimeType(time.Now().Unix())
	ev.EventStatus = ieee20305.EventStatus{}
	reply, flags := e.responses, ieee20305.HexBinary8{schedule.RespondReceived | schedule.RespondSpecific}
	ev.ReplyTo, ev.ResponseRequired = &reply, &flags
	if _, err := e.admin.Create(context.Background(), list, v, ""); err != nil {
		t.Fatal(err)
	}
}

func TestEventFunctionSets(t *testing.T) {
	d := &eventSite{derSite: &derSite{statuses: map[string][]uint8{}}}
	d.env = newEnv(t, server.Config{OnResponse: func(_ context.Context, _ string, r any) {
		var resp *ieee20305.Response
		switch r := r.(type) {
		case *ieee20305.DrResponse:
			resp = &r.Response
		case *ieee20305.TextResponse:
			resp = &r.Response
		case *ieee20305.PriceResponse:
			resp = &r.Response
		default:
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.statuses[string(resp.Subject)] = append(d.statuses[string(resp.Subject)], uint8(*resp.Status))
	}})
	ctx := context.Background()
	d.admin = d.env.srv.Admin()
	if err := d.admin.Root(ctx, "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{}); err != nil {
		t.Fatal(err)
	}
	set, err := d.admin.Create(ctx, "/rsps", examples.ResponseSet(0), "")
	if err != nil {
		t.Fatal(err)
	}
	d.responses = set + "/rsp"

	var mu sync.Mutex
	got := map[agent.FunctionSet][]schedule.Transition{}
	record := func(fs agent.FunctionSet) func(context.Context, schedule.Transition) error {
		return func(_ context.Context, tr schedule.Transition) error {
			mu.Lock()
			defer mu.Unlock()
			got[fs] = append(got[fs], tr)
			return nil
		}
	}
	a, lfdi := newAgent(t, d.env, "device-1", agent.Config{
		OnEndDeviceControl:           record(agent.DRLC),
		OnTextMessage:                record(agent.Messaging),
		OnTimeTariffInterval:         record(agent.Pricing),
		OnSupplyInterruptionOverride: record(agent.Prepayment),
	})
	if _, err := d.admin.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true}); err != nil {
		t.Fatal(err)
	}
	d.lfdi = lfdi
	if d.fsa, err = d.admin.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// DRLC: two overlapping controls of the same category in programs of
	// different primacy, and one of another category.
	dr := d.assign(t, "DemandResponseProgramList", "/dr", "dr")
	newDR := func(primacy uint8) string {
		p := examples.DemandResponseProgram(0)
		p.Primacy = ieee20305.PrimacyType(primacy)
		return dr(p)
	}
	low, high := newDR(9), newDR(1)
	edc := func(prog string, mrid byte, category byte, start time.Time) {
		c := examples.EndDeviceControl(0)
		c.DeviceCategory = ieee20305.DeviceCategoryType{0, 0, 0, category}
		c.RandomizeStart, c.RandomizeDuration = nil, nil
		d.event(t, prog+"/edc", &c.Event, c, mrid, start, 3600)
	}
	edc(low, 1, 0x01, now.Add(time.Hour))
	edc(high, 2, 0x01, now.Add(time.Hour+time.Minute))
	edc(low, 3, 0x02, now)

	// Messaging, pricing and prepayment: an event each, active now.
	msg := d.assign(t, "MessagingProgramList", "/msg", "msg")(examples.MessagingProgram(0))
	tm := examples.TextMessage(0)
	d.event(t, msg+"/txt", &tm.Event, tm, 4, now, 3600)
	tp := d.assign(t, "TariffProfileList", "/tp", "tp")(examples.TariffProfile(0))
	rc, err := d.admin.Create(ctx, tp+"/rc", examples.RateComponent(0), "")
	if err != nil {
		t.Fatal(err)
	}
	tti := examples.TimeTariffInterval(0)
	tti.RandomizeStart, tti.RandomizeDuration = nil, nil
	d.event(t, rc+"/tti", &tti.Event, tti, 5, now, 3600)
	ppy := d.assign(t, "PrepaymentList", "/ppy", "ppy")(examples.Prepayment(0))
	sio := &ieee20305.SupplyInterruptionOverride{Interval: ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix()), Duration: 3600}}
	if _, err := d.admin.Create(ctx, ppy+"/si", sio, ""); err != nil {
		t.Fatal(err)
	}

	run(t, a)
	eventually(t, "the events to be active", func() bool {
		return len(a.Active(agent.DRLC)) == 1 && len(a.Active(agent.Messaging)) == 1 &&
			len(a.Active(agent.Pricing)) == 1 && len(a.Active(agent.Prepayment)) == 1
	})
	eventually(t, "the Responses", func() bool {
		return has(d.responsesOf(3), schedule.ResponseReceived, schedule.ResponseStarted) &&
			has(d.responsesOf(4), schedule.ResponseReceived, schedule.ResponseStarted) &&
			has(d.responsesOf(5), schedule.ResponseReceived, schedule.ResponseStarted) &&
			has(d.responsesOf(1), schedule.ResponseSuperseded)
	})
	if has(d.responsesOf(2), schedule.ResponseSuperseded) {
		t.Error("the EndDeviceControl of the program of higher precedence was superseded")
	}
	mu.Lock()
	for _, fs := range []agent.FunctionSet{agent.DRLC, agent.Messaging, agent.Pricing, agent.Prepayment} {
		if len(got[fs]) == 0 {
			t.Errorf("no transitions of %v", fs)
		}
	}
	mu.Unlock()
	if n := len(a.Events(agent.DRLC)); n != 3 {
		t.Errorf("%d DRLC events", n)
	}
	// The server lists the active events of each program.
	c := d.env.client(t, d.env.pki.device(t, "device-1"), nil)
	for _, active := range []string{low + "/actedc", msg + "/acttxt", rc + "/acttti", ppy + "/actsi"} {
		l, err := c.FetchList(ctx, active, client0())
		if err != nil || len(l.ListItems()) != 1 {
			t.Errorf("%s: %v", active, err)
		}
	}
}

func TestFunctionSetNames(t *testing.T) {
	for fs, want := range map[agent.FunctionSet]string{agent.DER: "DER", agent.DRLC: "DRLC", agent.Messaging: "Messaging",
		agent.Pricing: "Pricing", agent.Prepayment: "Prepayment", 42: "FunctionSet(?)"} {
		if fs.String() != want {
			t.Errorf("%d: %s", fs, fs)
		}
	}
	env := newEnv(t, server.Config{})
	a, _ := newAgent(t, env, "device-1", agent.Config{})
	defer a.Close()
	if a.Active(42) != nil || a.Events(-1) != nil {
		t.Error("events of an unknown function set")
	}
}
