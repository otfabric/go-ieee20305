// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// The store of a server reports the errors of the store it wraps, and
// refuses changes after Close.
func TestStoreOfServer(t *testing.T) {
	h := newHarness(t, server.Config{})
	ctx := context.Background()
	st := h.srv.Store()
	if err := st.Put(ctx, &server.Node{}); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Put without an href: %v", err)
	}
	if err := st.Delete(ctx, "/nope"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("Delete: %v", err)
	}
	if err := st.AddMember(ctx, "/nope", "/tm"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("AddMember: %v", err)
	}
	if err := h.srv.Admin().Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	href, err := h.srv.Admin().Create(ctx, "/edev", examples.EndDevice(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveMember(ctx, "/edev", href); err != nil {
		t.Errorf("RemoveMember: %v", err)
	}
	if err := st.RemoveMember(ctx, "/edev", href); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("RemoveMember again: %v", err)
	}
	_ = h.srv.Close()
	for name, err := range map[string]error{
		"Delete":       st.Delete(ctx, href),
		"AddMember":    st.AddMember(ctx, "/edev", href),
		"RemoveMember": st.RemoveMember(ctx, "/edev", href),
	} {
		if !errors.Is(err, ieee20305.ErrClosed) {
			t.Errorf("%s after Close: %v", name, err)
		}
	}
}

// noDocument is a value a hook returns that no codec marshals.
type noDocument struct{ C chan int }

// What the store holds that the server cannot serve answers 500; a request
// it cannot read answers 400.
func TestServeErrors(t *testing.T) {
	var countErr error
	var countDoc any
	h := newHarness(t, server.Config{Hooks: map[string]server.Hooks{
		"Time": {Get: func(context.Context, *server.Request) (any, error) { return &noDocument{}, nil }},
		"DERControlList": {Get: func(context.Context, *server.Request) (any, error) {
			return countDoc, countErr
		}},
	}}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	devices(t, h, h.lfdi("dev"), 1)
	if err := h.srv.Store().Put(ctx, &server.Node{Href: "/odd", Resource: "Nope", Value: &ieee20305.Time{}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, &server.Node{Href: "/notalist", Resource: "DERProgramList", Value: &ieee20305.DERProgram{}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, &server.Node{Href: "/actderc", Resource: "ActiveDERControlList", Value: &ieee20305.Time{}}); err != nil {
		t.Fatal(err)
	}
	// An item the list document cannot hold.
	if err := a.Put(ctx, &server.Node{Href: "/edev/x", Resource: "Time", Value: &ieee20305.Time{}}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddMember(ctx, "/edev", "/edev/x"); err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, method, path string
		body               []byte
		header             []string
		status             int
	}{
		{"an unknown resource", http.MethodGet, "/odd", nil, nil, http.StatusInternalServerError},
		{"POST to what is no list", http.MethodPost, "/notalist", marshal(t, examples.DERProgram(0)), nil, http.StatusMethodNotAllowed},
		{"an active list that is no list", http.MethodGet, "/actderc", nil, nil, http.StatusInternalServerError},
		{"a list of a foreign item", http.MethodGet, "/edev?l=5", nil, nil, http.StatusInternalServerError},
		{"a bad Accept part", http.MethodGet, "/dcap", nil, []string{"Accept", ";;, application/sep+xml"}, http.StatusOK},
		{"DELETE of a resource of every client", http.MethodDelete, prog, nil, nil, http.StatusForbidden},
	} {
		if status, _, data := h.do(t, "dev", c.method, c.path, c.body, c.header...); status != c.status {
			t.Errorf("%s: %d, want %d: %s", c.name, status, c.status, data)
		}
	}
	// A document no codec marshals fails the GET (with a 400, see
	// TestUnmarshalableDocument).
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/tm", nil); status < 400 {
		t.Errorf("a document no codec marshals: %d", status)
	}
	// The count of a list with a Get hook: none when it fails or answers no
	// list.
	for _, c := range []struct {
		doc any
		err error
	}{{nil, errors.New("boom")}, {&ieee20305.Time{}, nil}} {
		countDoc, countErr = c.doc, c.err
		status, _, data := h.do(t, "dev", http.MethodGet, prog, nil)
		if p := decode[ieee20305.DERProgram](t, data); status != http.StatusOK || p.DERControlListLink == nil || p.DERControlListLink.All == nil || *p.DERControlListLink.All != 0 {
			t.Errorf("count of a hooked list (%v): %d %+v", c.err, status, p.DERControlListLink)
		}
	}
	// A body that cannot be read.
	req := httptest.NewRequest(http.MethodPut, "/edev/1", iotest.ErrReader(errors.New("broken")))
	req.Header.Set("X-Test-Device", "dev")
	req.Header.Set("Content-Type", server.MediaType)
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a broken body: %d", w.Code)
	}
}

// A response the server cannot marshal is the server's failure: 500. It
// answers 400 with an Error document: respond (handler.go:366) returns the
// error of the codec, which matches ieee20305.ErrInvalid, and asHTTPError
// maps ErrInvalid to 400, as for a client's invalid document.
func TestUnmarshalableDocument(t *testing.T) {
	h := newHarness(t, server.Config{Hooks: map[string]server.Hooks{
		"Time": {Get: func(context.Context, *server.Request) (any, error) { return &noDocument{}, nil }},
	}}, "dev")
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/tm", nil); status != http.StatusInternalServerError {
		t.Errorf("a document no codec marshals: %d, want 500", status)
	}
}

