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
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// derSite is a server with DERPrograms assigned to a device, and a
// ResponseSet for their Responses.
type derSite struct {
	env       *env
	admin     *server.Admin
	lfdi      security.LFDI
	fsaDerp   string
	responses string

	mu       sync.Mutex
	statuses map[string][]uint8 // by subject (hex mRID)
}

func newDERSite(t *testing.T) *derSite {
	t.Helper()
	d := &derSite{statuses: map[string][]uint8{}}
	d.env = newEnv(t, server.Config{OnResponse: func(_ context.Context, _ string, r any) {
		resp := r.(*ieee20305.DERControlResponse)
		d.mu.Lock()
		defer d.mu.Unlock()
		key := string(resp.Subject)
		d.statuses[key] = append(d.statuses[key], uint8(*resp.Status))
	}})
	ctx := context.Background()
	d.admin = d.env.srv.Admin()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.admin.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}))
	must(d.admin.Root(ctx, "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{}))
	set, err := d.admin.Create(ctx, "/rsps", examples.ResponseSet(0), "")
	must(err)
	d.responses = set + "/rsp"
	return d
}

// device provisions the device of the agent and assigns it an FSA with a
// DERProgramList.
func (d *derSite) device(t *testing.T, lfdi security.LFDI) {
	t.Helper()
	ctx := context.Background()
	d.lfdi = lfdi
	if _, err := d.admin.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true}); err != nil {
		t.Fatal(err)
	}
	fsa, err := d.admin.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0))
	if err != nil {
		t.Fatal(err)
	}
	d.fsaDerp = fsa + "/derp"
	if err := d.admin.NewList(ctx, "DERProgramList", d.fsaDerp, fsa, "DERProgramListLink", lfdi.String()); err != nil {
		t.Fatal(err)
	}
}

// program creates a DERProgram of primacy p in the device's FSA.
func (d *derSite) program(t *testing.T, primacy uint8) string {
	t.Helper()
	ctx := context.Background()
	prog := examples.DERProgram(0)
	prog.Primacy = ieee20305.PrimacyType(primacy)
	href, err := d.admin.Create(ctx, "/derp", prog, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.admin.AddMember(ctx, d.fsaDerp, href); err != nil {
		t.Fatal(err)
	}
	return href
}

// control creates a DERControl of the program, from start for duration
// seconds, that sets opModConnect and asks for every Response.
func (d *derSite) control(t *testing.T, prog string, mrid byte, start time.Time, duration uint32) string {
	t.Helper()
	c := examples.DERControl(0)
	c.MRID = ieee20305.MRIDType{mrid, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(start.Unix()), Duration: ieee20305.UInt32(duration)}
	c.CreationTime = ieee20305.TimeType(time.Now().Unix())
	c.EventStatus = ieee20305.EventStatus{}
	c.RandomizeStart, c.RandomizeDuration = nil, nil
	c.DERControlBase = ieee20305.DERControlBase{OpModConnect: new(bool)}
	reply, flags := d.responses, ieee20305.HexBinary8{schedule.RespondReceived | schedule.RespondSpecific}
	c.ReplyTo, c.ResponseRequired = &reply, &flags
	href, err := d.admin.Create(context.Background(), prog+"/derc", c, "")
	if err != nil {
		t.Fatal(err)
	}
	return href
}

func (d *derSite) responsesOf(mrid byte) []uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]uint8(nil), d.statuses[string(ieee20305.MRIDType{mrid, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})]...)
}

func has(statuses []uint8, want ...uint8) bool {
	for _, w := range want {
		found := false
		for _, s := range statuses {
			found = found || s == w
		}
		if !found {
			return false
		}
	}
	return true
}

// A DERControl runs its course: the server marks it active, the agent
// applies it and responds received, started and completed.
func TestDERControlLifecycle(t *testing.T) {
	d := newDERSite(t)
	var mu sync.Mutex
	var transitions []schedule.Transition
	a, lfdi := newAgent(t, d.env, "device-1", agent.Config{OnDERControl: func(_ context.Context, tr schedule.Transition) error {
		mu.Lock()
		defer mu.Unlock()
		transitions = append(transitions, tr)
		return nil
	}})
	d.device(t, lfdi)
	prog := d.program(t, 1)
	ctl := d.control(t, prog, 1, time.Now(), 2)
	run(t, a)
	eventually(t, "the start", func() bool { return len(a.ActiveDERControls()) == 1 })
	// The server knows it is active.
	ctx := context.Background()
	eventually(t, "the server's status", func() bool {
		n, err := d.admin.Get(ctx, ctl)
		return err == nil && n.Value.(*ieee20305.DERControl).EventStatus.CurrentStatus == ieee20305.UInt8(schedule.Active)
	})
	c := d.env.client(t, d.env.pki.device(t, "device-1"), nil)
	var active ieee20305.DERControlList
	if err := c.GetList(ctx, prog+"/actderc", client0(), &active); err != nil || len(active.DERControl) != 1 {
		t.Errorf("ActiveDERControlList: %d, %v", len(active.DERControl), err)
	}
	eventually(t, "the Responses", func() bool {
		return has(d.responsesOf(1), schedule.ResponseReceived, schedule.ResponseStarted, schedule.ResponseCompleted)
	})
	// The server takes the last Response before the agent counts it.
	eventually(t, "the agent to count its Responses", func() bool { return a.State().Responses >= 3 })
	mu.Lock()
	defer mu.Unlock()
	if len(transitions) < 3 || !transitions[0].Received() {
		t.Errorf("transitions %+v", transitions)
	}
}

