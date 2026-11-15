// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// mrid returns an mRID of tag and i.
func mrid(tag, i byte) ieee20305.MRIDType { return ieee20305.MRIDType{tag, i} }

// owned counts the items of the list at href owned by the device name.
func (h *harness) owned(t *testing.T, href, name string) int {
	t.Helper()
	ms, err := h.srv.Admin().Members(context.Background(), href)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range ms {
		if m.Owner == h.lfdi(name) {
			n++
		}
	}
	return n
}

// An aggregator registers the devices it manages and those the operator
// did not give the server; never a device the operator gave the server
// without assigning it to the aggregator, and with AssignedDevicesOnly
// only the devices the operator assigned it.
func TestAggregatorRegistration(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, server.Config{}, "agg", "op", "dev", "new", "mine")
	a := h.srv.Admin()
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("agg"), Role: server.RoleAggregator}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("op"), Role: server.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h.own(t, "new")); status != http.StatusCreated {
		t.Errorf("an aggregator registers a device the server was not given: %d", status)
	}
	if status, _, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h.own(t, "op")); status != http.StatusForbidden {
		t.Errorf("an aggregator registers an operator: %d", status)
	}
	// A provisioned device that deleted its EndDevice stays the
	// operator's: its aggregator-less record is kept, and the device
	// registers again although InBandRegistration is off.
	if status, _, _ := h.do(t, "dev", http.MethodDelete, ed, nil); status != http.StatusNoContent {
		t.Fatalf("a device deletes its EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusForbidden {
		t.Errorf("an aggregator registers a provisioned device without EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusCreated {
		t.Errorf("a provisioned device registers again: %d", status)
	}
	// An operator's deletion forgets the device.
	ed = a.EndDevice(h.id("dev"))
	if status, _, _ := h.do(t, "op", http.MethodDelete, ed, nil); status != http.StatusNoContent {
		t.Fatalf("an operator deletes an EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusForbidden {
		t.Errorf("a device the operator deleted registers: %d", status)
	}

	h2 := newHarness(t, server.Config{AssignedDevicesOnly: true}, "agg", "new", "mine")
	a2 := h2.srv.Admin()
	if _, err := a2.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h2.id("agg"), Role: server.RoleAggregator}); err != nil {
		t.Fatal(err)
	}
	mine, err := a2.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h2.id("mine"), ManagedBy: h2.id("agg")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h2.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h2.own(t, "new")); status != http.StatusForbidden {
		t.Errorf("AssignedDevicesOnly: an aggregator registers a device the server was not given: %d", status)
	}
	if _, hdr, _ := h2.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h2.own(t, "mine")); hdr.Get("Location") != mine {
		t.Errorf("AssignedDevicesOnly: an aggregator registers its device: %q", hdr.Get("Location"))
	}
	// Its aggregator deletes and registers it again: the assignment stays.
	if status, _, _ := h2.do(t, "agg", http.MethodDelete, mine, nil); status != http.StatusNoContent {
		t.Fatalf("an aggregator deletes the EndDevice of its device: %d", status)
	}
	status, hdr, _ := h2.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h2.own(t, "mine"))
	if status != http.StatusCreated || hdr.Get("Location") == mine {
		t.Fatalf("an aggregator registers its device again: %d %q", status, hdr.Get("Location"))
	}
	if status, _, _ := h2.do(t, "agg", http.MethodGet, hdr.Get("Location"), nil); status != http.StatusOK {
		t.Errorf("the aggregator reads the new EndDevice: %d", status)
	}
}

// mirrorHarness returns a harness with a MirrorUsagePointList at /mup and
// the devices a and b provisioned.
func mirrorHarness(t *testing.T, cfg server.Config) *harness {
	t.Helper()
	h := newHarness(t, cfg, "a", "b", "op")
	ctx := context.Background()
	a := h.srv.Admin()
	for _, n := range []string{"a", "b"} {
		if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id(n)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("op"), Role: server.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}); err != nil {
		t.Fatal(err)
	}
	return h
}

