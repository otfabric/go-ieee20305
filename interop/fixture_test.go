// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// power is an ActivePower of a fixture.
type power struct {
	Value      int64 `json:"value"`
	Multiplier int8  `json:"multiplier"`
}

func (p *power) active() *ieee20305.ActivePower {
	if p == nil {
		return nil
	}
	return &ieee20305.ActivePower{Value: ieee20305.Int16(p.Value), Multiplier: ieee20305.PowerOfTenMultiplierType(p.Multiplier)}
}

// control is a control or default control of a fixture.
type control struct {
	MRID        string         `json:"mRID"`
	Description string         `json:"description"`
	Base        map[string]any `json:"base"`
	Duration    uint32         `json:"duration"`
	Start       uint32         `json:"start"`
}

// fixture is an ieee20305-interop fixture (schema 1.1).
type fixture struct {
	Name   string `json:"name"`
	Device struct {
		Serial         string `json:"serial"`
		PIN            uint32 `json:"pin"`
		DeviceCategory string `json:"deviceCategory"`
	} `json:"device"`
	DERPrograms []struct {
		MRID           string    `json:"mRID"`
		Description    string    `json:"description"`
		Primacy        uint8     `json:"primacy"`
		DefaultControl *control  `json:"defaultControl"`
		Controls       []control `json:"controls"`
	} `json:"derPrograms"`
	Report struct {
		MirrorUsagePoint struct {
			MRID                string `json:"mRID"`
			Description         string `json:"description"`
			RoleFlags           string `json:"roleFlags"`
			ServiceCategoryKind uint8  `json:"serviceCategoryKind"`
			Status              uint8  `json:"status"`
		} `json:"mirrorUsagePoint"`
		MeterReadings []struct {
			MRID        string           `json:"mRID"`
			Description string           `json:"description"`
			ReadingType map[string]int64 `json:"readingType"`
			Value       int64            `json:"value"`
		} `json:"meterReadings"`
	} `json:"report"`
	DER *struct {
		Capability struct {
			ModesSupported    string `json:"modesSupported"`
			RtgMaxW           *power `json:"rtgMaxW"`
			Type              uint8  `json:"type"`
			DoeModesSupported string `json:"doeModesSupported"`
		} `json:"capability"`
		Settings struct {
			ModesEnabled    string `json:"modesEnabled"`
			SetMaxW         *power `json:"setMaxW"`
			SetGradW        uint16 `json:"setGradW"`
			DoeModesEnabled string `json:"doeModesEnabled"`
		} `json:"settings"`
		Status struct {
			GenConnectStatus      string `json:"genConnectStatus"`
			OperationalModeStatus uint8  `json:"operationalModeStatus"`
		} `json:"status"`
		Availability struct {
			AvailabilityDuration uint32 `json:"availabilityDuration"`
			StatWAvail           *power `json:"statWAvail"`
		} `json:"availability"`
	} `json:"der"`
	LogEvent *struct {
		LogEventCode uint8  `json:"logEventCode"`
		LogEventID   uint16 `json:"logEventID"`
		LogEventPEN  uint32 `json:"logEventPEN"`
		FunctionSet  uint8  `json:"functionSet"`
		ProfileID    uint8  `json:"profileID"`
		Details      string `json:"details"`
	} `json:"logEvent"`
	FlowReservation *struct {
		MRID              string `json:"mRID"`
		Description       string `json:"description"`
		EnergyRequested   power  `json:"energyRequested"`
		PowerRequested    power  `json:"powerRequested"`
		Start             uint32 `json:"start"`
		Duration          uint32 `json:"duration"`
		DurationRequested uint16 `json:"durationRequested"`
	} `json:"flowReservation"`
	Tariffs []struct {
		MRID                      string `json:"mRID"`
		Description               string `json:"description"`
		Currency                  uint16 `json:"currency"`
		PricePowerOfTenMultiplier int8   `json:"pricePowerOfTenMultiplier"`
		Primacy                   uint8  `json:"primacy"`
		RateCode                  string `json:"rateCode"`
		RateComponents            []struct {
			MRID                string `json:"mRID"`
			Description         string `json:"description"`
			RoleFlags           string `json:"roleFlags"`
			FlowDirection       uint8  `json:"flowDirection"`
			TimeTariffIntervals []struct {
				MRID        string `json:"mRID"`
				Description string `json:"description"`
				Start       uint32 `json:"start"`
				Duration    uint32 `json:"duration"`
				Price       int32  `json:"price"`
			} `json:"timeTariffIntervals"`
		} `json:"rateComponents"`
	} `json:"tariffs"`
}

