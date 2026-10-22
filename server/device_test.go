// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// id returns the LFDI of the device name.
func (h *harness) id(name string) security.LFDI { return security.CertificateLFDI(h.devices[name]) }

// own returns the EndDevice a device posts to register itself.
func (h *harness) own(t *testing.T, name string) []byte {
	t.Helper()
	l := h.id(name)
	lfdi := ieee20305.HexBinary160(l[:])
	ed := &ieee20305.EndDevice{}
	ed.SFDI, ed.LFDI, ed.ChangedTime = ieee20305.SFDIType(l.SFDI()), &lfdi, 1
	return marshal(t, ed)
}

func TestProvisionDevice(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev", "other")
	ctx := context.Background()
	a := h.srv.Admin()
	tmpl := &ieee20305.EndDevice{}
	cat := ieee20305.DeviceCategoryType{0x01}
	tmpl.DeviceCategory = &cat
	href, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), Register: true, PIN: 123455, EndDevice: tmpl})
	if err != nil || href != "/edev/1" || a.EndDevice(h.id("dev")) != href {
		t.Fatalf("ProvisionDevice = %q, %v", href, err)
	}
	// Again: the same EndDevice, with the new PIN.
	if again, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), Register: true, PIN: 111115}); err != nil || again != href {
		t.Fatalf("ProvisionDevice again = %q, %v", again, err)
	}
	status, _, data := h.do(t, "dev", http.MethodGet, href, nil)
	if status != http.StatusOK {
		t.Fatalf("GET: %d", status)
	}
	ed := decode[ieee20305.EndDevice](t, data)
	if uint64(ed.SFDI) != uint64(h.id("dev").SFDI()) || ed.DeviceCategory == nil || ed.RegistrationLink == nil || ed.LogEventListLink == nil || ed.DERListLink == nil {
		t.Fatalf("EndDevice = %s", data)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, ed.RegistrationLink.Href, nil)
	if reg := decode[ieee20305.Registration](t, data); reg.PIN != 111115 {
		t.Errorf("PIN %d", reg.PIN)
	}
	// Lists are there and empty; resources the device puts are not yet.
	if status, _, _ := h.do(t, "dev", http.MethodGet, ed.FunctionSetAssignmentsListLink.Href, nil); status != http.StatusOK {
		t.Errorf("FSA list: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, ed.DeviceInformationLink.Href, nil); status != http.StatusNotFound {
		t.Errorf("DeviceInformation before PUT: %d", status)
	}
	di := marshal(t, examples.DeviceInformation(0))
	if status, _, _ := h.do(t, "dev", http.MethodPut, ed.DeviceInformationLink.Href, di); status != http.StatusNoContent {
		t.Errorf("first PUT of DeviceInformation: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPut, ed.DeviceInformationLink.Href, di); status != http.StatusNoContent {
		t.Errorf("second PUT of DeviceInformation: %d", status)
	}
	// Another device sees nothing of it.
	for _, p := range []string{href, ed.RegistrationLink.Href, ed.DeviceInformationLink.Href} {
		if status, _, _ := h.do(t, "other", http.MethodGet, p, nil); status != http.StatusNotFound {
			t.Errorf("another device GETs %s: %d", p, status)
		}
	}
	// The device cannot change its Registration.
	reg := marshal(t, &ieee20305.Registration{PIN: 1})
	if status, _, _ := h.do(t, "dev", http.MethodPut, ed.RegistrationLink.Href, reg); status != http.StatusForbidden {
		t.Errorf("a device PUTs its Registration: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodDelete, ed.RegistrationLink.Href, nil); status != http.StatusForbidden {
		t.Errorf("a device DELETEs its Registration: %d", status)
	}
	// FunctionSetAssignments.
	fsa, err := a.AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0))
	if err != nil || fsa != "/edev/1/fsa/1" {
		t.Fatalf("AssignFSA = %q, %v", fsa, err)
	}
	if _, err := a.AssignFSA(ctx, h.id("other"), examples.FunctionSetAssignments(0)); err == nil {
		t.Error("AssignFSA to an unknown device")
	}
	if err := a.Register(ctx, h.id("other"), 1); err == nil {
		t.Error("Register of an unknown device")
	}
	// RemoveDevice deletes its tree.
	if err := a.RemoveDevice(ctx, h.id("dev")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Get(ctx, fsa); err == nil || a.EndDevice(h.id("dev")) != "" {
		t.Error("the device's resources outlive it")
	}
	if err := a.RemoveDevice(ctx, h.id("dev")); err != nil {
		t.Errorf("RemoveDevice of an unknown device: %v", err)
	}
}

func TestPutEndDeviceKeepsLinks(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	href, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	put := examples.EndDevice(0)
	put.SFDI = 7
	put.FunctionSetAssignmentsListLink = &ieee20305.FunctionSetAssignmentsListLink{}
	put.FunctionSetAssignmentsListLink.Href = "/elsewhere"
	if status, _, data := h.do(t, "dev", http.MethodPut, href, marshal(t, put)); status != http.StatusNoContent {
		t.Fatalf("PUT: %d %s", status, data)
	}
	_, _, data := h.do(t, "dev", http.MethodGet, href, nil)
	ed := decode[ieee20305.EndDevice](t, data)
	if ed.FunctionSetAssignmentsListLink.Href != href+"/fsa" || uint64(ed.SFDI) != uint64(h.id("dev").SFDI()) {
		t.Errorf("the PUT changed the server's links or the sFDI: %s", data)
	}
}

func TestInBandRegistration(t *testing.T) {
	h := newHarness(t, server.Config{InBandRegistration: true}, "dev", "other")
	ctx := context.Background()
	status, hdr, data := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev"))
	if status != http.StatusCreated || hdr.Get("Location") != "/edev/1" {
		t.Fatalf("POST: %d %q %s", status, hdr.Get("Location"), data)
	}
	// Posting again finds the same EndDevice.
	if _, hdr, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); hdr.Get("Location") != "/edev/1" {
		t.Errorf("POST again: %q", hdr.Get("Location"))
	}
	_, _, data = h.do(t, "dev", http.MethodGet, "/edev/1", nil)
	if ed := decode[ieee20305.EndDevice](t, data); ed.RegistrationLink != nil || ed.LogEventListLink == nil {
		t.Fatalf("in-band EndDevice: %s", data)
	}
	if err := h.srv.Admin().Register(ctx, h.id("dev"), 222220); err != nil {
		t.Fatal(err)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, "/edev/1", nil)
	if ed := decode[ieee20305.EndDevice](t, data); ed.RegistrationLink == nil {
		t.Error("no RegistrationLink after Register")
	}
	// A device posts the EndDevice of another, or one whose sFDI is not
	// that of its lFDI.
	if status, _, _ := h.do(t, "other", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusForbidden {
		t.Errorf("POST of another's EndDevice: %d", status)
	}
	bad := examples.EndDevice(0)
	l := h.id("other")
	lfdi := ieee20305.HexBinary160(l[:])
	bad.LFDI, bad.SFDI = &lfdi, 1
	if status, _, _ := h.do(t, "other", http.MethodPost, server.EndDeviceListHref, marshal(t, bad)); status != http.StatusBadRequest {
		t.Errorf("POST with a wrong sFDI: %d", status)
	}
	// A device that deletes its EndDevice is forgotten.
	if status, _, _ := h.do(t, "dev", http.MethodDelete, "/edev/1", nil); status != http.StatusNoContent {
		t.Fatalf("DELETE: %d", status)
	}
	if h.srv.Admin().EndDevice(h.id("dev")) != "" {
		t.Error("the device is remembered")
	}
}

func TestRegistrationNeedsProvisioning(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	if status, _, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusNotFound {
		t.Errorf("POST without an EndDeviceList: %d", status)
	}
	if err := h.srv.Admin().Root(context.Background(), "EndDeviceList", server.EndDeviceListHref, &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusForbidden {
		t.Errorf("POST of an unknown device: %d", status)
	}
	// A device the operator provisioned without an EndDevice record (an
	// operator) still has none.
	ctx := context.Background()
	if _, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), Edition: ieee20305.Edition2018}); err != nil {
		t.Fatal(err)
	}
	if _, hdr, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); hdr.Get("Location") != "/edev/1" {
		t.Errorf("POST of a provisioned device: %q", hdr.Get("Location"))
	}
}

