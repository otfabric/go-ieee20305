// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// A document has its href; a ListLink, the number of items the client
// reads; and a link to a resource of the server that does not exist yet is
// left out, while one the client creates with PUT stays.
func TestLinksOfResponses(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev", "other")
	ctx := context.Background()
	a := h.srv.Admin()
	ed, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{}, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Create(ctx, ed+"/lel", examples.LogEvent(0), h.lfdi("dev")); err != nil {
		t.Fatal(err)
	}

	_, _, data := h.do(t, "dev", http.MethodGet, "/dcap", nil)
	if dc := decode[ieee20305.DeviceCapability](t, data); dc.GetHref() != "/dcap" || dc.EndDeviceListLink.All == nil || *dc.EndDeviceListLink.All != 1 {
		t.Errorf("DeviceCapability %s", data)
	}
	// Another device reads no EndDevice.
	_, _, data = h.do(t, "other", http.MethodGet, "/dcap", nil)
	if dc := decode[ieee20305.DeviceCapability](t, data); *dc.EndDeviceListLink.All != 0 {
		t.Errorf("DeviceCapability of another device %s", data)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, ed, nil)
	e := decode[ieee20305.EndDevice](t, data)
	if e.LogEventListLink.All == nil || *e.LogEventListLink.All != 1 || e.DeviceInformationLink == nil {
		t.Errorf("EndDevice %s", data)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, prog, nil)
	p := decode[ieee20305.DERProgram](t, data)
	if p.DefaultDERControlLink != nil {
		t.Error("a link to a DefaultDERControl there is not")
	}
	if p.DERControlListLink == nil || *p.DERControlListLink.All != 3 || p.ActiveDERControlListLink == nil || *p.ActiveDERControlListLink.All != 0 {
		t.Errorf("DERProgram %s", data)
	}
	if err := a.Put(ctx, &server.Node{Href: prog + "/dderc", Resource: "DefaultDERControl", Value: examples.DefaultDERControl(0)}); err != nil {
		t.Fatal(err)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, prog, nil)
	if p := decode[ieee20305.DERProgram](t, data); p.DefaultDERControlLink == nil {
		t.Error("no link to the DefaultDERControl")
	}
	// The items of a list have their hrefs and links too.
	_, _, data = h.do(t, "dev", http.MethodGet, "/derp?l=5", nil)
	if l := decode[ieee20305.DERProgramList](t, data); len(l.DERProgram) != 1 || l.DERProgram[0].DERControlListLink.All == nil {
		t.Errorf("DERProgramList %s", data)
	}
}