// DeviceCapability and Time stored as other documents are served as they
// are, and a new Time.
func TestVirtualResourcesOfOtherDocuments(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	if err := h.srv.Admin().Put(ctx, &server.Node{Href: "/dcap", Resource: "DeviceCapability", Value: &ieee20305.Time{}}); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.Admin().Put(ctx, &server.Node{Href: "/tm", Resource: "Time", Value: &ieee20305.DeviceCapability{}}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/dcap", nil); status != http.StatusOK {
		t.Errorf("DeviceCapability of a Time: %d", status)
	}
	if status, _, data := h.do(t, "dev", http.MethodGet, "/tm", nil); status != http.StatusOK || decode[ieee20305.Time](t, data).Quality != 7 {
		t.Errorf("Time of a DeviceCapability: %d", status)
	}
}

// An aggregator reads and registers the devices it manages; a device
// posts to the lists the configuration opens to clients.
func TestManagedDevicesAndClientLists(t *testing.T) {
	h := newHarness(t, server.Config{ClientLists: map[string]bool{"LogEventList": true}, MaxListItems: -1}, "agg", "dev", "other", "op")
	ctx := context.Background()
	a := h.srv.Admin()
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("agg"), Role: server.RoleAggregator}); err != nil {
		t.Fatal(err)
	}
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), ManagedBy: h.id("agg")})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "agg", http.MethodGet, ed, nil); status != http.StatusOK {
		t.Errorf("an aggregator reads a managed EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "other", http.MethodGet, ed, nil); status != http.StatusNotFound {
		t.Errorf("another device reads the EndDevice: %d", status)
	}
	// A device the operator gave a manager, without an EndDevice: its
	// aggregator registers it.
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("other"), Role: server.RoleOperator, ManagedBy: h.id("agg")}); err != nil {
		t.Fatal(err)
	}
	o := examples.EndDevice(0)
	otherID := h.id("other")
	lfdi := ieee20305.HexBinary160(otherID[:])
	o.LFDI, o.SFDI = &lfdi, ieee20305.SFDIType(otherID.SFDI())
	status, hdr, _ := h.do(t, "agg", http.MethodPost, "/edev", marshal(t, o))
	if status != http.StatusCreated || a.EndDevice(h.id("other")) != hdr.Get("Location") {
		t.Errorf("an aggregator registers a device of the operator: %d %q", status, hdr.Get("Location"))
	}
	// An EndDevice posted to another EndDeviceList is an item of it.
	if err := a.Root(ctx, "EndDeviceList", "/edev2", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, "/edev2", marshal(t, examples.EndDevice(0))); status != http.StatusCreated {
		t.Errorf("POST to another EndDeviceList: %d", status)
	}
	// A list of every client opened to clients: the item is the device's.
	if err := a.Put(ctx, &server.Node{Href: "/lel", Resource: "LogEventList", Value: &ieee20305.LogEventList{}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if status, _, _ := h.do(t, "dev", http.MethodPost, "/lel", marshal(t, examples.LogEvent(0))); status != http.StatusCreated {
			t.Errorf("POST to a client list: %d", status)
		}
	}
	if n, err := a.Get(ctx, "/lel/1"); err != nil || n.Owner != h.lfdi("dev") {
		t.Errorf("the LogEvent of a client list: %v", err)
	}
	if ms, _ := a.Members(ctx, "/lel"); len(ms) != 2 {
		t.Errorf("MaxListItems -1 trimmed the list to %d", len(ms))
	}
	// A Registration is the operator's to change.
	if err := a.Register(ctx, h.id("dev"), 1234); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodDelete, ed+"/rg", nil); status != http.StatusForbidden && status != http.StatusMethodNotAllowed {
		t.Errorf("a device deletes its Registration: %d", status)
	}
	// A Register of an EndDevice that is not one fails.
	if err := a.Put(ctx, &server.Node{Href: ed, Resource: "EndDevice", Value: &ieee20305.Time{}, Owner: h.lfdi("dev")}); err != nil {
		t.Fatal(err)
	}
	if err := a.Register(ctx, h.id("dev"), 1); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Register of a Time: %v", err)
	}
}