// postMirror posts a mirror of the device name with the mRID id, and
// returns its href.
func (h *harness) postMirror(t *testing.T, name string, id ieee20305.MRIDType) string {
	t.Helper()
	m := examples.MirrorUsagePoint(0)
	l := h.id(name)
	m.DeviceLFDI = l[:]
	m.MRID = id
	status, hdr, data := h.do(t, name, http.MethodPost, "/mup", marshal(t, m))
	if status != http.StatusCreated {
		t.Fatalf("%s posts a mirror: %d %s", name, status, data)
	}
	return hdr.Get("Location")
}

// A PUT of a MirrorUsagePoint takes a MirrorUsagePoint alone, and keeps
// the deviceLFDI of the mirror unless an operator puts it.
func TestMirrorPut(t *testing.T) {
	h := mirrorHarness(t, server.Config{})
	ctx := context.Background()
	mup := h.postMirror(t, "b", mrid(1, 0))
	if status, _, _ := h.do(t, "b", http.MethodPut, mup, marshal(t, examples.MirrorMeterReading(0))); status != http.StatusBadRequest {
		t.Errorf("PUT of a MirrorMeterReading to a mirror: %d", status)
	}
	if status, _, _ := h.do(t, "b", http.MethodPost, mup, marshal(t, examples.MirrorMeterReading(0))); status != http.StatusCreated {
		t.Errorf("POST of a MirrorMeterReading to a mirror: %d", status)
	}
	if status, _, _ := h.do(t, "op", http.MethodGet, "/mup?l=10", nil); status != http.StatusOK {
		t.Errorf("the operator lists the mirrors: %d", status)
	}
	other := examples.MirrorUsagePoint(0)
	la, lb := h.id("a"), h.id("b")
	other.DeviceLFDI = la[:]
	other.MRID = mrid(1, 0)
	if status, _, _ := h.do(t, "b", http.MethodPut, mup, marshal(t, other)); status != http.StatusNoContent {
		t.Fatalf("PUT of a mirror: %d", status)
	}
	n, err := h.srv.Admin().Get(ctx, mup)
	if err != nil || !bytes.Equal(n.Value.(*ieee20305.MirrorUsagePoint).DeviceLFDI, lb[:]) {
		t.Errorf("a device's PUT changed the deviceLFDI of its mirror: %v", err)
	}
	if status, _, _ := h.do(t, "op", http.MethodPut, mup, marshal(t, other)); status != http.StatusNoContent {
		t.Fatalf("an operator's PUT of a mirror: %d", status)
	}
	if n, _ := h.srv.Admin().Get(ctx, mup); !bytes.Equal(n.Value.(*ieee20305.MirrorUsagePoint).DeviceLFDI, la[:]) {
		t.Error("an operator's PUT kept the deviceLFDI")
	}
	// A mirror without a document takes the deviceLFDI of a device the
	// client acts for.
	if err := h.srv.Admin().Put(ctx, &server.Node{Href: "/mup/99", Resource: "MirrorUsagePoint", Owner: h.lfdi("b"), Parent: "/mup"}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "b", http.MethodPut, "/mup/99", marshal(t, other)); status != http.StatusForbidden {
		t.Errorf("PUT of a new mirror of another device: %d", status)
	}
	other.DeviceLFDI = lb[:]
	if status, _, _ := h.do(t, "b", http.MethodPut, "/mup/99", marshal(t, other)); status != http.StatusNoContent {
		t.Errorf("PUT of a new mirror of the device: %d", status)
	}
}