// bakedFixture reads the fixture name that img carries.
func bakedFixture(t *testing.T, img, name string) fixture {
	t.Helper()
	out, err := docker(context.Background(), "run", "--rm", img, "print-fixture", name)
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return f
}

// csipAUS reports whether a fixture is of CSIP-AUS: its controls set site
// limits.
func (f fixture) csipAUS() bool {
	for _, p := range f.DERPrograms {
		for _, c := range append(p.Controls, deref(p.DefaultControl)...) {
			for k := range c.Base {
				if strings.HasPrefix(k, "opModExpLim") || strings.HasPrefix(k, "opModImpLim") ||
					strings.HasPrefix(k, "opModGenLim") || strings.HasPrefix(k, "opModLoadLim") {
					return true
				}
			}
		}
	}
	return false
}

func deref(c *control) []control {
	if c == nil {
		return nil
	}
	return []control{*c}
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("hex %q: %v", s, err)
	}
	return b
}

// base returns the DERControlBase of a fixture's base, with the CSIP-AUS
// limits.
func base(t *testing.T, m map[string]any) csipaus.DERControlBase {
	t.Helper()
	var b csipaus.DERControlBase
	limit := func(v any) *ieee20305.ActivePower {
		o, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("a limit %v", v)
		}
		value, _ := o["value"].(float64)
		mult, _ := o["multiplier"].(float64)
		return &ieee20305.ActivePower{Value: ieee20305.Int16(value), Multiplier: ieee20305.PowerOfTenMultiplierType(mult)}
	}
	for k, v := range m {
		switch k {
		case "opModConnect", "opModEnergize":
			x, ok := v.(bool)
			if !ok {
				t.Fatalf("%s: %v", k, v)
			}
			if k == "opModConnect" {
				b.OpModConnect = &x
			} else {
				b.OpModEnergize = &x
			}
		case "opModMaxLimW":
			n, _ := v.(float64)
			b.OpModMaxLimW = &ieee20305.PerCentControlType{Value: ieee20305.PerCent(n)}
		case "opModFixedW":
			n, _ := v.(float64)
			b.OpModFixedW = &ieee20305.SignedPerCentControlType{Value: ieee20305.SignedPerCent(n)}
		case "opModExpLimW":
			b.OpModExpLimW = limit(v)
		case "opModImpLimW":
			b.OpModImpLimW = limit(v)
		case "opModGenLimW":
			b.OpModGenLimW = limit(v)
		case "opModLoadLimW":
			b.OpModLoadLimW = limit(v)
		default:
			t.Fatalf("a fixture control %s the suite does not know", k)
		}
	}
	return b
}

// sameBase reports whether b sets exactly the controls of the fixture's
// base, to its values.
func sameBase(b *csipaus.DERControlBase, m map[string]any) bool {
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
	for k, l := range map[string]*ieee20305.ActivePower{"opModExpLimW": b.OpModExpLimW, "opModImpLimW": b.OpModImpLimW,
		"opModGenLimW": b.OpModGenLimW, "opModLoadLimW": b.OpModLoadLimW} {
		if l != nil {
			got[k] = fmt.Sprintf("%d*10^%d", l.Value, l.Multiplier)
		}
	}
	if len(got) != len(m) {
		return false
	}
	for k, v := range m {
		if o, ok := v.(map[string]any); ok {
			value, _ := o["value"].(float64)
			mult, _ := o["multiplier"].(float64)
			v = fmt.Sprintf("%d*10^%d", int64(value), int64(mult))
		}
		if got[k] != v {
			return false
		}
	}
	return true
}