func TestConcurrentRegistration(t *testing.T) {
	h := newHarness(t, server.Config{InBandRegistration: true}, "dev")
	body := h.own(t, "dev")
	var wg sync.WaitGroup
	locs := make([]string, 20)
	for i := range locs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, hdr, _ := h.do(t, "dev", http.MethodPost, server.EndDeviceListHref, body)
			locs[i] = hdr.Get("Location")
		}()
	}
	wg.Wait()
	for _, l := range locs {
		if l != locs[0] || l == "" {
			t.Fatalf("Locations %v", locs)
		}
	}
	_, _, data := h.do(t, "dev", http.MethodGet, "/edev?l=255", nil)
	if l := decode[ieee20305.EndDeviceList](t, data); l.All != 1 {
		t.Errorf("%d EndDevices", l.All)
	}
}

func TestAggregator(t *testing.T) {
	h := newHarness(t, server.Config{}, "agg", "agg2", "dev", "other")
	ctx := context.Background()
	a := h.srv.Admin()
	for _, n := range []string{"agg", "agg2"} {
		if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id(n), Role: server.RoleAggregator}); err != nil {
			t.Fatal(err)
		}
	}
	status, hdr, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev"))
	if status != http.StatusCreated {
		t.Fatalf("an aggregator registers a device: %d", status)
	}
	href := hdr.Get("Location")
	for _, who := range []string{"agg", "dev"} {
		if status, _, _ := h.do(t, who, http.MethodGet, href, nil); status != http.StatusOK {
			t.Errorf("%s GETs the device: %d", who, status)
		}
	}
	if status, _, _ := h.do(t, "agg", http.MethodPost, href+"/lel", marshal(t, examples.LogEvent(0))); status != http.StatusCreated {
		t.Errorf("the aggregator posts a LogEvent of the device: %d", status)
	}
	if status, _, data := h.do(t, "dev", http.MethodGet, href+"/lel/1", nil); status != http.StatusOK {
		t.Errorf("the device reads the LogEvent its aggregator posted: %d %s", status, data)
	}
	if status, _, _ := h.do(t, "agg2", http.MethodGet, href, nil); status != http.StatusNotFound {
		t.Errorf("another aggregator GETs the device: %d", status)
	}
	if status, _, _ := h.do(t, "agg2", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); status != http.StatusForbidden {
		t.Errorf("another aggregator registers the device: %d", status)
	}
	if _, hdr, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); hdr.Get("Location") != href {
		t.Errorf("the aggregator registers the device again: %q", hdr.Get("Location"))
	}
	// Without an lFDI an aggregator cannot say whose EndDevice it posts.
	ed := &ieee20305.EndDevice{}
	ed.SFDI = ieee20305.SFDIType(h.id("other").SFDI())
	if status, _, _ := h.do(t, "agg", http.MethodPost, server.EndDeviceListHref, marshal(t, ed)); status != http.StatusForbidden {
		t.Errorf("an aggregator posts an EndDevice without lFDI: %d", status)
	}
}