// A link a client wrote to a resource it may not read is sent as a link to
// none: without the all attribute, and not dropped.
func TestLinkToUnreadable(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		target string
		all    bool
	}{{"{b}/fsa", false}, {"{b}/lel", false}, {"{b}/di", false}, {"/edev/99/fsa", false}, {"{a}/lel", true}} {
		// The first PUT of the DeviceInformation chooses its links.
		h := newHarness(t, server.Config{}, "a", "b")
		adm := h.srv.Admin()
		ea, err := adm.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("a")})
		if err != nil {
			t.Fatal(err)
		}
		eb, err := adm.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("b")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adm.Create(ctx, eb+"/fsa", examples.FunctionSetAssignments(0), ""); err != nil {
			t.Fatal(err)
		}
		c.target = strings.NewReplacer("{a}", ea, "{b}", eb).Replace(c.target)
		di := examples.DeviceInformation(0)
		di.SupportedLocaleListLink = &ieee20305.SupportedLocaleListLink{}
		di.SupportedLocaleListLink.Href = c.target
		if status, _, _ := h.do(t, "a", http.MethodPut, ea+"/di", marshal(t, di)); status != http.StatusNoContent {
			t.Fatalf("PUT DeviceInformation: %d", status)
		}
		status, _, data := h.do(t, "a", http.MethodGet, ea+"/di", nil)
		if status != http.StatusOK {
			t.Fatalf("GET DeviceInformation: %d", status)
		}
		got := decode[ieee20305.DeviceInformation](t, data).SupportedLocaleListLink
		if got == nil || got.Href != c.target || (got.All != nil) != c.all {
			t.Errorf("link to %s: %+v", c.target, got)
		}
	}
}