// ders returns the DER resources of a fixture, as put-der puts them: the
// types of CSIP-AUS when it sets their elements.
func (f fixture) ders(t *testing.T) (capability, settings, status, availability any) {
	t.Helper()
	d := f.DER
	now := ieee20305.TimeType(time.Now().Unix())
	cp := ieee20305.DERCapability{ModesSupported: unhex(t, d.Capability.ModesSupported), Type: ieee20305.DERType(d.Capability.Type)}
	if d.Capability.RtgMaxW != nil {
		cp.RtgMaxW = *d.Capability.RtgMaxW.active()
	}
	st := ieee20305.DERSettings{SetGradW: ieee20305.UInt16(d.Settings.SetGradW), UpdatedTime: now}
	if d.Settings.ModesEnabled != "" {
		m := ieee20305.DERControlType(unhex(t, d.Settings.ModesEnabled))
		st.ModesEnabled = &m
	}
	if d.Settings.SetMaxW != nil {
		st.SetMaxW = *d.Settings.SetMaxW.active()
	}
	status = &ieee20305.DERStatus{ReadingTime: now,
		GenConnectStatus:      &ieee20305.ConnectStatusType{DateTime: now, Value: unhex(t, d.Status.GenConnectStatus)},
		OperationalModeStatus: &ieee20305.OperationalModeStatusType{DateTime: now, Value: ieee20305.UInt8(d.Status.OperationalModeStatus)}}
	dur := ieee20305.UInt32(d.Availability.AvailabilityDuration)
	availability = &ieee20305.DERAvailability{ReadingTime: now, AvailabilityDuration: &dur, StatWAvail: d.Availability.StatWAvail.active()}
	if d.Capability.DoeModesSupported != "" || d.Settings.DoeModesEnabled != "" {
		c := &csipaus.DERCapability{DERCapability: cp, DoeModesSupported: unhex(t, d.Capability.DoeModesSupported)}
		s := &csipaus.DERSettings{DERSettings: st}
		if d.Settings.DoeModesEnabled != "" {
			e := csipaus.DOEControlType(unhex(t, d.Settings.DoeModesEnabled))
			s.DoeModesEnabled = &e
		}
		return c, s, status, availability
	}
	return &cp, &st, status, availability
}

// logEvent returns the LogEvent of a fixture.
func (f fixture) logEvent() *ieee20305.LogEvent {
	l := f.LogEvent
	ev := &ieee20305.LogEvent{CreatedDateTime: ieee20305.TimeType(time.Now().Unix()), LogEventCode: ieee20305.UInt8(l.LogEventCode),
		LogEventID: ieee20305.UInt16(l.LogEventID), LogEventPEN: ieee20305.PENType(l.LogEventPEN),
		FunctionSet: ieee20305.UInt8(l.FunctionSet), ProfileID: ieee20305.UInt8(l.ProfileID)}
	if l.Details != "" {
		d := ieee20305.String32(l.Details)
		ev.Details = &d
	}
	return ev
}

// flowReservation returns the FlowReservationRequest of a fixture.
func (f fixture) flowReservation(t *testing.T) *ieee20305.FlowReservationRequest {
	t.Helper()
	r := f.FlowReservation
	now := time.Now().Unix()
	req := &ieee20305.FlowReservationRequest{CreationTime: ieee20305.TimeType(now),
		EnergyRequested:   ieee20305.SignedRealEnergy{Value: ieee20305.Int48(r.EnergyRequested.Value), Multiplier: ieee20305.PowerOfTenMultiplierType(r.EnergyRequested.Multiplier)},
		PowerRequested:    *r.PowerRequested.active(),
		IntervalRequested: ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now + int64(r.Start)), Duration: ieee20305.UInt32(r.Duration)}}
	req.MRID = unhex(t, r.MRID)
	if r.DurationRequested > 0 {
		d := ieee20305.UInt16(r.DurationRequested)
		req.DurationRequested = &d
	}
	if r.Description != "" {
		d := ieee20305.String32(r.Description)
		req.Description = &d
	}
	return req
}

