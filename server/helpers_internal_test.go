// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
)

// The reflective helpers take any value: those that are not documents of
// the shape they look for give nothing, and do not panic.
func TestReflectiveHelpers(t *testing.T) {
	var nilDC *ieee20305.DeviceCapability
	if _, ok := eventStart(nilDC); ok {
		t.Error("eventStart of a nil pointer")
	}
	if _, ok := fieldUint(42, "SFDI"); ok {
		t.Error("fieldUint of a non-pointer")
	}
	if _, ok := fieldUint(&ieee20305.Time{}, "Nope"); ok {
		t.Error("fieldUint of a missing field")
	}
	if _, ok := fieldUint(&ieee20305.Time{}, "CurrentTime"); ok {
		t.Error("fieldUint of a signed field")
	}
	if fieldInt(nilDC, "CreationTime") != 0 || fieldInt(&ieee20305.Time{}, "Nope") != 0 || fieldInt(&ieee20305.Time{Quality: 3}, "Quality") != 0 {
		t.Error("fieldInt of what has no signed field")
	}
	// setHref sets an href field that is not that of Resource.
	type ptrHref struct{ Href *string }
	type strHref struct{ Href string }
	type intHref struct{ Href int }
	p, s, i := &ptrHref{}, &strHref{}, &intHref{}
	for _, v := range []any{p, s, i, &struct{}{}, (*ptrHref)(nil), 1} {
		setHref(v, "/x")
	}
	if p.Href == nil || *p.Href != "/x" || s.Href != "/x" || i.Href != 0 {
		t.Errorf("setHref: %v %q %d", p.Href, s.Href, i.Href)
	}
	if _, ok := intervalOf(nilDC); ok {
		t.Error("intervalOf of a nil pointer")
	}
	type period struct{ TimePeriod *ieee20305.DateTimeInterval }
	if iv, ok := intervalOf(&period{TimePeriod: &ieee20305.DateTimeInterval{Start: 5, Duration: 6}}); !ok || iv.Start != 5 {
		t.Errorf("intervalOf a timePeriod pointer: %v %v", iv, ok)
	}
	if responseOf(nilDC) != nil || responseOf(1) != nil || responseOf(&ieee20305.Time{}) != nil {
		t.Error("responseOf of what is no Response")
	}
	if baseEndDevice(1) != nil || baseEndDevice(&ieee20305.Time{}) != nil {
		t.Error("baseEndDevice of what is no EndDevice")
	}
	if subscribable(1) || subscribable(&ieee20305.Time{}) {
		t.Error("subscribable of what has no subscribable")
	}
	if _, ok := mridOf(&ieee20305.Time{}); ok {
		t.Error("mridOf a Time")
	}
	if listAll((*ieee20305.EndDeviceList)(nil)) != 0 || listAll(noAll{}) != 0 {
		t.Error("listAll of what has no all")
	}
	// setLink refuses what it cannot link.
	ed := &ieee20305.EndDevice{}
	for _, c := range []struct {
		v    any
		name string
	}{
		{ieee20305.EndDevice{}, "RegistrationLink"},
		{ed, "Nope"},
		{ed, "SFDI"},
		{&ieee20305.DERControl{}, "Interval"},
	} {
		if err := setLink(c.v, c.name, "/x"); !errors.Is(err, ieee20305.ErrInvalid) {
			t.Errorf("setLink %T.%s: %v", c.v, c.name, err)
		}
	}
	// keepLinks and copyLinks leave what is not of the same type.
	keepLinks(ed, &ieee20305.Time{})
	copyLinks(reflect.ValueOf(1), reflect.ValueOf(2))
}

// noAll is a Lister without an all attribute.
type noAll struct{}

func (noAll) ListItems() []any             { return nil }
func (noAll) SetListItems([]any) error     { return nil }
func (noAll) NewListItem() any             { return nil }
func (noAll) ListItemElement() string      { return "" }
func (noAll) SetListCounts(uint32, uint32) {}

// setLink of a field that is a pointer to no struct: an error, as of any
// field that is no link. It panics, in reflect.Value.FieldByName on the
// uint32 the pointer points to: Admin.NewList with such a link panics.
func TestSetLinkPointerToScalar(t *testing.T) {
	if err := setLink(&ieee20305.DeviceCapability{}, "PollRate", "/x"); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("setLink of PollRate: %v", err)
	}
}