// Mirrors refuse what is not the client's to mirror.
func TestMirrorRefusals(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}); err != nil {
		t.Fatal(err)
	}
	mup := examples.MirrorUsagePoint(0)
	mup.DeviceLFDI = nil
	if status, _, _ := h.do(t, "dev", http.MethodPost, "/mup", marshal(t, mup)); status != http.StatusBadRequest {
		t.Errorf("a mirror without a deviceLFDI: %d", status)
	}
	// A mirror of every client: readings are the operator's to post.
	public, err := a.Create(ctx, "/mup", examples.MirrorUsagePoint(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, public, marshal(t, examples.MirrorMeterReading(0))); status != http.StatusForbidden {
		t.Errorf("readings of a mirror of every client: %d", status)
	}
}

// A profile whose EndDevice is no EndDevice, or whose parts are no links,
// cannot make EndDevices; parts that are no resources are left out.
func TestProfileEndDevice(t *testing.T) {
	ctx := context.Background()
	p := server.StandardProfile()
	newElement := p.NewElement
	p.NewElement = func(n xml.Name) any {
		if n.Local == "EndDevice" {
			return &ieee20305.Time{}
		}
		return newElement(n)
	}
	h := newHarness(t, server.Config{Profile: p, InBandRegistration: true}, "dev")
	if _, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("ProvisionDevice with an EndDevice of a Time: %v", err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, "/edev", h.own(t, "dev")); status != http.StatusInternalServerError {
		t.Errorf("in-band registration with an EndDevice of a Time: %d", status)
	}
	p = server.StandardProfile()
	p.Parts = map[string][]string{"EndDevice": {"Nope"}}
	h = newHarness(t, server.Config{Profile: p}, "dev")
	if _, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")}); err != nil {
		t.Errorf("a part that is no resource: %v", err)
	}
	p.Parts = map[string][]string{"EndDevice": {"Time"}}
	h = newHarness(t, server.Config{Profile: p}, "dev")
	if _, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")}); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("a part the EndDevice has no link to: %v", err)
	}
}

// After a restart, an EndDevice whose owner is no LFDI is no device.
func TestLoadDevicesOfBadOwner(t *testing.T) {
	store := server.NewMemStore(nil)
	if err := store.Put(context.Background(), &server.Node{Href: "/edev/9", Resource: "EndDevice", Owner: "not hex", Value: &ieee20305.EndDevice{}}); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	href, err := srv.Admin().Create(context.Background(), "/edev/9/x", examples.EndDevice(0), "")
	if err == nil || href != "" {
		t.Errorf("Create in what is no list: %q", href)
	}
}

// CancelEvent with randomization sets the status of a randomized
// cancellation.
func TestCancelEventRandomized(t *testing.T) {
	h := newHarness(t, server.Config{})
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelEvent(ctx, ev, true); err != nil {
		t.Fatal(err)
	}
	n, err := a.Get(ctx, ev)
	if err != nil || n.Value.(*ieee20305.DERControl).EventStatus.CurrentStatus != 3 {
		t.Errorf("a randomized cancellation: %v", err)
	}
	if err := a.CancelEvent(ctx, prog, false); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("CancelEvent of a program: %v", err)
	}
}

func TestNewNotificationClient(t *testing.T) {
	c := server.NewNotificationClient(tls.Certificate{}, x509.NewCertPool())
	if c == nil || c.Timeout == 0 || c.Transport == nil {
		t.Errorf("NewNotificationClient: %+v", c)
	}
}
