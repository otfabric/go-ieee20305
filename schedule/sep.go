// SPDX-License-Identifier: MIT

package schedule

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// AllConflict is the Conflicts of a Scheduler without one: every pair of
// events conflicts, as for pricing, messaging, prepayment and flow
// reservation.
func AllConflict(a, b Event) bool { return true }

// DERConflicts reports whether two DERControl events conflict: they set at
// least one opMod* control of DERControlBase in common, including those a
// profile adds (such as the limits of CSIP-AUS). An event whose Value is
// not a DERControl conflicts with everything.
func DERConflicts(a, b Event) bool {
	x, y := opMods(a.Value), opMods(b.Value)
	if x == nil || y == nil {
		return true
	}
	for name := range x {
		if y[name] {
			return true
		}
	}
	return false
}

// opMods returns the names of the opMod* controls a DERControl (of package
// ieee20305 or of a profile, with a DERControlBase field) sets, or nil for
// another value.
func opMods(v any) map[string]bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	base := rv.FieldByName("DERControlBase")
	if !base.IsValid() || base.Kind() != reflect.Struct {
		return nil
	}
	set := map[string]bool{}
	collectOpMods(base, set)
	return set
}

func collectOpMods(v reflect.Value, set map[string]bool) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct:
			collectOpMods(v.Field(i), set)
		case strings.HasPrefix(f.Name, "OpMod") && f.Type.Kind() == reflect.Pointer && !v.Field(i).IsNil():
			set[f.Name] = true
		}
	}
}

// DRLCConflicts reports whether two EndDeviceControl events conflict: their
// deviceCategory bits intersect. An event whose Value is not an
// EndDeviceControl conflicts with everything.
func DRLCConflicts(a, b Event) bool {
	x, y := endDeviceControl(a.Value), endDeviceControl(b.Value)
	if x == nil || y == nil {
		return true
	}
	// deviceCategory is a big-endian bit string; align the shorter one
	// on the right.
	p, q := x.DeviceCategory, y.DeviceCategory
	if len(p) < len(q) {
		p, q = q, p
	}
	off := len(p) - len(q)
	for i := range q {
		if p[off+i]&q[i] != 0 {
			return true
		}
	}
	return false
}

func endDeviceControl(v any) *ieee20305.EndDeviceControl {
	switch x := v.(type) {
	case *ieee20305.EndDeviceControl:
		return x
	case ieee20305.EndDeviceControl:
		return &x
	default:
		return nil
	}
}

// FromEvent returns the Event of the resource v, a DERControl,
// EndDeviceControl, TextMessage, TimeTariffInterval,
// SupplyInterruptionOverride or FlowReservationResponse, or a pointer to
// one, of a program with the given primacy. The Value of the Event is v.
//
// A SupplyInterruptionOverride is not an Event in IEEE 2030.5: it has no
// mRID, creation time or status, so its MRID is its href, or else its
// interval, and it is Scheduled.
//
// The error matches ieee20305.ErrInvalid for a type FromEvent does not
// know, a nil pointer, an event without mRID, or an unknown currentStatus.
func FromEvent(v any, primacy uint8, program string) (Event, error) {
	var (
		ev *ieee20305.Event
		re *ieee20305.RandomizableEvent
	)
	switch x := v.(type) {
	case *ieee20305.DERControl:
		if x != nil {
			re = &x.RandomizableEvent
		}
	case ieee20305.DERControl:
		re = &x.RandomizableEvent
	case *ieee20305.EndDeviceControl:
		if x != nil {
			re = &x.RandomizableEvent
		}
	case ieee20305.EndDeviceControl:
		re = &x.RandomizableEvent
	case *ieee20305.TimeTariffInterval:
		if x != nil {
			re = &x.RandomizableEvent
		}
	case ieee20305.TimeTariffInterval:
		re = &x.RandomizableEvent
	case *ieee20305.TextMessage:
		if x != nil {
			ev = &x.Event
		}
	case ieee20305.TextMessage:
		ev = &x.Event
	case *ieee20305.FlowReservationResponse:
		if x != nil {
			ev = &x.Event
		}
	case ieee20305.FlowReservationResponse:
		ev = &x.Event
	case *ieee20305.SupplyInterruptionOverride:
		if x != nil {
			return fromOverride(x, v, primacy, program), nil
		}
	case ieee20305.SupplyInterruptionOverride:
		return fromOverride(&x, v, primacy, program), nil
	default:
		// An event of a profile, such as the DERControl of CSIP-AUS, which
		// embeds RandomizableEvent or Event.
		switch emb := embedded(v).(type) {
		case *ieee20305.RandomizableEvent:
			re = emb
		case *ieee20305.Event:
			ev = emb
		default:
			return Event{}, fmt.Errorf("%w: schedule: %T is not an event", ieee20305.ErrInvalid, v)
		}
	}
	var e Event
	if re != nil {
		ev = &re.Event
		if re.RandomizeStart != nil {
			e.RandStart = int32(*re.RandomizeStart)
		}
		if re.RandomizeDuration != nil {
			e.RandDuration = int32(*re.RandomizeDuration)
		}
	}
	if ev == nil {
		return Event{}, fmt.Errorf("%w: schedule: nil %T", ieee20305.ErrInvalid, v)
	}
	if len(ev.MRID) == 0 {
		return Event{}, fmt.Errorf("%w: schedule: %T without mRID", ieee20305.ErrInvalid, v)
	}
	st := Status(ev.EventStatus.CurrentStatus)
	if st > Completed {
		return Event{}, fmt.Errorf("%w: schedule: %T %s has currentStatus %d", ieee20305.ErrInvalid, v, ev.MRID, st)
	}
	e.MRID = ev.MRID.String()
	e.Program = program
	e.Primacy = primacy
	e.Created = time.Unix(int64(ev.CreationTime), 0).UTC()
	e.Start = time.Unix(int64(ev.Interval.Start), 0).UTC()
	e.Duration = time.Duration(ev.Interval.Duration) * time.Second
	e.Status = st
	e.Value = v
	return e, nil
}