func TestInternalEdges(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if (&Server{store: s.base()}).base() != s.base() {
		t.Error("base of an unwrapped store")
	}
	if s.newList("Nope") != nil {
		t.Error("newList of an unknown resource")
	}
	// dressLinks goes no deeper than 8 levels, and leaves what is no link.
	s.dressLinks(ctx, Principal{}, 1)
	type odd struct{ OddLink string }
	s.dressLinks(ctx, Principal{}, &odd{OddLink: "/dcap"})
	s.dress(ctx, Principal{}, reflect.ValueOf(ieee20305.EndDevice{}), 9)
	// countMembers and filterMembers of a list there is not.
	ms := s.base().(*memStore)
	if _, err := ms.countMembers("/nope", nil); !errors.Is(err, ErrNoNode) {
		t.Errorf("countMembers: %v", err)
	}
	// The built-in hooks given a document they do not take.
	other := &Request{Principal: Principal{Role: RoleOperator}, Node: &Node{Href: "/x", Resource: "TimeList", Value: &ieee20305.EndDeviceList{}}, Doc: &ieee20305.Time{}}
	for name, f := range map[string]func(context.Context, *Request) (string, error){
		"createMirror": s.createMirror, "createResponse": s.createResponse, "createFlowReservation": s.createFlowReservation,
	} {
		if _, err := f(ctx, other); err == nil {
			t.Errorf("%s of a Time in a missing list", name)
		}
	}
	// A mirror in a list the client may not post to.
	m := &ieee20305.MirrorUsagePoint{DeviceLFDI: testLFDI[:]}
	if _, err := s.createMirror(ctx, &Request{Principal: devicePrincipal(), Node: &Node{Href: "/derp", Resource: "DERProgramList"}, Doc: m}); !errors.Is(err, ieee20305.ErrForbidden) {
		t.Errorf("createMirror in a list of the operator: %v", err)
	}
	if _, err := s.postMirrorReadings(ctx, &Request{Principal: Principal{Role: RoleOperator}, Node: &Node{Href: "/m"}, Doc: &ieee20305.Time{}}); err == nil {
		t.Error("postMirrorReadings of a Time")
	}
	if _, err := s.mirrorReading(ctx, &Node{Href: "/m", Value: &ieee20305.Time{}}, &ieee20305.MirrorMeterReading{}); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("mirrorReading of no mirror: %v", err)
	}
	if err := s.checkSubscription(ctx, &Request{Doc: &ieee20305.Time{}}); err == nil {
		// Without a NotificationClient every subscription is refused.
		t.Error("checkSubscription without notifications")
	}
	s.cfg.NotificationClient = &http.Client{}
	if err := s.checkSubscription(ctx, &Request{Doc: &ieee20305.Time{}}); err != nil {
		t.Errorf("checkSubscription of no Subscription: %v", err)
	}
	// The status of an event there is not, and a cancellation of one.
	if err := s.events.setStatus(ctx, "/nope", schedule.Active, time.Now()); !errors.Is(err, ErrNoNode) {
		t.Errorf("setStatus: %v", err)
	}
	if err := s.Admin().CancelEvent(ctx, "/nope", false); !errors.Is(err, ErrNoNode) {
		t.Errorf("CancelEvent: %v", err)
	}
	// A list resource without a document element, and NewList without a
	// link.
	s.resources["NoElementList"] = ieee20305.ResourceSpec{ID: "NoElementList"}
	if err := s.Admin().NewList(ctx, "NoElementList", "/nel", "/dcap", "", ""); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("NewList without an element: %v", err)
	}
	if err := s.Admin().NewList(ctx, "DERProgramList", "/derp", "", "", ""); err != nil {
		t.Errorf("NewList without a link: %v", err)
	}
	// newEndDevice from a template that embeds the EndDevice.
	type profileEndDevice struct {
		ieee20305.EndDevice
	}
	tmpl := &profileEndDevice{}
	tmpl.ChangedTime = 42
	href, err := s.newEndDevice(ctx, testLFDI, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.store.Get(ctx, href); err != nil || n.Value.(*ieee20305.EndDevice).ChangedTime != 42 {
		t.Errorf("EndDevice from an embedding template: %v", err)
	}
}