// seed provisions the device of the fixture on srv, with its PIN and a
// DER, and assigns it the fixture's DER programs (with their default
// controls and controls, which start now plus their start, and answer to
// a ResponseSet) and tariffs, as a server of the contract does. It returns
// the href of the device's EndDevice.
func seed(t *testing.T, srv *server.Server, f fixture, lfdi security.LFDI, edition ieee20305.Edition) string {
	t.Helper()
	ctx := context.Background()
	a := srv.Admin()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mrid := func(s string) ieee20305.MRIDType { return unhex(t, s) }
	str32 := func(s string) *ieee20305.String32 { v := ieee20305.String32(s); return &v }
	for _, r := range []struct{ resource, href string }{
		{"DERProgramList", "/derp"}, {"MirrorUsagePointList", "/mup"}, {"ResponseSetList", "/rsps"}, {"TariffProfileList", "/tp"},
	} {
		must(a.Root(ctx, r.resource, r.href, a.Document(r.resource)))
	}
	rs, err := a.Create(ctx, "/rsps", &ieee20305.ResponseSet{}, "")
	must(err)
	replyTo := rs + "/rsp"
	tmpl := &ieee20305.EndDevice{}
	if f.Device.DeviceCategory != "" {
		dc := ieee20305.DeviceCategoryType(unhex(t, f.Device.DeviceCategory))
		tmpl.DeviceCategory = &dc
	}
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Edition: edition, Register: true, PIN: f.Device.PIN, EndDevice: tmpl})
	must(err)
	_, err = a.Create(ctx, ed+"/der", &ieee20305.DER{}, lfdi.String())
	must(err)
	fsaDoc := &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{0xF5, 0xA0}}
	fsaDoc.Description = str32("interop")
	fsa, err := a.AssignFSA(ctx, lfdi, fsaDoc)
	must(err)
	must(a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String()))
	now := time.Now()
	flags := ieee20305.HexBinary8{0x03}
	for _, fp := range f.DERPrograms {
		prog := &ieee20305.DERProgram{Primacy: ieee20305.PrimacyType(fp.Primacy)}
		prog.MRID = mrid(fp.MRID)
		prog.Description = str32(fp.Description)
		href, err := a.Create(ctx, "/derp", prog, "")
		must(err)
		must(a.AddMember(ctx, fsa+"/derp", href))
		if dc := fp.DefaultControl; dc != nil {
			d := &csipaus.DefaultDERControl{DERControlBase: base(t, dc.Base)}
			d.MRID = mrid(dc.MRID)
			d.Description = str32(dc.Description)
			must(a.Put(ctx, &server.Node{Href: href + "/dderc", Resource: "DefaultDERControl", Value: defaultControl(d, f.csipAUS())}))
		}
		for _, fc := range fp.Controls {
			c := &csipaus.DERControl{DERControlBase: base(t, fc.Base)}
			c.MRID = mrid(fc.MRID)
			c.Description = str32(fc.Description)
			c.CreationTime = ieee20305.TimeType(now.Unix())
			c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix() + int64(fc.Start)), Duration: ieee20305.UInt32(fc.Duration)}
			c.ReplyTo, c.ResponseRequired = &replyTo, &flags
			_, err := a.Create(ctx, href+"/derc", derControl(c, f.csipAUS()), "")
			must(err)
		}
	}
	if len(f.Tariffs) > 0 {
		must(a.NewList(ctx, "TariffProfileList", fsa+"/tp", fsa, "TariffProfileListLink", lfdi.String()))
	}
	for _, ft := range f.Tariffs {
		tp := &ieee20305.TariffProfile{Primacy: ieee20305.PrimacyType(ft.Primacy)}
		tp.MRID = mrid(ft.MRID)
		tp.Description = str32(ft.Description)
		cur := ieee20305.CurrencyCode(ft.Currency)
		mult := ieee20305.PowerOfTenMultiplierType(ft.PricePowerOfTenMultiplier)
		tp.Currency, tp.PricePowerOfTenMultiplier = &cur, &mult
		if ft.RateCode != "" {
			rc := ieee20305.String20(ft.RateCode)
			tp.RateCode = &rc
		}
		tph, err := a.Create(ctx, "/tp", tariff(tp, f.csipAUS()), "")
		must(err)
		must(a.AddMember(ctx, fsa+"/tp", tph))
		for _, frc := range ft.RateComponents {
			rc := &ieee20305.RateComponent{RoleFlags: unhex(t, frc.RoleFlags)}
			rc.MRID = mrid(frc.MRID)
			rc.Description = str32(frc.Description)
			rch, err := a.Create(ctx, tph+"/rc", rc, "")
			must(err)
			dir := ieee20305.FlowDirectionType(frc.FlowDirection)
			must(a.Put(ctx, &server.Node{Href: rch + "/rt", Resource: "ReadingType", Value: &ieee20305.ReadingType{FlowDirection: &dir}}))
			n, err := a.Get(ctx, rch)
			must(err)
			n.Value.(*ieee20305.RateComponent).ReadingTypeLink.Href = rch + "/rt"
			must(a.Put(ctx, n))
			for _, fi := range frc.TimeTariffIntervals {
				tti := &ieee20305.TimeTariffInterval{TouTier: 1}
				tti.MRID = mrid(fi.MRID)
				tti.Description = str32(fi.Description)
				tti.CreationTime = ieee20305.TimeType(now.Unix())
				tti.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix() + int64(fi.Start)), Duration: ieee20305.UInt32(fi.Duration)}
				ttih, err := a.Create(ctx, rch+"/tti", interval(tti, rch, f.csipAUS()), "")
				must(err)
				price := ieee20305.Int32(fi.Price)
				_, err = a.Create(ctx, ttih+"/cti", &ieee20305.ConsumptionTariffInterval{ConsumptionBlock: 1, Price: &price}, "")
				must(err)
			}
		}
	}
	return ed
}

