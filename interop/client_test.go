// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
)

// TestClient runs the client of this module against each server image, for
// each edition the image serves, over each cipher suite both ends have.
func TestClient(t *testing.T) {
	requireDocker(t)
	for _, s := range []struct{ name, env, def string }{
		{"envoy", "IEEE20305_INTEROP_ENVOY_IMAGE", EnvoyImage},
		{"gridappsd", "IEEE20305_INTEROP_GRIDAPPSD_IMAGE", GridAPPSDImage},
	} {
		t.Run(s.name, func(t *testing.T) {
			img := image(s.env, s.def)
			caps := capabilitiesOf(t, img)
			for _, version := range []string{"2018", "2023"} {
				if !caps.Versions[version].Server.Supported {
					t.Logf("SKIP_UNSUPPORTED %s %s server", s.name, version)
					continue
				}
				for _, via := range []transport{viaCCM8, viaGCM} {
					suite := suiteCCM8
					if via == viaGCM {
						suite = suiteGCM128
					}
					if !caps.offers(suite) {
						t.Logf("SKIP_UNSUPPORTED %s %s over %s", s.name, version, via)
						continue
					}
					t.Run(version+"/"+string(via), func(t *testing.T) {
						t.Parallel()
						clientScenario(t, img, caps, version, via)
					})
				}
			}
		})
	}
}

// clientScenario starts the server image seeded with its baseline fixture,
// and checks what the client of this module reads and writes against it.
func clientScenario(t *testing.T, img string, caps capabilities, version string, via transport) {
	f := bakedFixture(t, img, "baseline")
	p := newPKI(t, f.Device.Serial, "localhost", "127.0.0.1")
	srv := startServer(t, img, version, p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		t.Fatal(err)
	}
	codec := edition.Codec()
	ops := caps.Versions[version].Server.Operations
	c := newClient(t, srv, p, p.device, via, codec)
	id := client.IdentityOf(p.device.cert)

	dcap, err := c.DeviceCapability(ctx)
	if err != nil {
		t.Fatalf("DeviceCapability: %v", err)
	}
	if dcap.EndDeviceListLink == nil {
		t.Fatalf("DeviceCapability lacks an EndDeviceListLink: %+v", dcap)
	}
	if dcap.TimeLink != nil {
		t.Run("time", func(t *testing.T) {
			ts, err := c.Time(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if ts.Offset > time.Minute || ts.Offset < -time.Minute {
				t.Errorf("the server's time is %v off", ts.Offset)
			}
		})
	}

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

	if caps.Adapter == "envoy" {
		t.Run("csip-aus EndDevice", func(t *testing.T) {
			// Envoy adds the CSIP-AUS ConnectionPointLink to EndDevice: the
			// base codec keeps it as an extension, the csipaus codec decodes
			// it.
			if !hasExtension(ed.Extensions, csipaus.Namespace, "ConnectionPointLink") {
				t.Errorf("the base EndDevice did not keep csipaus:ConnectionPointLink: %+v", ed.Extensions)
			}
			cc := newClient(t, srv, p, p.device, via, csipaus.Codec{})
			var aus csipaus.EndDevice
			if err := cc.Get(ctx, ed.GetHref(), &aus); err != nil {
				t.Fatal(err)
			}
			if aus.ConnectionPointLink == nil || aus.ConnectionPointLink.Href == "" {
				t.Error("csipaus.EndDevice has no ConnectionPointLink")
			}
		})
	}

	if contains(ops, "poll-controls") {
		t.Run("poll-controls", func(t *testing.T) {
			checkControls(ctx, t, c, ed, f)
		})
		t.Run("agent", func(t *testing.T) {
			// The agent of this module, on the same device: it starts, and
			// schedules the fixture's control that applies now.
			a, err := agent.New(agent.Config{Client: c, Identity: id, PIN: f.Device.PIN, VerifyPIN: true})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if err := a.Start(ctx); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(30 * time.Second)
			for len(a.Active(agent.DER)) == 0 && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if len(a.Active(agent.DER)) == 0 {
				t.Errorf("no active DERControl; events %+v", a.Events(agent.DER))
			}
		})
	}

	if contains(ops, "report") && dcap.MirrorUsagePointListLink != nil {
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
			if hex.EncodeToString(got.MRID) != hex.EncodeToString(mup.MRID) {
				t.Errorf("the MirrorUsagePoint at %s has mRID %X, want %X", loc, []byte(got.MRID), []byte(mup.MRID))
			}
		})
	}

	t.Run("unprovisioned device", func(t *testing.T) {
		// A server refuses a device it was not given, or lets it register
		// itself with a PIN of its own; the PIN check is what tells the
		// device.
		oc := newClient(t, srv, p, p.other, via, codec)
		oed, created, err := oc.Register(ctx, dcap.EndDeviceListLink.Href, client.IdentityOf(p.other.cert))
		if err != nil {
			var se *ieee20305.StatusError
			if !errors.As(err, &se) || se.StatusCode/100 != 4 {
				t.Fatalf("Register: %v", err)
			}
			return
		}
		if !created {
			t.Error("Register found an EndDevice for a device the server was not given")
		}
		reg, err := oc.Registration(ctx, oed)
		if errors.Is(err, client.ErrNotRegistered) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CheckPIN(reg, f.Device.PIN); !errors.Is(err, client.ErrPINMismatch) {
			t.Errorf("CheckPIN = %v, want ErrPINMismatch", err)
		}
	})

	t.Run("device of another CA", func(t *testing.T) {
		fc := newClient(t, srv, p, p.foreign, via, codec)
		if _, err := fc.DeviceCapability(ctx); err == nil {
			t.Error("the server accepted a device certificate of another CA")
		}
	})
}

