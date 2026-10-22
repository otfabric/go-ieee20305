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
	if status, _ := do(http.MethodPut, cp, &csipaus.ConnectionPointType{ConnectionPointId: "NMI1"}); status != http.StatusCreated {
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