func TestOperator(t *testing.T) {
	h := newHarness(t, server.Config{}, "op", "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if href, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("op"), Role: server.RoleOperator}); err != nil || href != "" {
		t.Fatalf("ProvisionDevice of an operator = %q, %v", href, err)
	}
	href, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), Register: true, PIN: 5})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "op", http.MethodPut, href+"/rg", marshal(t, &ieee20305.Registration{PIN: 6})); status != http.StatusNoContent {
		t.Errorf("an operator PUTs a Registration: %d", status)
	}
	// An operator posts EndDevices with and without lFDI, and programs.
	if status, _, _ := h.do(t, "op", http.MethodPost, server.EndDeviceListHref, h.own(t, "op")); status != http.StatusCreated {
		t.Errorf("an operator posts an EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "op", http.MethodPost, server.EndDeviceListHref, marshal(t, examples.EndDevice(0))); status != http.StatusCreated {
		t.Errorf("an operator posts an EndDevice without lFDI: %d", status)
	}
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	status, hdr, _ := h.do(t, "op", http.MethodPost, "/derp", marshal(t, examples.DERProgram(0)))
	if status != http.StatusCreated {
		t.Fatalf("an operator posts a DERProgram: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, hdr.Get("Location"), nil); status != http.StatusOK {
		t.Errorf("a device reads the operator's DERProgram: %d", status)
	}
	if status, _, _ := h.do(t, "op", http.MethodDelete, href+"/rg", nil); status != http.StatusNoContent {
		t.Errorf("an operator DELETEs a Registration: %d", status)
	}
}

func TestDevicesAfterRestart(t *testing.T) {
	store := server.NewMemStore(nil)
	h := newHarness(t, server.Config{Store: store}, "dev")
	href, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t, server.Config{Store: store})
	h2.devices = h.devices
	if got := h2.srv.Admin().EndDevice(h.id("dev")); got != href {
		t.Errorf("after a restart the device's EndDevice is %q", got)
	}
	if _, hdr, _ := h2.do(t, "dev", http.MethodPost, server.EndDeviceListHref, h.own(t, "dev")); hdr.Get("Location") != href {
		t.Errorf("POST after a restart: %q", hdr.Get("Location"))
	}
}

func TestRole(t *testing.T) {
	for r, want := range map[server.Role]string{server.RoleDevice: "device", server.RoleAggregator: "aggregator", server.RoleOperator: "operator", 9: "Role(?)"} {
		if r.String() != want {
			t.Errorf("%d: %s", r, r)
		}
	}
}

// A list clients post to keeps its newest MaxListItems items.
func TestMaxListItems(t *testing.T) {
	h := newHarness(t, server.Config{MaxListItems: 3}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		ev := examples.LogEvent(0)
		ev.LogEventID = ieee20305.UInt16(i)
		if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/lel", marshal(t, ev)); status != http.StatusCreated {
			t.Fatalf("POST %d: %d", i, status)
		}
	}
	ms, err := h.srv.Admin().Members(context.Background(), ed+"/lel")
	if err != nil || len(ms) != 3 || ms[0].Value.(*ieee20305.LogEvent).LogEventID != 3 {
		t.Errorf("%d LogEvents, %v", len(ms), err)
	}
	// An operator's are not capped.
	for i := 0; i < 3; i++ {
		if _, err := h.srv.Admin().Create(context.Background(), ed+"/lel", examples.LogEvent(0), h.lfdi("dev")); err != nil {
			t.Fatal(err)
		}
	}
	if ms, _ := h.srv.Admin().Members(context.Background(), ed+"/lel"); len(ms) != 6 {
		t.Errorf("%d LogEvents after the operator's", len(ms))
	}
}
