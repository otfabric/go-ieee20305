// SPDX-License-Identifier: MIT

package csipserver_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/csipaus/csipserver"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

func TestConnectionPoint(t *testing.T) {
	ca, key, err := security.TestCA("csip")
	if err != nil {
		t.Fatal(err)
	}
	cert, _, err := security.TestDeviceCert(ca, key, "inverter")
	if err != nil {
		t.Fatal(err)
	}
	cfg := csipserver.Configure(server.Config{PeerCertificates: func(*http.Request) ([]*x509.Certificate, error) {
		return []*x509.Certificate{cert}, nil
	}})
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx := context.Background()
	href, err := srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: security.CertificateLFDI(cert)})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, v any) (int, []byte) {
		t.Helper()
		var body io.Reader
		if v != nil {
			out, err := csipaus.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(out)
		}
		req, _ := http.NewRequest(method, ts.URL+path, body)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, data
	}
	_, data := do(http.MethodGet, href, nil)
	var ed csipaus.EndDevice
	if err := csipaus.Unmarshal(data, &ed); err != nil || ed.ConnectionPointLink == nil {
		t.Fatalf("EndDevice %s: %v", data, err)
	}
	if bytes.Contains(data, []byte("schemaVer")) {
		t.Error("a CSIP-AUS document with the schemaVer of 2023")
	}
	cp := ed.ConnectionPointLink.Href
	if status, _ := do(http.MethodPut, cp, &csipaus.ConnectionPointType{ConnectionPointId: "NMI1"}); status != http.StatusNoContent {
		t.Errorf("PUT: %d", status)
	}
	status, data := do(http.MethodGet, cp, nil)
	var got csipaus.ConnectionPointType
	if err := csipaus.Unmarshal(data, &got); status != http.StatusOK || err != nil || got.ConnectionPointId != "NMI1" {
		t.Errorf("GET: %d %s", status, data)
	}
	if status, _ := do(http.MethodPost, cp, &csipaus.ConnectionPointType{}); status != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", status)
	}
	if status, _ := do(http.MethodPut, cp, &ieee20305.Time{}); status != http.StatusBadRequest {
		t.Errorf("PUT of a Time: %d", status)
	}
	if status, _ := do(http.MethodDelete, cp, nil); status != http.StatusNoContent {
		t.Errorf("DELETE: %d", status)
	}
	p := csipserver.Profile()
	if p.NewElement(xml.Name{Space: csipaus.Namespace, Local: "ConnectionPoint"}) == nil || p.NewElement(xml.Name{Space: ieee20305.Namespace, Local: "Time"}) == nil {
		t.Error("NewElement")
	}
	if p.Clone(&ieee20305.Time{}) == nil || p.Clone(&csipaus.ConnectionPointType{}) == nil {
		t.Error("Clone")
	}
	if _, ok := p.ElementName(&ieee20305.Time{}); !ok {
		t.Error("ElementName")
	}
}

func TestEnvelope(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	c := csipserver.Envelope(ieee20305.MRIDType{1}, start, time.Hour, csipaus.Limits{Import: csipserver.Watts(-200)})
	if int64(c.Interval.Start) != start.Unix() || c.Interval.Duration != 3600 || c.DERControlBase.OpModImpLimW.Value != -200 {
		t.Errorf("Envelope %+v", c)
	}
	if l, ok := csipaus.LimitsOf(&c.DERControlBase); !ok || l.Import == nil {
		t.Error("LimitsOf a DERControlBase")
	}
	if _, ok := csipaus.LimitsOf((*csipaus.DERControlBase)(nil)); ok {
		t.Error("LimitsOf nil")
	}
}

// The default limits of a program: on one without a DefaultDERControl, and
// on one whose default has other controls, which stay.
func TestSetDefaultLimits(t *testing.T) {
	srv, err := server.New(csipserver.Configure(server.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ctx := context.Background()
	a := srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", a.Document("DERProgramList")); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{Primacy: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := csipserver.SetDefaultLimits(ctx, a, prog, csipaus.Limits{Export: csipserver.Watts(5000)}); err != nil {
		t.Fatal(err)
	}
	n, err := a.Get(ctx, prog+"/dderc")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := n.Value.(*csipaus.DefaultDERControl)
	if !ok || d.DERControlBase.OpModExpLimW.Value != 5000 || d.UpdatedTime == nil {
		t.Fatalf("the default: %#v", n.Value)
	}

	// Change it: the limits are replaced, the other controls kept.
	on := true
	d.DERControlBase.OpModConnect = &on
	if err := a.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := csipserver.SetDefaultLimits(ctx, a, prog, csipaus.Limits{Import: csipserver.Watts(3000)}); err != nil {
		t.Fatal(err)
	}
	n, _ = a.Get(ctx, prog+"/dderc")
	d = n.Value.(*csipaus.DefaultDERControl)
	if d.DERControlBase.OpModImpLimW.Value != 3000 || d.DERControlBase.OpModExpLimW != nil ||
		d.DERControlBase.OpModConnect == nil || !*d.DERControlBase.OpModConnect {
		t.Errorf("the default after a change: %+v", d.DERControlBase)
	}

	if err := csipserver.SetDefaultLimits(ctx, a, "/derp/9", csipaus.Limits{}); err == nil {
		t.Error("SetDefaultLimits of a program the server does not have")
	}
}

// The profile serves the documents of the base standard that CSIP-AUS does
// not change with the types of the base package.
func TestProfile(t *testing.T) {
	p := csipserver.Profile()
	timeName := xml.Name{Space: "urn:ieee:std:2030.5:ns", Local: "Time"}
	if _, ok := p.NewElement(timeName).(*ieee20305.Time); !ok {
		t.Error("NewElement of Time")
	}
	if _, ok := p.NewElement(xml.Name{Space: "urn:ieee:std:2030.5:ns", Local: "EndDevice"}).(*csipaus.EndDevice); !ok {
		t.Error("NewElement of EndDevice")
	}
	tm := &ieee20305.Time{CurrentTime: 5}
	if c, ok := p.Clone(tm).(*ieee20305.Time); !ok || c == tm || c.CurrentTime != 5 {
		t.Errorf("Clone of a Time: %v", c)
	}
	ed := &csipaus.EndDevice{}
	if c, ok := p.Clone(ed).(*csipaus.EndDevice); !ok || c == ed {
		t.Error("Clone of an EndDevice")
	}
	if c := p.Clone("nothing"); c != nil {
		t.Errorf("Clone of a string: %v", c)
	}
	if n, ok := p.ElementName(tm); !ok || n != timeName {
		t.Errorf("ElementName of a Time: %v", n)
	}
	if n, ok := p.ElementName(ed); !ok || n.Local != "EndDevice" {
		t.Errorf("ElementName of an EndDevice: %v", n)
	}
	if _, ok := p.ElementName("nothing"); ok {
		t.Error("ElementName of a string")
	}
	if !p.Derives("DERControlResponse", "Response") || p.Derives("Time", "EndDevice") {
		t.Error("Derives")
	}
	if _, ok := p.Codec(ieee20305.Edition2023).(csipaus.Codec); !ok {
		t.Error("Codec")
	}
}