// The mirrors a device posts, their MeterReadings, ReadingSets and
// Readings, and the FlowReservationResponses it gets are capped by
// MaxListItems; in the list of every client, per device.
func TestMirrorAndFlowBounds(t *testing.T) {
	const max = 3
	h := mirrorHarness(t, server.Config{MaxListItems: max})
	ctx := context.Background()
	adm := h.srv.Admin()
	first := h.postMirror(t, "b", mrid(9, 0))
	var last string
	for i := range byte(6) {
		last = h.postMirror(t, "a", mrid(1, i))
	}
	if n := h.owned(t, "/mup", "a"); n != max {
		t.Errorf("%d mirrors of a", n)
	}
	if n := h.owned(t, "/mup", "b"); n != 1 {
		t.Errorf("%d mirrors of b", n)
	}
	if n := h.owned(t, server.UsagePointListHref, "a"); n != max {
		t.Errorf("%d UsagePoints of a", n)
	}
	if _, err := adm.Get(ctx, first); err != nil {
		t.Errorf("the mirror of b: %v", err)
	}
	// The MeterReadings of one request, one per mRID.
	var list ieee20305.MirrorMeterReadingList
	for _, i := range []byte{0, 0, 1} {
		mmr := examples.MirrorMeterReading(0)
		mmr.MRID = mrid(2, i)
		list.MirrorMeterReading = append(list.MirrorMeterReading, *mmr)
	}
	if status, _, data := h.do(t, "a", http.MethodPost, last, marshal(t, &list)); status >= 300 {
		t.Fatalf("POST of MirrorMeterReadings: %d %s", status, data)
	}
	n, err := adm.Get(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	up := n.Value.(*ieee20305.MirrorUsagePoint).UsagePointLink.Href
	upn, err := adm.Get(ctx, up)
	if err != nil {
		t.Fatal(err)
	}
	mrList := upn.Value.(*ieee20305.UsagePoint).MeterReadingListLink.Href
	if ms, _ := adm.Members(ctx, mrList); len(ms) != 2 {
		t.Errorf("%d MeterReadings for 2 mRIDs", len(ms))
	}
	list.MirrorMeterReading = nil
	for i := range byte(5) {
		mmr := examples.MirrorMeterReading(0)
		mmr.MRID = mrid(3, i)
		list.MirrorMeterReading = append(list.MirrorMeterReading, *mmr)
	}
	if status, _, _ := h.do(t, "a", http.MethodPost, last, marshal(t, &list)); status >= 300 {
		t.Fatalf("POST of MirrorMeterReadings: %d", status)
	}
	ms, _ := adm.Members(ctx, mrList)
	if len(ms) != max || !bytes.Equal(ms[max-1].Value.(*ieee20305.MeterReading).MRID, mrid(3, 4)) {
		t.Errorf("%d MeterReadings", len(ms))
	}
	// The ReadingSets of a MeterReading and their Readings.
	mmr := examples.MirrorMeterReading(0)
	mmr.MRID = mrid(3, 4)
	for i := range byte(5) {
		set := examples.MirrorReadingSet(0)
		set.MRID = mrid(4, i)
		for range 5 {
			set.Reading = append(set.Reading, *examples.Reading(0))
		}
		mmr.MirrorReadingSet = append(mmr.MirrorReadingSet, *set)
	}
	if status, _, _ := h.do(t, "a", http.MethodPost, last, marshal(t, mmr)); status != http.StatusCreated {
		t.Fatalf("POST of ReadingSets: %d", status)
	}
	rsList := ms[max-1].Value.(*ieee20305.MeterReading).ReadingSetListLink.Href
	sets, _ := adm.Members(ctx, rsList)
	if len(sets) != max {
		t.Fatalf("%d ReadingSets", len(sets))
	}
	if rs, _ := adm.Members(ctx, sets[0].Value.(*ieee20305.ReadingSet).ReadingListLink.Href); len(rs) != max {
		t.Errorf("%d Readings", len(rs))
	}
	// Deleting a mirror deletes its UsagePoint.
	if status, _, _ := h.do(t, "a", http.MethodDelete, last, nil); status != http.StatusNoContent {
		t.Fatalf("DELETE of a mirror: %d", status)
	}
	if _, err := adm.Get(ctx, up); err == nil {
		t.Error("the UsagePoint of a deleted mirror is left")
	}
	if status, _, _ := h.do(t, "b", http.MethodDelete, last, nil); status != http.StatusNotFound {
		t.Errorf("DELETE of a deleted mirror: %d", status)
	}
	if status, _, _ := h.do(t, "b", http.MethodDelete, first, nil); status != http.StatusNoContent {
		t.Errorf("DELETE of a mirror: %d", status)
	}

	// The FlowReservationResponses of a device.
	ed := adm.EndDevice(h.id("b"))
	for i := range byte(6) {
		f := examples.FlowReservationRequest(0)
		f.MRID = mrid(5, i)
		f.IntervalRequested = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(adm.Now().Unix()), Duration: 0xFFFFFFFF}
		if status, _, _ := h.do(t, "b", http.MethodPost, ed+"/frq", marshal(t, f)); status != http.StatusCreated {
			t.Fatalf("POST of a FlowReservationRequest: %d", status)
		}
	}
	if ms, _ := adm.Members(ctx, ed+"/frp"); len(ms) != max {
		t.Errorf("%d FlowReservationResponses", len(ms))
	}
}

// In a list of every client, a device's posts trim its own items, not
// another's.
func TestSharedListTrimmedPerDevice(t *testing.T) {
	h := newHarness(t, server.Config{MaxListItems: 2}, "a", "b")
	ctx := context.Background()
	adm := h.srv.Admin()
	if err := adm.Root(ctx, "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{}); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Create(ctx, "/rsps", &ieee20305.ResponseSet{}, ""); err != nil {
		t.Fatal(err)
	}
	post := func(name string, subject byte) {
		t.Helper()
		r := examples.Response(0)
		l := h.id(name)
		r.EndDeviceLFDI = l[:]
		r.Subject = mrid(7, subject)
		if status, _, _ := h.do(t, name, http.MethodPost, "/rsps/1/rsp", marshal(t, r)); status != http.StatusCreated {
			t.Fatalf("%s posts a Response: %d", name, status)
		}
	}
	post("b", 0)
	for i := range byte(5) {
		post("a", i)
	}
	if a, b := h.owned(t, "/rsps/1/rsp", "a"), h.owned(t, "/rsps/1/rsp", "b"); a != 2 || b != 1 {
		t.Errorf("Responses of a %d, of b %d", a, b)
	}
}
