// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

func decode[T any](t *testing.T, data []byte) *T {
	t.Helper()
	v := new(T)
	if err := ieee20305.Unmarshal(data, v); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return v
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	out, err := ieee20305.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// devices creates the EndDeviceList at /edev with an EndDevice of each SFDI,
// owned by owner.
func devices(t *testing.T, h *harness, owner string, sfdis ...uint64) {
	t.Helper()
	ctx := context.Background()
	if err := h.srv.Admin().Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	for _, sfdi := range sfdis {
		ed := &ieee20305.EndDevice{}
		ed.SFDI = ieee20305.SFDIType(sfdi)
		if _, err := h.srv.Admin().Create(ctx, "/edev", ed, owner); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListPaging(t *testing.T) {
	h := newHarness(t, server.Config{MaxLimit: 3}, "dev")
	devices(t, h, "", 50, 10, 40, 20, 30)
	for _, c := range []struct {
		query        string
		all, results uint32
		first        uint64
	}{
		{"", 5, 1, 10},
		{"?l=2", 5, 2, 10},
		{"?s=2&l=2", 5, 2, 30},
		{"?s=4&l=2", 5, 1, 50},
		{"?s=9", 5, 0, 0},
		{"?l=255", 5, 3, 10},
		{"?sFDI=40&l=5", 1, 1, 40},
	} {
		status, _, data := h.do(t, "dev", http.MethodGet, "/edev"+c.query, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: %d", c.query, status)
		}
		l := decode[ieee20305.EndDeviceList](t, data)
		if uint32(l.All) != c.all || uint32(l.Results) != c.results || len(l.EndDevice) != int(c.results) {
			t.Errorf("%s: all %d results %d items %d", c.query, l.All, l.Results, len(l.EndDevice))
		}
		if c.results > 0 && uint64(l.EndDevice[0].SFDI) != c.first {
			t.Errorf("%s: first sFDI %d, want %d (ordered by sFDI)", c.query, l.EndDevice[0].SFDI, c.first)
		}
		if l.Href == nil || *l.Href != "/edev" {
			t.Errorf("%s: href %v", c.query, l.Href)
		}
	}
}

func TestEventOrderAndAfter(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.NewList(ctx, "DERControlList", prog+"/derc", prog, "DERControlListLink", ""); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []struct{ start, created int64 }{{300, 1}, {100, 1}, {100, 5}, {200, 1}} {
		dc := examples.DERControl(0)
		dc.Interval.Start = ieee20305.TimeType(ev.start)
		dc.CreationTime = ieee20305.TimeType(ev.created)
		if _, err := a.Create(ctx, prog+"/derc", dc, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, data := h.do(t, "dev", http.MethodGet, prog+"/derc?l=10", nil)
	l := decode[ieee20305.DERControlList](t, data)
	var got []int64
	for _, dc := range l.DERControl {
		got = append(got, int64(dc.Interval.Start)*10+int64(dc.CreationTime))
	}
	if want := []int64{1005, 1001, 2001, 3001}; !equalInts(got, want) {
		t.Errorf("order (start*10+created) = %v, want %v", got, want)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, prog+"/derc?l=10&a=200", nil)
	if l := decode[ieee20305.DERControlList](t, data); l.All != 2 {
		t.Errorf("a=200: all %d, want 2", l.All)
	}
	_, _, data = h.do(t, "dev", http.MethodGet, prog, nil)
	if p := decode[ieee20305.DERProgram](t, data); p.DERControlListLink == nil || p.DERControlListLink.Href != prog+"/derc" {
		t.Errorf("NewList did not link the list: %+v", p.DERControlListLink)
	}
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLogEventOrder(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Put(ctx, &server.Node{Href: "/lel", Resource: "LogEventList", Value: &ieee20305.LogEventList{}}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{10, 30, 20} {
		le := examples.LogEvent(0)
		le.CreatedDateTime = ieee20305.TimeType(at)
		if _, err := a.Create(ctx, "/lel", le, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, data := h.do(t, "dev", http.MethodGet, "/lel?l=5", nil)
	l := decode[ieee20305.LogEventList](t, data)
	if len(l.LogEvent) != 3 || l.LogEvent[0].CreatedDateTime != 30 || l.LogEvent[2].CreatedDateTime != 10 {
		t.Errorf("LogEvents not most recent first: %+v", l.LogEvent)
	}
}

func TestAccess(t *testing.T) {
	h := newHarness(t, server.Config{}, "a", "b")
	devices(t, h, h.lfdi("a"), 1)
	if status, _, _ := h.do(t, "b", http.MethodGet, "/edev/1", nil); status != http.StatusNotFound {
		t.Errorf("another device's EndDevice: %d", status)
	}
	if _, _, data := h.do(t, "b", http.MethodGet, "/edev?l=5", nil); decode[ieee20305.EndDeviceList](t, data).All != 0 {
		t.Error("another device's EndDevice is listed")
	}
	ed := examples.EndDevice(0)
	if status, _, _ := h.do(t, "a", http.MethodPut, "/edev/1", marshal(t, ed)); status != http.StatusNoContent {
		t.Errorf("PUT of the own EndDevice: %d", status)
	}
	if status, _, _ := h.do(t, "a", http.MethodPut, "/edev", marshal(t, &ieee20305.EndDeviceList{})); status != http.StatusMethodNotAllowed {
		t.Errorf("PUT of the list: %d", status)
	}
	if status, _, _ := h.do(t, "a", http.MethodDelete, "/edev", nil); status != http.StatusMethodNotAllowed {
		t.Errorf("DELETE of the list: %d", status)
	}
	if status, _, _ := h.do(t, "a", http.MethodDelete, "/edev/1", nil); status != http.StatusNoContent {
		t.Errorf("DELETE of the own EndDevice: %d", status)
	}
	// A public resource cannot be changed by a device.
	ctx := context.Background()
	if _, err := h.srv.Admin().Create(ctx, "/edev", examples.EndDevice(0), ""); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "a", http.MethodPut, "/edev/2", marshal(t, examples.EndDevice(0))); status != http.StatusForbidden {
		t.Errorf("PUT of a public resource: %d", status)
	}
	if status, _, _ := h.do(t, "a", http.MethodDelete, "/edev/2", nil); status != http.StatusForbidden {
		t.Errorf("DELETE of a public resource: %d", status)
	}
	// A POST to a list of every client creates an item of the device; a
	// device cannot post to other lists of every client.
	if err := h.srv.Admin().Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.Admin().Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	mup := examples.MirrorUsagePoint(0)
	b := h.id("b")
	mup.DeviceLFDI = b[:]
	status, hdr, _ := h.do(t, "b", http.MethodPost, "/mup", marshal(t, mup))
	if status != http.StatusCreated || hdr.Get("Location") != "/mup/1" {
		t.Fatalf("POST: %d %q", status, hdr.Get("Location"))
	}
	if status, _, _ := h.do(t, "a", http.MethodGet, "/mup/1", nil); status != http.StatusNotFound {
		t.Errorf("a device reads another's MirrorUsagePoint: %d", status)
	}
	if status, _, _ := h.do(t, "b", http.MethodPost, "/derp", marshal(t, examples.DERProgram(0))); status != http.StatusForbidden {
		t.Errorf("a device posts a DERProgram: %d", status)
	}
}

func TestRequestErrors(t *testing.T) {
	h := newHarness(t, server.Config{MaxRequestBytes: 4096}, "dev")
	devices(t, h, "", 1)
	ed := marshal(t, examples.EndDevice(0))
	for _, c := range []struct {
		name, device, method, path string
		body                       []byte
		header                     []string
		status                     int
	}{
		{"no certificate", "", http.MethodGet, "/dcap", nil, nil, http.StatusForbidden},
		{"unknown path", "dev", http.MethodGet, "/nope", nil, nil, http.StatusNotFound},
		{"bad s", "dev", http.MethodGet, "/edev?s=x", nil, nil, http.StatusBadRequest},
		{"s twice", "dev", http.MethodGet, "/edev?s=1&s=2", nil, nil, http.StatusBadRequest},
		{"bad a", "dev", http.MethodGet, "/edev/1?a=x", nil, nil, http.StatusOK},
		{"EXI only", "dev", http.MethodGet, "/dcap", nil, []string{"Accept", "application/sep-exi"}, http.StatusNotAcceptable},
		{"XML accepted", "dev", http.MethodGet, "/dcap", nil, []string{"Accept", "application/sep-exi, application/sep+xml"}, http.StatusOK},
		{"PATCH", "dev", "PATCH", "/edev", nil, nil, http.StatusMethodNotAllowed},
		{"plain text", "dev", http.MethodPost, "/edev", ed, []string{"Content-Type", "text/plain"}, http.StatusUnsupportedMediaType},
		{"bad content type", "dev", http.MethodPost, "/edev", ed, []string{"Content-Type", ";;"}, http.StatusUnsupportedMediaType},
		{"too large", "dev", http.MethodPost, "/edev", bytes.Repeat([]byte(" "), 5000), nil, http.StatusRequestEntityTooLarge},
		{"not XML", "dev", http.MethodPost, "/edev", []byte("garbage"), nil, http.StatusBadRequest},
		{"wrong element", "dev", http.MethodPost, "/edev", marshal(t, &ieee20305.Time{}), nil, http.StatusBadRequest},
		{"invalid document", "dev", http.MethodPost, "/edev", invalidEndDevice(t), nil, http.StatusBadRequest},
		{"POST to a resource", "dev", http.MethodPost, "/edev/1", ed, nil, http.StatusMethodNotAllowed},
	} {
		status, hdr, data := h.do(t, c.device, c.method, c.path, c.body, c.header...)
		if status != c.status {
			t.Errorf("%s: %d, want %d: %s", c.name, status, c.status, data)
		}
		if status == http.StatusBadRequest && c.method != http.MethodGet {
			if hdr.Get("Content-Type") != server.MediaType {
				t.Errorf("%s: no Error document", c.name)
			} else {
				decode[ieee20305.Error](t, data)
			}
		}
	}
}

func invalidEndDevice(t *testing.T) []byte {
	ed := examples.InvalidEndDevice()
	out, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"urn:ieee:std:2030.5:ns EndDevice"`
		*ieee20305.EndDevice
	}{EndDevice: ed})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSlotAndClose(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	if err := h.srv.Admin().Put(ctx, &server.Node{Href: "/der/1/dg", Resource: "DERSettings", Owner: h.lfdi("dev")}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/der/1/dg", nil); status != http.StatusNotFound {
		t.Errorf("GET of a slot: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPut, "/der/1/dg", marshal(t, examples.DERSettings(0))); status != http.StatusCreated {
		t.Errorf("PUT into a slot: %d", status)
	}
	status, _, data := h.do(t, "dev", http.MethodGet, "/der/1/dg", nil)
	if status != http.StatusOK || *decode[ieee20305.DERSettings](t, data).Href != "/der/1/dg" {
		t.Errorf("GET after PUT: %d", status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodHead, "/der/1/dg", nil); status != http.StatusOK {
		t.Errorf("HEAD: %d", status)
	}
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, "/dcap", nil); status != http.StatusServiceUnavailable {
		t.Errorf("after Close: %d", status)
	}
}

func TestHooksAndConfig(t *testing.T) {
	boom := errors.New("boom")
	h := newHarness(t, server.Config{
		Hooks: map[string]server.Hooks{
			"EndDevice": {
				Get: func(context.Context, *server.Request) (any, error) {
					return nil, &server.HTTPError{Status: http.StatusTeapot}
				},
				Update: func(context.Context, *server.Request) error { return boom },
				Delete: func(_ context.Context, r *server.Request) error {
					if r.Principal.EndDevice != "" {
						t.Error("an unknown device has an EndDevice")
					}
					return nil
				},
			},
			"EndDeviceList": {Create: func(context.Context, *server.Request) (string, error) { return "", nil }},
		},
		Disable:           map[string][]string{"Time": {"GET"}},
		EnableDiscouraged: map[string][]string{"AccountBalance": {"PUT"}},
	}, "dev")
	devices(t, h, "", 1)
	if err := h.srv.Admin().Put(context.Background(), &server.Node{Href: "/ab", Resource: "AccountBalance", Value: examples.AccountBalance(0)}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path string
		body         []byte
		status       int
	}{
		{http.MethodGet, "/edev/1", nil, http.StatusTeapot},
		{http.MethodPut, "/edev/1", marshal(t, examples.EndDevice(0)), http.StatusInternalServerError},
		{http.MethodDelete, "/edev/1", nil, http.StatusNoContent},
		{http.MethodPost, "/edev", marshal(t, examples.EndDevice(0)), http.StatusNoContent},
		{http.MethodGet, "/tm", nil, http.StatusMethodNotAllowed},
		{http.MethodPut, "/dcap", marshal(t, &ieee20305.DeviceCapability{}), http.StatusMethodNotAllowed},
		{http.MethodPut, "/ab", marshal(t, examples.AccountBalance(0)), http.StatusForbidden},
	} {
		if status, _, data := h.do(t, "dev", c.method, c.path, c.body); status != c.status {
			t.Errorf("%s %s: %d, want %d: %s", c.method, c.path, status, c.status, data)
		}
	}
	if _, err := server.New(server.Config{Hooks: map[string]server.Hooks{"Nope": {}}}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("hooks of an unknown resource: %v", err)
	}
	if _, err := server.New(server.Config{Profile: &server.Profile{}}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("an empty profile: %v", err)
	}
	p := server.StandardProfile()
	p.Resources = []ieee20305.ResourceSpec{{ID: "EndDevice"}}
	if _, err := server.New(server.Config{Profile: p}); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("a profile resource that exists: %v", err)
	}
	if err := (&server.HTTPError{Status: 404}).Error(); err != "404 Not Found" {
		t.Errorf("HTTPError.Error = %q", err)
	}
}

func TestDeviceCapabilityAndTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, e := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		h := newHarness(t, server.Config{Clock: func() time.Time { return now }, PollRate: 60, DefaultEdition: e}, "dev")
		ctx := context.Background()
		if err := h.srv.Admin().Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
			t.Fatal(err)
		}
		if err := h.srv.Admin().Root(ctx, "SelfDevice", "/sdev", examples.SelfDevice(0)); err != nil {
			t.Fatal(err)
		}
		_, _, data := h.do(t, "dev", http.MethodGet, "/dcap", nil)
		dc := decode[ieee20305.DeviceCapability](t, data)
		if dc.PollRate == nil || *dc.PollRate != 60 || dc.EndDeviceListLink == nil || dc.EndDeviceListLink.Href != "/edev" || dc.TimeLink == nil || dc.SelfDeviceLink == nil {
			t.Errorf("%v: DeviceCapability %+v", e, dc)
		}
		_, _, data = h.do(t, "dev", http.MethodGet, "/tm", nil)
		tm := decode[ieee20305.Time](t, data)
		if int64(tm.CurrentTime) != now.Unix() || tm.Quality != 7 {
			t.Errorf("%v: Time %+v", e, tm)
		}
	}
	h := newHarness(t, server.Config{DefaultEdition: ieee20305.Edition2018}, "dev")
	ctx := context.Background()
	if err := h.srv.Admin().Root(ctx, "CurrentDERControls", "/cdc", &ieee20305.CurrentDERControls{}); err == nil {
		_, _, data := h.do(t, "dev", http.MethodGet, "/dcap", nil)
		if strings.Contains(string(data), "/cdc") {
			t.Error("a 2018 client sees a link to a 2023 resource")
		}
	}
}

func TestAdminErrors(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	a := h.srv.Admin()
	if err := a.Put(ctx, &server.Node{Href: "/x", Resource: "Nope"}); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Put of an unknown resource: %v", err)
	}
	if _, err := a.Create(ctx, "/nope", examples.EndDevice(0), ""); err == nil {
		t.Error("Create in a missing list")
	}
	if _, err := a.Create(ctx, "/tm", examples.EndDevice(0), ""); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("Create in a resource: %v", err)
	}
	if err := a.NewList(ctx, "Nope", "/x", "/dcap", "", ""); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("NewList of an unknown resource: %v", err)
	}
	if err := a.NewList(ctx, "EndDeviceList", "/edev", "/dcap", "NoSuchLink", ""); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("NewList with an unknown link: %v", err)
	}
	if err := a.NewList(ctx, "EndDeviceList", "/edev2", "/missing", "EndDeviceListLink", ""); err == nil {
		t.Error("NewList under a missing parent")
	}
	if err := a.Root(ctx, "EndDeviceList", "/edev3", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "Nope", "/x", nil); err == nil {
		t.Error("Root of an unknown resource")
	}
	if err := a.Delete(ctx, "/edev3"); err != nil {
		t.Error(err)
	}
	if _, err := a.Get(ctx, "/edev3"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("Get after Delete: %v", err)
	}
	if h.srv.Store() == nil {
		t.Error("no store")
	}
}

// Hrefs continue after the largest number a persistent store holds.
func TestHrefsAfterRestart(t *testing.T) {
	store := server.NewMemStore(nil)
	ctx := context.Background()
	srv, err := server.New(server.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Admin().Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	for _, href := range []string{"/edev/7", "/edev/x", "/edev/2/sub"} {
		if err := srv.Admin().Put(ctx, &server.Node{Href: href, Resource: "EndDevice", Value: examples.EndDevice(0)}); err != nil {
			t.Fatal(err)
		}
	}
	again, err := server.New(server.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	href, err := again.Admin().Create(ctx, "/edev", examples.EndDevice(0), "")
	if err != nil || href != "/edev/8" {
		t.Errorf("Create after a restart = %q, %v", href, err)
	}
}

func TestPeerCertificateErrorsAndLogging(t *testing.T) {
	var log bytes.Buffer
	srv, err := server.New(server.Config{
		PeerCertificates: func(*http.Request) ([]*x509.Certificate, error) { return nil, errors.New("no handshake") },
		Logger:           ieee20305.NewSlogLogger(slog.NewTextHandler(&log, nil)),
		Hooks: map[string]server.Hooks{"Time": {Get: func(context.Context, *server.Request) (any, error) {
			return nil, &server.HTTPError{Status: http.StatusBadGateway, Err: errors.New("upstream")}
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tm", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("a failed peer certificate lookup: %d", w.Code)
	}
	he := &server.HTTPError{Status: 502, Err: errors.New("upstream")}
	if !errors.Is(he, he.Err) || !strings.Contains(he.Error(), "upstream") {
		t.Error("HTTPError wraps its error")
	}
	cert := testDevice(t, "dev")
	srv2, err := server.New(server.Config{
		PeerCertificates: func(*http.Request) ([]*x509.Certificate, error) { return []*x509.Certificate{cert}, nil },
		Logger:           ieee20305.NewSlogLogger(slog.NewTextHandler(&log, nil)),
		Hooks: map[string]server.Hooks{"Time": {Get: func(context.Context, *server.Request) (any, error) {
			return nil, &server.HTTPError{Status: http.StatusBadGateway, Err: errors.New("upstream")}
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	srv2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tm", nil))
	if w.Code != http.StatusBadGateway || !strings.Contains(log.String(), "upstream") {
		t.Errorf("a 5xx hook error: %d, log %q", w.Code, log.String())
	}
}

// A hook's error answers with the status of its kind: an HTTPError's own,
// or that of the sentinel of package ieee20305 it matches; 500 otherwise.
func TestHookErrors(t *testing.T) {
	var hookErr error
	h := newHarness(t, server.Config{Hooks: map[string]server.Hooks{
		"Time": {Get: func(context.Context, *server.Request) (any, error) { return nil, hookErr }},
	}}, "dev")
	for _, c := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("%w: gone", ieee20305.ErrNotFound), http.StatusNotFound},
		{server.ErrNoNode, http.StatusNotFound},
		{ieee20305.ErrForbidden, http.StatusForbidden},
		{ieee20305.ErrUnauthorized, http.StatusUnauthorized},
		{&ieee20305.ValidationError{Field: "x", Reason: "y"}, http.StatusBadRequest},
		{ieee20305.ErrConflict, http.StatusConflict},
		{ieee20305.ErrUnavailable, http.StatusServiceUnavailable},
		{&server.HTTPError{Err: errors.New("no status")}, http.StatusInternalServerError},
		{&server.HTTPError{Status: http.StatusTeapot}, http.StatusTeapot},
		{errors.New("plain"), http.StatusInternalServerError},
	} {
		hookErr = c.err
		if status, _, _ := h.do(t, "dev", http.MethodGet, "/tm", nil); status != c.status {
			t.Errorf("%v: %d, want %d", c.err, status, c.status)
		}
	}
	he := &server.HTTPError{Status: http.StatusNotFound}
	if !errors.Is(he, ieee20305.ErrNotFound) || errors.Is(he, ieee20305.ErrServer) ||
		!errors.Is(&server.HTTPError{Status: 502}, ieee20305.ErrServer) {
		t.Error("HTTPError does not match the sentinels of its status")
	}
	// Changes after Close fail with ErrClosed.
	_ = h.srv.Close()
	if err := h.srv.Admin().Root(context.Background(), "DERProgramList", "/derp", &ieee20305.DERProgramList{}); !errors.Is(err, ieee20305.ErrClosed) {
		t.Errorf("a change after Close: %v", err)
	}
}
