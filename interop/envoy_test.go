// SPDX-License-Identifier: MIT

//go:build interop

package interop

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
)

// TestEnvoy runs the client against ANU Envoy, an IEEE 2030.5-2018 /
// CSIP-AUS server, seeded with the baseline fixture of ieee20305-interop.
func TestEnvoy(t *testing.T) {
	requireDocker(t)
	img := image("IEEE20305_INTEROP_ENVOY_IMAGE", envoyImage)
	f := bakedFixture(t, img, "baseline")
	p := newPKI(t, "localhost", "127.0.0.1")
	srv := startServer(t, img, "2018", p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := newClient(t, srv, p, p.device, nil)
	id := client.IdentityOf(p.device.cert)

	dcap, err := c.DeviceCapability(ctx)
	if err != nil {
		t.Fatalf("DeviceCapability: %v", err)
	}
	if dcap.EndDeviceListLink == nil || dcap.MirrorUsagePointListLink == nil || dcap.TimeLink == nil {
		t.Fatalf("DeviceCapability lacks EndDeviceListLink, MirrorUsagePointListLink or TimeLink: %+v", dcap)
	}

	t.Run("time", func(t *testing.T) {
		var tm ieee20305.Time
		if err := c.Get(ctx, dcap.TimeLink.Href, &tm); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(time.Unix(int64(tm.CurrentTime), 0)); d > time.Minute || d < -time.Minute {
			t.Errorf("the server's time is %v off", d)
		}
	})

	// The provisioned device: found, not created, with the fixture's PIN.
	ed, created, err := c.Register(ctx, dcap.EndDeviceListLink.Href, id)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Run("register", func(t *testing.T) {
		if created {
			t.Error("Register created an EndDevice for the provisioned device")
		}
		reg, err := c.Registration(ctx, ed)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CheckPIN(reg, f.Device.PIN); err != nil {
			t.Error(err)
		}
	})

	t.Run("csip-aus EndDevice", func(t *testing.T) {
		// Envoy adds the CSIP-AUS ConnectionPointLink to EndDevice: the base
		// codec keeps it as an extension, the csipaus codec decodes it.
		if !hasExtension(ed.Extensions, csipaus.Namespace, "ConnectionPointLink") {
			t.Errorf("the base EndDevice did not keep csipaus:ConnectionPointLink: %+v", ed.Extensions)
		}
		cc := newClient(t, srv, p, p.device, csipaus.Codec{})
		var aus csipaus.EndDevice
		if err := cc.Get(ctx, *ed.Href, &aus); err != nil {
			t.Fatal(err)
		}
		if aus.ConnectionPointLink == nil || aus.ConnectionPointLink.Href == "" {
			t.Error("csipaus.EndDevice has no ConnectionPointLink")
		}
	})

	t.Run("poll-controls", func(t *testing.T) {
		if ed.FunctionSetAssignmentsListLink == nil {
			t.Fatal("the EndDevice has no FunctionSetAssignmentsListLink")
		}
		var fsas ieee20305.FunctionSetAssignmentsList
		if err := c.GetList(ctx, ed.FunctionSetAssignmentsListLink.Href, client.ListOptions{Limit: 50}, &fsas); err != nil {
			t.Fatal(err)
		}
		served := map[string][]ieee20305.DERControl{}
		primacy := map[string]ieee20305.PrimacyType{}
		for _, fsa := range fsas.FunctionSetAssignments {
			if fsa.DERProgramListLink == nil {
				continue
			}
			var programs ieee20305.DERProgramList
			if err := c.GetList(ctx, fsa.DERProgramListLink.Href, client.ListOptions{Limit: 50}, &programs); err != nil {
				t.Fatal(err)
			}
			for _, prog := range programs.DERProgram {
				name := ""
				if prog.Description != nil {
					name = string(*prog.Description)
				}
				primacy[name] = prog.Primacy
				if prog.DERControlListLink == nil {
					continue
				}
				var controls ieee20305.DERControlList
				if err := c.GetList(ctx, prog.DERControlListLink.Href, client.ListOptions{Limit: 50}, &controls); err != nil {
					t.Fatal(err)
				}
				served[name] = append(served[name], controls.DERControl...)
			}
		}
		for _, want := range f.DERPrograms {
			controls, ok := served[want.Description]
			if !ok {
				t.Errorf("no DER program %q", want.Description)
				continue
			}
			if primacy[want.Description] != ieee20305.PrimacyType(want.Primacy) {
				t.Errorf("program %q has primacy %d, want %d", want.Description, primacy[want.Description], want.Primacy)
			}
			for _, wc := range want.Controls {
				if !anyControl(controls, wc.Base, wc.Duration) {
					t.Errorf("program %q has no control %q (%v for %d s)", want.Description, wc.Description, wc.Base, wc.Duration)
				}
			}
		}
	})

	t.Run("report", func(t *testing.T) {
		mup, err := mirrorUsagePoint(f, id)
		if err != nil {
			t.Fatal(err)
		}
		loc, err := c.Post(ctx, dcap.MirrorUsagePointListLink.Href, mup)
		if err != nil {
			t.Fatal(err)
		}
		if loc == "" {
			t.Fatal("no Location for the MirrorUsagePoint")
		}
		var got ieee20305.MirrorUsagePoint
		if err := c.Get(ctx, loc, &got); err != nil {
			t.Fatal(err)
		}
		if !equalBytes(got.MRID, mup.MRID) {
			t.Errorf("the MirrorUsagePoint at %s has mRID %X, want %X", loc, []byte(got.MRID), []byte(mup.MRID))
		}
	})

	t.Run("unprovisioned device", func(t *testing.T) {
		// Envoy lets a device it was not given register itself, with a PIN
		// of its own; the PIN check is what tells the device.
		oc := newClient(t, srv, p, p.other, nil)
		oed, created, err := oc.Register(ctx, dcap.EndDeviceListLink.Href, client.IdentityOf(p.other.cert))
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if !created {
			t.Error("Register found an EndDevice for a device the server was not given")
		}
		reg, err := oc.Registration(ctx, oed)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CheckPIN(reg, f.Device.PIN); !errors.Is(err, client.ErrPINMismatch) {
			t.Errorf("CheckPIN = %v, want ErrPINMismatch", err)
		}
	})

	t.Run("device of another CA", func(t *testing.T) {
		fc := newClient(t, srv, p, p.foreign, nil)
		if _, err := fc.DeviceCapability(ctx); err == nil {
			t.Error("the server accepted a device certificate of another CA")
		}
	})
}

func hasExtension(exts []ieee20305.ExtensionElement, space, local string) bool {
	for _, e := range exts {
		if e.XMLName.Space == space && e.XMLName.Local == local {
			return true
		}
	}
	return false
}

// anyControl reports whether a control has the fixture's DERControlBase
// values (opModConnect, opModEnergize) and duration.
func anyControl(controls []ieee20305.DERControl, base map[string]any, duration uint32) bool {
	for _, dc := range controls {
		if uint32(dc.Interval.Duration) != duration {
			continue
		}
		got := map[string]any{}
		if dc.DERControlBase.OpModConnect != nil {
			got["opModConnect"] = *dc.DERControlBase.OpModConnect
		}
		if dc.DERControlBase.OpModEnergize != nil {
			got["opModEnergize"] = *dc.DERControlBase.OpModEnergize
		}
		if len(got) != len(base) {
			continue
		}
		match := true
		for k, v := range base {
			if got[k] != v {
				match = false
			}
		}
		if match {
			return true
		}
	}
	return false
}

// mirrorUsagePoint builds the fixture's MirrorUsagePoint for the device id,
// with its readings taken now.
func mirrorUsagePoint(f fixture, id client.Identity) (*ieee20305.MirrorUsagePoint, error) {
	m := f.Report.MirrorUsagePoint
	mrid, err := hex.DecodeString(m.MRID)
	if err != nil {
		return nil, err
	}
	roleFlags, err := hex.DecodeString(m.RoleFlags)
	if err != nil {
		return nil, err
	}
	desc := ieee20305.String32(m.Description)
	mup := &ieee20305.MirrorUsagePoint{DeviceLFDI: ieee20305.HexBinary160(id.LFDI[:])}
	mup.MRID = mrid
	mup.Description = &desc
	mup.RoleFlags = roleFlags
	mup.ServiceCategoryKind = ieee20305.ServiceKind(m.ServiceCategoryKind)
	mup.Status = ieee20305.UInt8(m.Status)
	now := ieee20305.TimeType(time.Now().Unix())
	for _, r := range f.Report.MeterReadings {
		rmrid, err := hex.DecodeString(r.MRID)
		if err != nil {
			return nil, err
		}
		rdesc := ieee20305.String32(r.Description)
		rt := &ieee20305.ReadingType{}
		u8 := func(name string) *uint8 {
			if v, ok := r.ReadingType[name]; ok {
				b := uint8(v)
				return &b
			}
			return nil
		}
		if v := u8("accumulationBehaviour"); v != nil {
			x := ieee20305.AccumulationBehaviourType(*v)
			rt.AccumulationBehaviour = &x
		}
		if v := u8("commodity"); v != nil {
			x := ieee20305.CommodityType(*v)
			rt.Commodity = &x
		}
		if v := u8("dataQualifier"); v != nil {
			x := ieee20305.DataQualifierType(*v)
			rt.DataQualifier = &x
		}
		if v := u8("flowDirection"); v != nil {
			x := ieee20305.FlowDirectionType(*v)
			rt.FlowDirection = &x
		}
		if v := u8("kind"); v != nil {
			x := ieee20305.KindType(*v)
			rt.Kind = &x
		}
		if v := u8("phase"); v != nil {
			x := ieee20305.PhaseCode(*v)
			rt.Phase = &x
		}
		if v := u8("uom"); v != nil {
			x := ieee20305.UomType(*v)
			rt.Uom = &x
		}
		if v, ok := r.ReadingType["powerOfTenMultiplier"]; ok {
			x := ieee20305.PowerOfTenMultiplierType(v)
			rt.PowerOfTenMultiplier = &x
		}
		value := ieee20305.Int48(r.Value)
		reading := &ieee20305.Reading{}
		reading.Value = &value
		reading.TimePeriod = &ieee20305.DateTimeInterval{Start: now}
		mmr := ieee20305.MirrorMeterReading{ReadingType: rt, Reading: reading, LastUpdateTime: &now}
		mmr.MRID = rmrid
		mmr.Description = &rdesc
		mup.MirrorMeterReading = append(mup.MirrorMeterReading, mmr)
	}
	return mup, nil
}

func equalBytes(a, b []byte) bool { return hex.EncodeToString(a) == hex.EncodeToString(b) }
