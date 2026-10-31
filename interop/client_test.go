//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
)

// TestClient runs the client of this module against each server image, for
// each edition the image serves, each fixture it is seeded with, and each
// cipher suite both ends have.
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
				sv := caps.Versions[version].Server
				if !sv.Supported {
					t.Logf("SKIP_UNSUPPORTED %s %s server", s.name, version)
					continue
				}
				for _, fx := range fixturesOf(sv) {
					for _, via := range []transport{viaCCM8, viaGCM} {
						suite := suiteCCM8
						if via == viaGCM {
							suite = suiteGCM128
						}
						if !caps.offers(suite) {
							t.Logf("SKIP_UNSUPPORTED %s %s over %s", s.name, version, via)
							continue
						}
						// The base edition's checks run over both suites; the
						// other fixtures over one.
						if fx != "baseline" && via == viaGCM && caps.offers(suiteCCM8) {
							continue
						}
						t.Run(version+"/"+fx+"/"+string(via), func(t *testing.T) {
							t.Parallel()
							clientScenario(t, img, caps, version, fx, via)
						})
					}
				}
			}
		})
	}
}

// clientScenario starts the server image seeded with its fixture fx, and
// checks what the client of this module reads and writes against it.
func clientScenario(t *testing.T, img string, caps capabilities, version, fx string, via transport) {
	f := bakedFixture(t, img, fx)
	p := newPKI(t, f.Device.Serial, "localhost", "127.0.0.1", "host.docker.internal")
	srv := startServer(t, img, version, fx, p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		t.Fatal(err)
	}
	codec := edition.Codec()
	if f.csipAUS() {
		codec = csipaus.Codec{}
	}
	ops := caps.Versions[version].Server.Operations
	c := newClient(t, srv, p, p.device, via, codec)
	id := client.IdentityOf(p.device.cert)
	run := func(op string, fn func(t *testing.T)) {
		if !contains(ops, op) {
			t.Logf("SKIP_UNSUPPORTED %s %s %s %s", caps.Adapter, version, fx, op)
			return
		}
		t.Run(op, func(t *testing.T) {
			if b := known(caps.Adapter, "server", version+"/"+fx+"/"+op); b != nil {
				// Run it apart, to learn whether it fails.
				failed := !t.Run("run", fn)
				b.expect(t, failed, "")
				return
			}
			fn(t)
		})
	}

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
			bc := newClient(t, srv, p, p.device, via, edition.Codec())
			var base ieee20305.EndDevice
			if err := bc.Get(ctx, ed.GetHref(), &base); err != nil {
				t.Fatal(err)
			}
			if !hasExtension(base.Extensions, csipaus.Namespace, "ConnectionPointLink") {
				t.Errorf("the base EndDevice did not keep csipaus:ConnectionPointLink: %+v", base.Extensions)
			}
			cc := newClient(t, srv, p, p.device, via, csipaus.Codec{})
			var aus csipaus.EndDevice
			if err := cc.Get(ctx, ed.GetHref(), &aus); err != nil {
				t.Fatal(err)
			}
			if aus.ConnectionPointLink == nil || aus.ConnectionPointLink.Href == "" {
				t.Fatal("csipaus.EndDevice has no ConnectionPointLink")
			}
			if err := cc.Put(ctx, aus.ConnectionPointLink.Href, &csipaus.ConnectionPointType{ConnectionPointId: "NMI-INTEROP"}); err != nil {
				t.Errorf("PUT of the ConnectionPoint: %v", err)
			}
		})
	}

	run("poll-controls", func(t *testing.T) {
		checkControls(ctx, t, c, ed, f)
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
			t.Errorf("the agent has no active DERControl; events %+v", a.Events(agent.DER))
		}
	})

	run("report", func(t *testing.T) {
		if dcap.MirrorUsagePointListLink == nil {
			t.Fatal("DeviceCapability lacks a MirrorUsagePointListLink")
		}
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

	run("respond", func(t *testing.T) {
		ctl := activeControl(ctx, t, c, ed, f)
		if ctl.replyTo == "" {
			t.Fatalf("the active control %X asks for no response", []byte(ctl.mrid))
		}
		status := ieee20305.UInt8(2)
		now := ieee20305.TimeType(time.Now().Unix())
		r := &ieee20305.DERControlResponse{Response: ieee20305.Response{CreatedDateTime: &now,
			EndDeviceLFDI: ieee20305.HexBinary160(id.LFDI[:]), Status: &status, Subject: ctl.mrid}}
		if _, err := c.Post(ctx, ctl.replyTo, r); err != nil {
			t.Fatal(err)
		}
	})

	run("log-event", func(t *testing.T) {
		if ed.LogEventListLink == nil {
			t.Fatal("the EndDevice has no LogEventListLink")
		}
		loc, err := c.Post(ctx, ed.LogEventListLink.Href, f.logEvent())
		if err != nil {
			t.Fatal(err)
		}
		if loc == "" {
			t.Error("no Location for the LogEvent")
		}
	})

	run("put-der", func(t *testing.T) {
		dc := c
		if edition == ieee20305.Edition2018 {
			// The fixture's DER may set CSIP-AUS elements.
			dc = newClient(t, srv, p, p.device, via, csipaus.Codec{})
		}
		checkDER(ctx, t, dc, ed, f)
	})

	run("flow-reservation", func(t *testing.T) {
		if ed.FlowReservationRequestListLink == nil {
			t.Fatal("the EndDevice has no FlowReservationRequestListLink")
		}
		if _, err := c.Post(ctx, ed.FlowReservationRequestListLink.Href, f.flowReservation(t)); err != nil {
			t.Fatal(err)
		}
	})

	run("poll-pricing", func(t *testing.T) {
		checkPricing(ctx, t, c, dcap, ed, f)
	})

	run("subscribe", func(t *testing.T) {
		checkSubscription(ctx, t, c, srv, p, ed, f)
	})

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

// servedControl is a DERControl a server serves.
type servedControl struct {
	mrid     ieee20305.MRIDType
	base     *csipaus.DERControlBase
	start    int64
	duration uint32
	replyTo  string
}

func controlOf(it any) (servedControl, bool) {
	var ev *ieee20305.RandomizableEvent
	var base *csipaus.DERControlBase
	switch x := it.(type) {
	case *ieee20305.DERControl:
		ev, base = &x.RandomizableEvent, &csipaus.DERControlBase{DERControlBase: x.DERControlBase}
	case *csipaus.DERControl:
		ev, base = &x.RandomizableEvent, &x.DERControlBase
	default:
		return servedControl{}, false
	}
	c := servedControl{mrid: ev.MRID, base: base, start: int64(ev.Interval.Start), duration: uint32(ev.Interval.Duration)}
	if ev.ReplyTo != nil {
		c.replyTo = *ev.ReplyTo
	}
	return c, true
}

// servedProgram is a DER program a server serves, with its default control
// and controls.
type servedProgram struct {
	prog     *ieee20305.DERProgram
	dflt     *csipaus.DERControlBase
	controls []servedControl
}

// programs returns the DER programs the server serves to the device of ed,
// by description.
func programs(ctx context.Context, t *testing.T, c *client.Client, ed *ieee20305.EndDevice) map[string]*servedProgram {
	t.Helper()
	if ed.FunctionSetAssignmentsListLink == nil {
		t.Fatal("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	served := map[string]*servedProgram{}
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
			sp := &servedProgram{prog: prog}
			served[name] = sp
			if prog.DefaultDERControlLink != nil {
				doc, err := c.Fetch(ctx, prog.DefaultDERControlLink.Href)
				switch d := doc.(type) {
				case *ieee20305.DefaultDERControl:
					sp.dflt = &csipaus.DERControlBase{DERControlBase: d.DERControlBase}
				case *csipaus.DefaultDERControl:
					sp.dflt = &d.DERControlBase
				}
				if err != nil && !errors.Is(err, ieee20305.ErrNotFound) {
					return err
				}
			}
			if prog.DERControlListLink == nil {
				return nil
			}
			return c.Iterate(ctx, prog.DERControlListLink.Href, 50, func(it any) error {
				if dc, ok := controlOf(it); ok {
					sp.controls = append(sp.controls, dc)
				}
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return served
}

// checkControls checks that the server serves the DER programs of the
// fixture, with their default controls and controls, to the device of ed.
func checkControls(ctx context.Context, t *testing.T, c *client.Client, ed *ieee20305.EndDevice, f fixture) {
	t.Helper()
	served := programs(ctx, t, c, ed)
	for _, want := range f.DERPrograms {
		sp, ok := served[want.Description]
		if !ok {
			t.Errorf("no DER program %q", want.Description)
			continue
		}
		if sp.prog.Primacy != ieee20305.PrimacyType(want.Primacy) {
			t.Errorf("program %q has primacy %d, want %d", want.Description, sp.prog.Primacy, want.Primacy)
		}
		if want.DefaultControl != nil && (sp.dflt == nil || !sameBase(sp.dflt, want.DefaultControl.Base)) {
			t.Errorf("program %q has not the default control %v", want.Description, want.DefaultControl.Base)
		}
		for _, wc := range want.Controls {
			if _, ok := find(sp.controls, wc); !ok {
				t.Errorf("program %q has no control %q (%v for %d s)", want.Description, wc.Description, wc.Base, wc.Duration)
			}
		}
	}
}

// find returns the served control of the fixture's control wc: of its
// DERControlBase values and duration.
func find(controls []servedControl, wc control) (servedControl, bool) {
	for _, dc := range controls {
		if dc.duration == wc.Duration && sameBase(dc.base, wc.Base) {
			return dc, true
		}
	}
	return servedControl{}, false
}

// activeControl returns the served control of the fixture's first control
// that starts now.
func activeControl(ctx context.Context, t *testing.T, c *client.Client, ed *ieee20305.EndDevice, f fixture) servedControl {
	t.Helper()
	served := programs(ctx, t, c, ed)
	for _, p := range f.DERPrograms {
		for _, wc := range p.Controls {
			if wc.Start != 0 || served[p.Description] == nil {
				continue
			}
			if dc, ok := find(served[p.Description].controls, wc); ok {
				return dc
			}
		}
	}
	t.Fatal("the server serves no control of the fixture that starts now")
	return servedControl{}
}

// checkDER puts the fixture's DER resources to the first DER of ed, and
// reads its settings back.
func checkDER(ctx context.Context, t *testing.T, c *client.Client, ed *ieee20305.EndDevice, f fixture) {
	t.Helper()
	if ed.DERListLink == nil {
		t.Fatal("the EndDevice has no DERListLink")
	}
	var der *ieee20305.DER
	err := c.Iterate(ctx, ed.DERListLink.Href, 1, func(it any) error {
		if d, ok := it.(*ieee20305.DER); ok && der == nil {
			der = d
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if der == nil {
		t.Fatal("the EndDevice has no DER")
	}
	capability, settings, status, availability := f.ders(t)
	hrefs := map[string]string{}
	if der.DERCapabilityLink != nil {
		hrefs["DERCapability"] = der.DERCapabilityLink.Href
	}
	if der.DERSettingsLink != nil {
		hrefs["DERSettings"] = der.DERSettingsLink.Href
	}
	if der.DERStatusLink != nil {
		hrefs["DERStatus"] = der.DERStatusLink.Href
	}
	if der.DERAvailabilityLink != nil {
		hrefs["DERAvailability"] = der.DERAvailabilityLink.Href
	}
	for _, put := range []struct {
		name string
		v    any
	}{{"DERCapability", capability}, {"DERSettings", settings}, {"DERStatus", status}, {"DERAvailability", availability}} {
		if hrefs[put.name] == "" {
			t.Errorf("the DER has no %sLink", put.name)
			continue
		}
		if err := c.Put(ctx, hrefs[put.name], put.v); err != nil {
			t.Errorf("PUT of the %s: %v", put.name, err)
		}
	}
	if der.DERSettingsLink == nil || f.DER.Settings.SetMaxW == nil {
		return
	}
	var got ieee20305.DERSettings
	if err := c.Get(ctx, der.DERSettingsLink.Href, &got); err != nil {
		t.Fatal(err)
	}
	if want := f.DER.Settings.SetMaxW; int64(got.SetMaxW.Value) != want.Value || int8(got.SetMaxW.Multiplier) != want.Multiplier {
		t.Errorf("DERSettings setMaxW %d*10^%d, want %d*10^%d", got.SetMaxW.Value, got.SetMaxW.Multiplier, want.Value, want.Multiplier)
	}
}

// checkPricing walks the tariffs the server serves to the device of ed, as
// the contract's poll-pricing does, and checks those of the fixture.
func checkPricing(ctx context.Context, t *testing.T, c *client.Client, dcap *ieee20305.DeviceCapability, ed *ieee20305.EndDevice, f fixture) {
	t.Helper()
	var lists []string
	if ed.FunctionSetAssignmentsListLink != nil {
		err := c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 50, func(it any) error {
			if fsa, ok := it.(*ieee20305.FunctionSetAssignments); ok && fsa.TariffProfileListLink != nil {
				lists = append(lists, fsa.TariffProfileListLink.Href)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(lists) == 0 && dcap.TariffProfileListLink != nil {
		lists = append(lists, dcap.TariffProfileListLink.Href)
	}
	// description of tariff → of rate component → "duration/price" of its
	// intervals.
	served := map[string]map[string][]string{}
	tariffs := map[string]*ieee20305.TariffProfile{}
	for _, l := range lists {
		err := c.Iterate(ctx, l, 50, func(it any) error {
			var tp *ieee20305.TariffProfile
			switch x := it.(type) {
			case *ieee20305.TariffProfile:
				tp = x
			case *csipaus.TariffProfile:
				tp = &x.TariffProfile
			default:
				return nil
			}
			name := describe(tp.Description)
			tariffs[name], served[name] = tp, map[string][]string{}
			if tp.RateComponentListLink == nil {
				return nil
			}
			return c.Iterate(ctx, tp.RateComponentListLink.Href, 50, func(it any) error {
				rc, ok := it.(*ieee20305.RateComponent)
				if !ok || rc.TimeTariffIntervalListLink.Href == "" {
					return nil
				}
				return c.Iterate(ctx, rc.TimeTariffIntervalListLink.Href, 50, func(it any) error {
					var tti *ieee20305.TimeTariffInterval
					switch x := it.(type) {
					case *ieee20305.TimeTariffInterval:
						tti = x
					case *csipaus.TimeTariffInterval:
						tti = &x.TimeTariffInterval
					default:
						return nil
					}
					price := "none"
					if tti.ConsumptionTariffIntervalListLink != nil {
						var ctis ieee20305.ConsumptionTariffIntervalList
						if err := c.GetList(ctx, tti.ConsumptionTariffIntervalListLink.Href, client.ListOptions{Limit: 1}, &ctis); err != nil {
							return err
						}
						if len(ctis.ConsumptionTariffInterval) > 0 && ctis.ConsumptionTariffInterval[0].Price != nil {
							price = fmt.Sprint(*ctis.ConsumptionTariffInterval[0].Price)
						}
					}
					rcName := describe(rc.Description)
					served[name][rcName] = append(served[name][rcName], fmt.Sprintf("%d/%s", tti.Interval.Duration, price))
					return nil
				})
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range f.Tariffs {
		tp, ok := tariffs[want.Description]
		if !ok {
			t.Errorf("no tariff %q", want.Description)
			continue
		}
		if tp.Currency == nil || uint16(*tp.Currency) != want.Currency ||
			tp.PricePowerOfTenMultiplier == nil || int8(*tp.PricePowerOfTenMultiplier) != want.PricePowerOfTenMultiplier {
			t.Errorf("tariff %q: currency %v, multiplier %v", want.Description, tp.Currency, tp.PricePowerOfTenMultiplier)
		}
		for _, rc := range want.RateComponents {
			got, ok := served[want.Description][rc.Description]
			if !ok {
				t.Errorf("tariff %q has no rate component %q", want.Description, rc.Description)
				continue
			}
			for _, wi := range rc.TimeTariffIntervals {
				if !slices.Contains(got, fmt.Sprintf("%d/%d", wi.Duration, wi.Price)) {
					t.Errorf("rate component %q has no interval %q (%d s at %d); it has %v", rc.Description, wi.Description, wi.Duration, wi.Price, got)
				}
			}
		}
	}
}

func describe(s *ieee20305.String32) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

// checkSubscription subscribes the device of ed to the DER programs of its
// first FunctionSetAssignments, with a notification listener of the host
// that the server reaches as host.docker.internal; issues a control through
// the server's call, and waits for the notification.
func checkSubscription(ctx context.Context, t *testing.T, c *client.Client, srv *serverContainer, p *pki, ed *ieee20305.EndDevice, f fixture) {
	t.Helper()
	notes := make(chan *ieee20305.Notification, 8)
	h, err := client.NewNotificationHandler(func(_ context.Context, n *ieee20305.Notification) error {
		select {
		case notes <- n:
		default:
		}
		return nil
	}, client.NotificationConfig{Codec: c.Codec()})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	// GridAPPS-D's notifier presents no client certificate, although
	// IEEE 2030.5 authenticates both ends: the listener asks for none, so
	// that the handler is tested against it all the same.
	l := tls.NewListener(inner, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{p.server}})
	mux := http.NewServeMux()
	mux.Handle("/notify", h)
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(testWriter{t}, "notify listener: ", 0)}
	go func() { _ = hs.Serve(l) }()
	t.Cleanup(func() { _ = hs.Close() })
	uri := fmt.Sprintf("https://host.docker.internal:%d/notify", inner.Addr().(*net.TCPAddr).Port)

	if ed.SubscriptionListLink == nil {
		t.Fatal("the EndDevice has no SubscriptionListLink")
	}
	var fsa *ieee20305.FunctionSetAssignments
	err = c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 1, func(it any) error {
		if x, ok := it.(*ieee20305.FunctionSetAssignments); ok && fsa == nil {
			fsa = x
		}
		return nil
	})
	if err != nil || fsa == nil || fsa.DERProgramListLink == nil {
		t.Fatalf("no FunctionSetAssignments with DER programs: %v", err)
	}
	if _, err := c.Subscribe(ctx, ed.SubscriptionListLink.Href, fsa.DERProgramListLink.Href, uri, 1); err != nil {
		t.Fatal(err)
	}
	r := srv.call(t, "issue-control", map[string]any{"program": f.DERPrograms[0].Description,
		"description": "Notified", "base": map[string]any{"opModEnergize": true}, "duration": 600, "start": 0})
	if !r.OK {
		t.Fatalf("issue-control: %+v", r.Error)
	}
	select {
	case n := <-notes:
		if n.SubscribedResource != fsa.DERProgramListLink.Href {
			t.Errorf("a notification of %s, want %s", n.SubscribedResource, fsa.DERProgramListLink.Href)
		}
	case <-time.After(90 * time.Second):
		t.Error("no notification within 90 s")
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
