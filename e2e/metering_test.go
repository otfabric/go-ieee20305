// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/server"
)

func members(t *testing.T, a *server.Admin, list string) int {
	t.Helper()
	ms, err := a.Members(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	return len(ms)
}

// The agent mirrors its metering: one UsagePoint, one MeterReading of its
// mRID with its ReadingType and latest Reading, and the readings of each
// post added to the ReadingSet of its mRID.
func TestMirrorMetering(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	if err := a.Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}); err != nil {
		t.Fatal(err)
	}
	mmr := examples.MirrorMeterReading(0)
	mmr.MRID = ieee20305.MRIDType{1, 2, 3}
	set := examples.MirrorReadingSet(0)
	set.MRID = ieee20305.MRIDType{4, 5, 6}
	set.Reading = []ieee20305.Reading{*examples.Reading(0), *examples.Reading(0)}
	mmr.MirrorReadingSet = []ieee20305.MirrorReadingSet{*set}
	mmr.ReadingType, mmr.Reading = examples.ReadingType(0), examples.Reading(0)
	mup := examples.MirrorUsagePoint(0)
	mup.MirrorMeterReading = nil
	ag, lfdi := newAgent(t, env, "device-1", agent.Config{
		MirrorUsagePoint: mup,
		Readings: func(context.Context) ([]ieee20305.MirrorMeterReading, error) {
			return []ieee20305.MirrorMeterReading{*mmr, *mmr}, nil
		},
		PostRate: 20 * time.Millisecond,
	})
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true}); err != nil {
		t.Fatal(err)
	}
	run(t, ag)
	eventually(t, "posts", func() bool { return ag.State().Posts >= 3 })
	href := ag.State().MirrorUsagePoint
	if href != "/mup/1" {
		t.Fatalf("MirrorUsagePoint %q", href)
	}
	if again, err := ag.Mirror(ctx, mup.Clone()); err != nil || again != href {
		t.Errorf("Mirror again: %q, %v", again, err)
	}
	if members(t, a, "/mup") != 1 || members(t, a, "/upt") != 1 || members(t, a, "/upt/1/mr") != 1 || members(t, a, "/upt/1/mr/1/rs") != 1 {
		t.Error("the mirror made more than one resource of an mRID")
	}
	if n := members(t, a, "/upt/1/mr/1/rs/1/r"); n < 6 {
		t.Errorf("%d readings", n)
	}
	for _, h := range []string{"/upt/1/mr/1/rt", "/upt/1/mr/1/r"} {
		if n, err := a.Get(ctx, h); err != nil || n.Value == nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	// The device reads its UsagePoint; another device does not.
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	var up ieee20305.UsagePoint
	if err := c.Get(ctx, "/upt/1", &up); err != nil || up.MeterReadingListLink == nil {
		t.Errorf("UsagePoint: %v", err)
	}
	other := env.client(t, env.pki.device(t, "device-2"), nil)
	if err := other.Get(ctx, "/upt/1", &up); err == nil {
		t.Error("another device reads the UsagePoint")
	}
	if err := ag.PostReadings(ctx, href); err != nil {
		t.Errorf("PostReadings of nothing: %v", err)
	}
	if err := ag.PostReadings(ctx, href, *mmr); err != nil {
		t.Errorf("PostReadings of one: %v", err)
	}
	// A device cannot mirror for another.
	bad := mup.Clone()
	bad.DeviceLFDI = make(ieee20305.HexBinary160, 20)
	if _, err := other.Post(ctx, "/mup", bad); err == nil {
		t.Error("a mirror of another device")
	}
}