// defaultControl and derControl return the documents a server of the
// edition of the fixture serves: those of CSIP-AUS for a CSIP-AUS fixture.
func defaultControl(d *csipaus.DefaultDERControl, aus bool) any {
	if aus {
		return d
	}
	b := &ieee20305.DefaultDERControl{RespondableSubscribableIdentifiedObject: d.RespondableSubscribableIdentifiedObject,
		DERControlBase: d.DERControlBase.DERControlBase}
	return b
}

func derControl(c *csipaus.DERControl, aus bool) any {
	if aus {
		return c
	}
	return &ieee20305.DERControl{RandomizableEvent: c.RandomizableEvent, DERControlBase: c.DERControlBase.DERControlBase}
}

// tariff and interval return the documents of a TariffProfile and a
// TimeTariffInterval of rate component rc that a server of the fixture
// serves: those of CSIP-AUS for a CSIP-AUS fixture.
func tariff(tp *ieee20305.TariffProfile, aus bool) any {
	if aus {
		return &csipaus.TariffProfile{TariffProfile: *tp}
	}
	return tp
}

func interval(tti *ieee20305.TimeTariffInterval, rc string, aus bool) any {
	if aus {
		return &csipaus.TimeTariffInterval{TimeTariffInterval: *tti, RateComponentLink: ieee20305.RateComponentLink{Link: ieee20305.Link{Href: rc}}}
	}
	return tti
}