// checkControls checks that the server serves the DER programs of the
// fixture, with their controls, to the device of ed.
func checkControls(ctx context.Context, t *testing.T, c *client.Client, ed *ieee20305.EndDevice, f fixture) {
	t.Helper()
	if ed.FunctionSetAssignmentsListLink == nil {
		t.Fatal("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	served := map[string][]*ieee20305.DERControl{}
	primacy := map[string]ieee20305.PrimacyType{}
	err := c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 50, func(it any) error {
		fsa, ok := it.(*ieee20305.FunctionSetAssignments)
		if !ok || fsa.DERProgramListLink == nil {
			return nil
		}
		return c.Iterate(ctx, fsa.DERProgramListLink.Href, 50, func(it any) error {
			prog, ok := it.(*ieee20305.DERProgram)
			if !ok {
				return nil
			}
			name := ""
			if prog.Description != nil {
				name = string(*prog.Description)
			}
			primacy[name] = prog.Primacy
			if prog.DERControlListLink == nil {
				return nil
			}
			return c.Iterate(ctx, prog.DERControlListLink.Href, 50, func(it any) error {
				if dc, ok := it.(*ieee20305.DERControl); ok {
					served[name] = append(served[name], dc)
				}
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
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
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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
// values and duration.
func anyControl(controls []*ieee20305.DERControl, base map[string]any, duration uint32) bool {
	for _, dc := range controls {
		if uint32(dc.Interval.Duration) == duration && sameBase(&dc.DERControlBase, base) {
			return true
		}
	}
	return false
}

// sameBase reports whether b sets exactly the controls of the fixture's
// base, to its values.
func sameBase(b *ieee20305.DERControlBase, base map[string]any) bool {
	got := map[string]any{}
	if b.OpModConnect != nil {
		got["opModConnect"] = *b.OpModConnect
	}
	if b.OpModEnergize != nil {
		got["opModEnergize"] = *b.OpModEnergize
	}
	if b.OpModMaxLimW != nil {
		got["opModMaxLimW"] = float64(b.OpModMaxLimW.Value)
	}
	if b.OpModFixedW != nil {
		got["opModFixedW"] = float64(b.OpModFixedW.Value)
	}
	if len(got) != len(base) {
		return false
	}
	for k, v := range base {
		if got[k] != v {
			return false
		}
	}
	return true
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