func TestFilesAndFlowReservation(t *testing.T) {
	var mu sync.Mutex
	var files []string
	var flows []schedule.Transition
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	ag, lfdi := newAgent(t, env, "device-1", agent.Config{
		OnFile: func(_ context.Context, f *ieee20305.File) error {
			mu.Lock()
			defer mu.Unlock()
			files = append(files, f.GetHref())
			return nil
		},
		OnFlowReservation: func(_ context.Context, tr schedule.Transition) error {
			mu.Lock()
			defer mu.Unlock()
			flows = append(flows, tr)
			return nil
		},
	})
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "FileList", "/file", &ieee20305.FileList{}); err != nil {
		t.Fatal(err)
	}
	fsa, err := a.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.NewList(ctx, "FileList", fsa+"/file", fsa, "FileListLink", lfdi.String()); err != nil {
		t.Fatal(err)
	}
	file, err := a.Create(ctx, "/file", examples.File(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddMember(ctx, fsa+"/file", file); err != nil {
		t.Fatal(err)
	}
	run(t, ag)
	eventually(t, "polls", func() bool { return ag.State().Polls >= 4 })
	mu.Lock()
	if len(files) != 1 || files[0] != file {
		t.Errorf("files %v", files)
	}
	mu.Unlock()
	if err := ag.PutFileStatus(ctx, examples.FileStatus(0)); err != nil {
		t.Error(err)
	}
	req := examples.FlowReservationRequest(0)
	req.IntervalRequested = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(time.Now().Unix()), Duration: 600}
	if _, err := ag.RequestFlowReservation(ctx, req); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the flow reservation to start", func() bool { return len(ag.Active(agent.FlowReservation)) == 1 })
	resp := ag.Active(agent.FlowReservation)[0].(*ieee20305.FlowReservationResponse)
	if string(resp.Subject) != string(req.MRID) || resp.PowerAvailable.Value != req.PowerRequested.Value {
		t.Errorf("response %+v", resp)
	}
}

func TestFlowPolicy(t *testing.T) {
	env := newEnv(t, server.Config{FlowPolicy: func(context.Context, string, *ieee20305.FlowReservationRequest) (*ieee20305.FlowReservationResponse, error) {
		return nil, nil
	}})
	ctx := context.Background()
	c, ed, _ := env.provisioned(t, "device-1", ieee20305.Edition2023)
	if _, err := c.Post(ctx, ed+"/frq", examples.FlowReservationRequest(0)); err != nil {
		t.Fatal(err)
	}
	if n := members(t, env.srv.Admin(), ed+"/frp"); n != 0 {
		t.Errorf("%d responses to a refused request", n)
	}
}

// Billing: the active BillingPeriod of a CustomerAgreement is the one of
// now.
func TestBilling(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	if err := a.Root(ctx, "CustomerAccountList", "/bill", &ieee20305.CustomerAccountList{}); err != nil {
		t.Fatal(err)
	}
	acct, err := a.Create(ctx, "/bill", examples.CustomerAccount(0), "")
	if err != nil {
		t.Fatal(err)
	}
	agr, err := a.Create(ctx, acct+"/ca", examples.CustomerAgreement(0), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, start := range []int64{now - 7200, now - 60, now + 7200} {
		bp := examples.BillingPeriod(0)
		bp.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(start), Duration: 3600}
		if _, err := a.Create(ctx, agr+"/bp", bp, ""); err != nil {
			t.Fatal(err)
		}
	}
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	var active ieee20305.BillingPeriodList
	if err := c.GetList(ctx, agr+"/actbp", client0(), &active); err != nil || len(active.BillingPeriod) != 1 || int64(active.BillingPeriod[0].Interval.Start) != now-60 {
		t.Errorf("ActiveBillingPeriodList: %+v, %v", active.BillingPeriod, err)
	}
}

// The aggregation resources of IEEE 2030.5-2023 are not those of a 2018
// device.
func TestAggregationByEdition(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	for _, e := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		c, ed, _ := env.provisioned(t, "device-"+e.String(), e)
		var doc ieee20305.EndDevice
		if err := c.Get(ctx, ed, &doc); err != nil {
			t.Fatal(err)
		}
		has := doc.AggregatedDeviceListLink != nil
		_, err := c.Fetch(ctx, ed+"/adev")
		if e == ieee20305.Edition2023 && (!has || err != nil) {
			t.Errorf("2023: link %v, GET %v", has, err)
		}
		if e == ieee20305.Edition2018 && (has || err == nil || !strings.Contains(err.Error(), "404")) {
			t.Errorf("2018: link %v, GET %v", has, err)
		}
	}
}