// embedded returns a pointer to the RandomizableEvent or Event a pointer
// to a struct embeds, or nil.
func embedded(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil
	}
	for _, name := range []string{"RandomizableEvent", "Event"} {
		f := rv.Elem().FieldByName(name)
		if f.IsValid() && f.CanAddr() {
			switch p := f.Addr().Interface().(type) {
			case *ieee20305.RandomizableEvent, *ieee20305.Event:
				return p
			}
		}
	}
	return nil
}

func fromOverride(x *ieee20305.SupplyInterruptionOverride, v any, primacy uint8, program string) Event {
	mrid := ""
	if x.Href != nil {
		mrid = *x.Href
	}
	if mrid == "" {
		mrid = "SupplyInterruptionOverride:" + strconv.FormatInt(int64(x.Interval.Start), 10) +
			"+" + strconv.FormatUint(uint64(x.Interval.Duration), 10)
	}
	return Event{
		MRID:     mrid,
		Program:  program,
		Primacy:  primacy,
		Start:    time.Unix(int64(x.Interval.Start), 0).UTC(),
		Duration: time.Duration(x.Interval.Duration) * time.Second,
		Value:    v,
	}
}

// The bits of the responseRequired attribute of a RespondableResource.
const (
	// RespondReceived asks for a Response when the event is received.
	RespondReceived byte = 0x01
	// RespondSpecific asks for a Response when the event starts, ends,
	// is cancelled or is superseded.
	RespondSpecific byte = 0x02
	// RespondUser asks for the response of the end user: opt-in,
	// opt-out, accept or reject.
	RespondUser byte = 0x04
)

// The status codes of a Response.
const (
	ResponseReceived              uint8 = 1
	ResponseStarted               uint8 = 2
	ResponseCompleted             uint8 = 3
	ResponseOptOut                uint8 = 4
	ResponseOptIn                 uint8 = 5
	ResponseCancelled             uint8 = 6
	ResponseSuperseded            uint8 = 7
	ResponsePartialOptOut         uint8 = 8
	ResponsePartialOptIn          uint8 = 9
	ResponseCompletedNoUser       uint8 = 10
	ResponseUserAccepted          uint8 = 11
	ResponseUserRejected          uint8 = 12
	ResponseAbortedServer         uint8 = 13
	ResponseAbortedProgram        uint8 = 14
	ResponseRejectedInvalid       uint8 = 252
	ResponseRejectedExpired       uint8 = 253
	ResponseRejectedNotApplicable uint8 = 254
)

// ResponseRequired reports whether the transition t calls for a Response,
// given the responseRequired flags of the event (a HexBinary8; absent is
// none), and the Response status to send:
//
//   - with RespondReceived, the receipt of an event is ResponseReceived;
//   - with RespondReceived or RespondSpecific, the receipt of an event
//     that has already ended is ResponseRejectedExpired;
//   - with RespondSpecific, becoming Active is ResponseStarted, Completed
//     ResponseCompleted, Cancelled or CancelledRandom ResponseCancelled,
//     and Superseded ResponseSuperseded.
//
// The responses of the end user are not transitions; send them when the
// user answers.
func ResponseRequired(flags []byte, t Transition) (respond bool, responseStatus uint8) {
	var f byte
	if len(flags) > 0 {
		f = flags[len(flags)-1]
	}
	if t.Received() {
		switch {
		case t.To == Completed && f&(RespondReceived|RespondSpecific) != 0:
			return true, ResponseRejectedExpired
		case t.To != Completed && f&RespondReceived != 0:
			return true, ResponseReceived
		default:
			return false, 0
		}
	}
	if f&RespondSpecific == 0 {
		return false, 0
	}
	switch t.To {
	case Active:
		return true, ResponseStarted
	case Completed:
		return true, ResponseCompleted
	case Cancelled, CancelledRandom:
		return true, ResponseCancelled
	case Superseded:
		return true, ResponseSuperseded
	default:
		return false, 0
	}
}