// A cancelled DERControl is reported cancelled; a DERControl of a program
// of lower primacy supersedes one that conflicts with it.
func TestDERControlCancelAndSupersede(t *testing.T) {
	d := newDERSite(t)
	a, lfdi := newAgent(t, d.env, "device-1", agent.Config{})
	d.device(t, lfdi)
	low, high := d.program(t, 9), d.program(t, 1)
	later := time.Now().Add(time.Hour)
	cancelled := d.control(t, low, 1, later, 60)
	d.control(t, low, 2, later.Add(time.Hour), 600)
	d.control(t, high, 3, later.Add(time.Hour+time.Minute), 60)
	run(t, a)
	eventually(t, "the receipts", func() bool {
		return has(d.responsesOf(1), schedule.ResponseReceived) && has(d.responsesOf(3), schedule.ResponseReceived)
	})
	if err := d.admin.CancelEvent(context.Background(), cancelled, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the cancellation", func() bool { return has(d.responsesOf(1), schedule.ResponseCancelled) })
	eventually(t, "the supersession", func() bool { return has(d.responsesOf(2), schedule.ResponseSuperseded) })
	if has(d.responsesOf(3), schedule.ResponseSuperseded) {
		t.Error("the control of the program of higher precedence was superseded")
	}
	if err := d.admin.CancelEvent(context.Background(), low, false); err == nil {
		t.Error("CancelEvent of a program")
	}
}

// The DefaultDERControl of the program of highest precedence is passed on
// once per change; the CurrentDERProgram of a DER is that program.
func TestDefaultDERControlAndCurrentProgram(t *testing.T) {
	d := newDERSite(t)
	var mu sync.Mutex
	var defaults []*ieee20305.DefaultDERControl
	a, lfdi := newAgent(t, d.env, "device-1", agent.Config{OnDefaultDERControl: func(_ context.Context, dd any) error {
		mu.Lock()
		defer mu.Unlock()
		defaults = append(defaults, dd.(*ieee20305.DefaultDERControl))
		return nil
	}})
	d.device(t, lfdi)
	ctx := context.Background()
	low, high := d.program(t, 9), d.program(t, 1)
	for i, p := range []string{low, high} {
		dd := examples.DefaultDERControl(0)
		set := ieee20305.UInt32(i)
		dd.SetESDelay = &set
		if err := d.admin.Put(ctx, &server.Node{Href: p + "/dderc", Resource: "DefaultDERControl", Value: dd}); err != nil {
			t.Fatal(err)
		}
	}
	run(t, a)
	eventually(t, "polls", func() bool { return a.State().Polls >= 5 })
	mu.Lock()
	if len(defaults) != 1 || *defaults[0].SetESDelay != 1 {
		t.Errorf("defaults %+v", defaults)
	}
	mu.Unlock()
	// The device posts its DER; the operator associates both programs.
	dev := d.env.pki.device(t, "device-1")
	c := d.env.client(t, dev, nil)
	ed := d.admin.EndDevice(lfdi)
	der, err := c.Post(ctx, ed+"/der", examples.DER(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, der+"/cdp"); err == nil {
		t.Error("a CurrentDERProgram without programs")
	}
	for _, p := range []string{low, high} {
		if err := d.admin.AddMember(ctx, der+"/derp", p); err != nil {
			t.Fatal(err)
		}
	}
	var cur ieee20305.DERProgram
	if err := c.Get(ctx, der+"/cdp", &cur); err != nil || cur.GetHref() != high {
		t.Errorf("CurrentDERProgram %q, %v", cur.GetHref(), err)
	}
	// The device puts its DER settings.
	if err := c.Put(ctx, der+"/derg", examples.DERSettings(0)); err != nil {
		t.Error(err)
	}
}

// A Response sent twice is stored once; a device cannot respond for
// another.
func TestResponseDeduplication(t *testing.T) {
	d := newDERSite(t)
	dev := d.env.pki.device(t, "device-1")
	lfdi := security.CertificateLFDI(dev.Leaf)
	d.device(t, lfdi)
	c := d.env.client(t, dev, nil)
	ctx := context.Background()
	st := ieee20305.UInt8(schedule.ResponseReceived)
	r := &ieee20305.DERControlResponse{}
	r.Status, r.Subject, r.EndDeviceLFDI = &st, ieee20305.MRIDType{1}, ieee20305.HexBinary160(lfdi[:])
	first, err := c.Post(ctx, d.responses, r)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.Post(ctx, d.responses, r); err != nil || again != first {
		t.Errorf("again: %q, %v", again, err)
	}
	other := security.CertificateLFDI(d.env.pki.device(t, "device-2").Leaf)
	r.EndDeviceLFDI = ieee20305.HexBinary160(other[:])
	if _, err := c.Post(ctx, d.responses, r); err == nil {
		t.Error("a Response of another device")
	}
	r.EndDeviceLFDI = ieee20305.HexBinary160{1}
	if _, err := c.Post(ctx, d.responses, r); err == nil {
		t.Error("a Response without LFDI")
	}
}

// client0 is a page of all items.
func client0() client.ListOptions { return client.ListOptions{Limit: 255} }
