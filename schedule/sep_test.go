// SPDX-License-Identifier: MIT

package schedule

import (
	"errors"
	"reflect"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// fill sets the Event fields of e.
func fill(e *ieee20305.Event, mrid byte, status uint8) {
	e.MRID = ieee20305.MRIDType{0xAB, mrid}
	e.CreationTime = 1_700_000_000
	e.Interval.Start = 1_700_000_100
	e.Interval.Duration = 900
	e.EventStatus.CurrentStatus = ieee20305.UInt8(status)
}

func randomizable(e *ieee20305.RandomizableEvent, mrid byte) {
	fill(&e.Event, mrid, 1)
	rs, rd := ieee20305.OneHourRangeType(-30), ieee20305.OneHourRangeType(60)
	e.RandomizeStart, e.RandomizeDuration = &rs, &rd
}

func TestFromEvent(t *testing.T) {
	var (
		der  ieee20305.DERControl
		edc  ieee20305.EndDeviceControl
		tti  ieee20305.TimeTariffInterval
		msg  ieee20305.TextMessage
		frr  ieee20305.FlowReservationResponse
		href = "/sio/1"
		sio  = ieee20305.SupplyInterruptionOverride{Resource: ieee20305.Resource{Href: &href}}
		bare ieee20305.SupplyInterruptionOverride
	)
	randomizable(&der.RandomizableEvent, 1)
	randomizable(&edc.RandomizableEvent, 2)
	randomizable(&tti.RandomizableEvent, 3)
	fill(&msg.Event, 4, 0)
	fill(&frr.Event, 5, 2)
	sio.Interval.Start, sio.Interval.Duration = 1_700_000_100, 60
	bare.Interval.Start, bare.Interval.Duration = 1_700_000_200, 30

	start := time.Unix(1_700_000_100, 0).UTC()
	tests := []struct {
		v      any
		mrid   string
		rand   [2]int32
		status Status
		start  time.Time
		dur    time.Duration
	}{
		{&der, "AB01", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{der, "AB01", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{&edc, "AB02", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{edc, "AB02", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{&tti, "AB03", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{tti, "AB03", [2]int32{-30, 60}, Active, start, 900 * time.Second},
		{&msg, "AB04", [2]int32{}, Scheduled, start, 900 * time.Second},
		{msg, "AB04", [2]int32{}, Scheduled, start, 900 * time.Second},
		{&frr, "AB05", [2]int32{}, Cancelled, start, 900 * time.Second},
		{frr, "AB05", [2]int32{}, Cancelled, start, 900 * time.Second},
		{&sio, "/sio/1", [2]int32{}, Scheduled, start, time.Minute},
		{sio, "/sio/1", [2]int32{}, Scheduled, start, time.Minute},
		{bare, "SupplyInterruptionOverride:1700000200+30", [2]int32{}, Scheduled, start.Add(100 * time.Second), 30 * time.Second},
	}
	for _, tt := range tests {
		e, err := FromEvent(tt.v, 3, "prog")
		if err != nil {
			t.Errorf("%T: %v", tt.v, err)
			continue
		}
		if e.MRID != tt.mrid || e.Primacy != 3 || e.Program != "prog" || e.Status != tt.status ||
			!e.Start.Equal(tt.start) || e.Duration != tt.dur || [2]int32{e.RandStart, e.RandDuration} != tt.rand {
			t.Errorf("%T: got %+v", tt.v, e)
		}
		if _, ok := tt.v.(ieee20305.SupplyInterruptionOverride); !ok {
			if _, ok := tt.v.(*ieee20305.SupplyInterruptionOverride); !ok && !e.Created.Equal(time.Unix(1_700_000_000, 0)) {
				t.Errorf("%T: created %v", tt.v, e.Created)
			}
		}
		if !reflect.DeepEqual(e.Value, tt.v) {
			t.Errorf("%T: Value is %T", tt.v, e.Value)
		}
	}
}

func TestFromEventErrors(t *testing.T) {
	var noMRID ieee20305.TextMessage
	var badStatus ieee20305.TextMessage
	fill(&badStatus.Event, 1, 6)
	for _, v := range []any{
		(*ieee20305.DERControl)(nil),
		(*ieee20305.EndDeviceControl)(nil),
		(*ieee20305.TimeTariffInterval)(nil),
		(*ieee20305.TextMessage)(nil),
		(*ieee20305.FlowReservationResponse)(nil),
		(*ieee20305.SupplyInterruptionOverride)(nil),
		nil,
		"text",
		ieee20305.Event{},
		noMRID,
		badStatus,
	} {
		if _, err := FromEvent(v, 0, ""); !errors.Is(err, ieee20305.ErrInvalid) {
			t.Errorf("%T: got %v, want ErrInvalid", v, err)
		}
	}
}

// derc returns a DERControl with the opMod fields named set.
func derc(names ...string) *ieee20305.DERControl {
	d := &ieee20305.DERControl{}
	v := reflect.ValueOf(&d.DERControlBase).Elem()
	for _, n := range names {
		f := v.FieldByName(n)
		f.Set(reflect.New(f.Type().Elem()))
	}
	return d
}

func TestDERConflicts(t *testing.T) {
	tests := []struct {
		a, b any
		want bool
	}{
		{derc("OpModMaxLimW"), derc("OpModMaxLimW"), true},
		{derc("OpModMaxLimW", "OpModConnect"), derc("OpModConnect"), true},
		{derc("OpModMaxLimW"), derc("OpModFixedPFInjectW"), false},
		{derc("OpModVoltVar"), *derc("OpModVoltVar", "OpModWattPF"), true},
		{derc(), derc("OpModConnect"), false},
		{derc("OpModMaxLimW"), "not a DERControl", true},
		{nil, derc("OpModMaxLimW"), true},
	}
	for i, tt := range tests {
		if got := DERConflicts(Event{Value: tt.a}, Event{Value: tt.b}); got != tt.want {
			t.Errorf("%d: got %v, want %v", i, got, tt.want)
		}
	}
	if n := len(opModFields()); n < 30 {
		t.Errorf("only %d opMod fields", n)
	}
}

func TestDRLCConflicts(t *testing.T) {
	edc := func(cat ...byte) *ieee20305.EndDeviceControl {
		return &ieee20305.EndDeviceControl{DeviceCategory: cat}
	}
	tests := []struct {
		a, b any
		want bool
	}{
		{edc(0x00, 0x01), edc(0x00, 0x01), true},
		{edc(0x00, 0x01), edc(0x00, 0x02), false},
		{edc(0x01, 0x00, 0x00), edc(0x00, 0x00), false},
		{edc(0x00, 0x00, 0x04), *edc(0x04), true},
		{edc(0x04), edc(0x10, 0x00, 0x04), true},
		{edc(), edc(0xFF), false},
		{edc(0x01), 42, true},
	}
	for i, tt := range tests {
		if got := DRLCConflicts(Event{Value: tt.a}, Event{Value: tt.b}); got != tt.want {
			t.Errorf("%d: got %v, want %v", i, got, tt.want)
		}
	}
	if !AllConflict(Event{}, Event{}) {
		t.Error("AllConflict")
	}
}

func TestResponseRequired(t *testing.T) {
	recv := func(st Status) Transition { return Transition{From: st, To: st} }
	move := func(from, to Status) Transition { return Transition{From: from, To: to} }
	tests := []struct {
		flags  []byte
		t      Transition
		ok     bool
		status uint8
	}{
		{nil, recv(Scheduled), false, 0},
		{[]byte{0x01}, recv(Scheduled), true, ResponseReceived},
		{[]byte{0x00, 0x01}, recv(Cancelled), true, ResponseReceived},
		{[]byte{0x02}, recv(Scheduled), false, 0},
		{[]byte{0x01}, recv(Completed), true, ResponseRejectedExpired},
		{[]byte{0x02}, recv(Completed), true, ResponseRejectedExpired},
		{[]byte{0x04}, recv(Completed), false, 0},
		{[]byte{0x01}, move(Scheduled, Active), false, 0},
		{[]byte{0x02}, move(Scheduled, Active), true, ResponseStarted},
		{[]byte{0x03}, move(Active, Completed), true, ResponseCompleted},
		{[]byte{0x02}, move(Scheduled, Completed), true, ResponseCompleted},
		{[]byte{0x02}, move(Active, Cancelled), true, ResponseCancelled},
		{[]byte{0x02}, move(Active, CancelledRandom), true, ResponseCancelled},
		{[]byte{0x02}, move(Scheduled, Superseded), true, ResponseSuperseded},
		{[]byte{0x07}, move(Active, Scheduled), false, 0},
	}
	for i, tt := range tests {
		ok, st := ResponseRequired(tt.flags, tt.t)
		if ok != tt.ok || st != tt.status {
			t.Errorf("%d: got %v %d, want %v %d", i, ok, st, tt.ok, tt.status)
		}
	}
}

// TestFromEventScheduled runs DERControls through a Scheduler with
// DERConflicts: controls of different modes coexist.
func TestFromEventScheduled(t *testing.T) {
	c := newFakeClock(time.Unix(1_700_000_000, 0))
	s := New(Options{Clock: c, Conflicts: DERConflicts})
	defer s.Close()
	mk := func(mrid byte, start uint32, mods ...string) Event {
		d := derc(mods...)
		fill(&d.Event, mrid, 0)
		d.Interval.Start = ieee20305.TimeType(start)
		d.CreationTime = ieee20305.TimeType(start) // later controls are newer
		e, err := FromEvent(d, 1, "p")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	s.Upsert(mk(1, 1_700_000_100, "OpModMaxLimW"))
	s.Upsert(mk(2, 1_700_000_200, "OpModFixedPFInjectW"))
	s.Upsert(mk(3, 1_700_000_300, "OpModMaxLimW", "OpModConnect"))
	c.Advance(400 * time.Second)
	if got := mrids(s.Active(c.Now())); got != "AB03,AB02" {
		t.Fatalf("active: %s (%s)", got, statuses(s))
	}
	if got := statuses(s); got != "AB01=Superseded AB02=Active AB03=Active" {
		t.Fatal(got)
	}
}
