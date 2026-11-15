// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

// The query parameters at and beyond their limits: out of range is 400, a
// huge l is capped, a huge s is an empty page.
func TestHostileQueryParameters(t *testing.T) {
	h := newHarness(t, server.Config{MaxLimit: 2}, "dev")
	devices(t, h, "", 1, 2, 3)
	for _, c := range []struct {
		query   string
		status  int
		results int
		all     uint32
	}{
		{"?s=-1", http.StatusBadRequest, 0, 3},
		{"?s=4294967296", http.StatusBadRequest, 0, 3},
		{"?s=99999999999999999999999", http.StatusBadRequest, 0, 3},
		{"?l=-1", http.StatusBadRequest, 0, 3},
		{"?l=4294967296", http.StatusBadRequest, 0, 3},
		{"?l=4294967295", http.StatusOK, 2, 3},
		{"?s=4294967295&l=4294967295", http.StatusOK, 0, 3},
		{"?s=2147483647", http.StatusOK, 0, 3},
		{"?s=2147483648", http.StatusOK, 0, 3},
		{"?s=2147483648&l=2", http.StatusOK, 0, 3},
		{"?s=1&l=2147483648", http.StatusOK, 2, 3},
		{"?s=2&l=4294967295", http.StatusOK, 1, 3},
		{"?l=0", http.StatusOK, 0, 3},
		{"?s=1&s=2", http.StatusBadRequest, 0, 3},
		{"?s=+1", http.StatusBadRequest, 0, 3},
		{"?s=0x1", http.StatusBadRequest, 0, 3},
		{"?s=%00", http.StatusBadRequest, 0, 3},
		{"?sFDI=18446744073709551616", http.StatusBadRequest, 0, 3},
		{"?sFDI=18446744073709551615&l=5", http.StatusOK, 0, 0},
		{"?" + strings.Repeat("x=1&", 5000) + "l=2", http.StatusOK, 2, 3},
	} {
		status, _, data := h.do(t, "dev", http.MethodGet, "/edev"+c.query, nil)
		if status != c.status {
			t.Errorf("%.40s: %d, want %d", c.query, status, c.status)
			continue
		}
		if status == http.StatusOK {
			l := decode[ieee20305.EndDeviceList](t, data)
			if len(l.EndDevice) != c.results || uint32(l.All) != c.all {
				t.Errorf("%.40s: %d items of %d, want %d of %d", c.query, len(l.EndDevice), l.All, c.results, c.all)
			}
		}
	}
}

// With MaxLimit at the end of the wire's range, s and l together pass the
// largest int of a 32-bit platform: the page is still the items that
// exist.
func TestHostileQueryParametersWithoutCap(t *testing.T) {
	h := newHarness(t, server.Config{MaxLimit: 4294967295}, "dev")
	devices(t, h, "", 1, 2, 3)
	for _, c := range []struct {
		query   string
		results int
	}{
		{"?l=4294967295", 3},
		{"?s=1&l=2147483648", 2},
		{"?s=2&l=2147483647", 1},
		{"?s=2147483647&l=2147483647", 0},
		{"?s=2147483648&l=2147483648", 0},
		{"?s=4294967295&l=4294967295", 0},
	} {
		status, _, data := h.do(t, "dev", http.MethodGet, "/edev"+c.query, nil)
		if status != http.StatusOK {
			t.Errorf("%s: %d, want 200", c.query, status)
			continue
		}
		if l := decode[ieee20305.EndDeviceList](t, data); len(l.EndDevice) != c.results || uint32(l.All) != 3 {
			t.Errorf("%s: %d items of %d, want %d of 3", c.query, len(l.EndDevice), l.All, c.results)
		}
	}
}

// The a parameter of an event list at the ends of int64.
func TestHostileAfterParameter(t *testing.T) {
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
	if _, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), ""); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{
		"?a=-9223372036854775808&l=5": http.StatusOK,
		"?a=9223372036854775807&l=5":  http.StatusOK,
		"?a=9223372036854775808":      http.StatusBadRequest,
		"?a=1e3":                      http.StatusBadRequest,
	} {
		if status, _, _ := h.do(t, "dev", http.MethodGet, prog+"/derc"+q, nil); status != want {
			t.Errorf("%s: %d, want %d", q, status, want)
		}
	}
}

// Bodies that are too large, malformed or deeply nested are refused, and
// the server goes on serving.
func TestHostileBodies(t *testing.T) {
	h := newHarness(t, server.Config{MaxRequestBytes: 64 << 10, NotificationClient: &http.Client{}, NotificationURIAllowed: allowAll}, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	ns := `<Subscription xmlns="urn:ieee:std:2030.5:ns">`
	for name, c := range map[string]struct {
		body   string
		status []int
	}{
		"oversized":   {ns + strings.Repeat(" ", 128<<10) + "</Subscription>", []int{http.StatusRequestEntityTooLarge}},
		"truncated":   {ns + "<notificationURI>https://x", []int{http.StatusBadRequest}},
		"not xml":     {"\x00\xff\xfe", []int{http.StatusBadRequest}},
		"empty":       {"", []int{http.StatusBadRequest}},
		"wrong root":  {`<EndDevice xmlns="urn:ieee:std:2030.5:ns"/>`, []int{http.StatusBadRequest}},
		"nested":      {ns + strings.Repeat("<x>", 5000) + strings.Repeat("</x>", 5000) + "</Subscription>", []int{http.StatusBadRequest}},
		"nested deep": {strings.Repeat(ns, 15000), []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge}},
		"entity":      {`<!DOCTYPE s [<!ENTITY a "aaaaaaaaaa">]>` + ns + "<notificationURI>&a;</notificationURI></Subscription>", []int{http.StatusBadRequest}},
	} {
		status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", []byte(c.body))
		ok := false
		for _, s := range c.status {
			ok = ok || status == s
		}
		if !ok {
			t.Errorf("%s: %d %.100s, want %v", name, status, data, c.status)
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		if status, _, _ := h.do(t, "dev", method, ed, []byte(strings.Repeat("<", 200<<10))); status != http.StatusRequestEntityTooLarge && status != http.StatusMethodNotAllowed {
			t.Errorf("%s of an oversized EndDevice: %d", method, status)
		}
	}
	if status, _, _ := h.do(t, "dev", http.MethodGet, ed, nil); status != http.StatusOK {
		t.Errorf("GET after the hostile bodies: %d", status)
	}
}

// A device cannot grow its SubscriptionList without end: the oldest
// subscriptions beyond MaxListItems go.
func TestHostileSubscriptionFlood(t *testing.T) {
	snk := newSink(t, http.StatusCreated)
	cfg := notifyConfig(snk)
	cfg.MaxListItems = 5
	h := newHarness(t, cfg, "dev")
	ed, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed, snk.URL+"/n"))); status != http.StatusCreated {
			t.Fatalf("POST: %d %s", status, data)
		}
	}
	_, _, data := h.do(t, "dev", http.MethodGet, ed+"/sub?l=255", nil)
	if l := decode[ieee20305.SubscriptionList](t, data); l.All != 5 {
		t.Errorf("%d subscriptions after 20 with MaxListItems 5", l.All)
	}
}

// The default policy refuses every spelling of a loopback or link-local
// address that the dialer reaches.
func TestHostileNotificationURISpellings(t *testing.T) {
	for _, s := range []string{
		"https://[fe80::1%25en0]/n",
		"https://[fe80::a9fe:a9fe%25eth0]/n",
		"https://127.1/n",
		"https://2130706433/n",
		"https://0x7f000001/n",
		"https://0/n",
	} {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		if server.DefaultNotificationURIAllowed(u) {
			t.Errorf("%s is allowed", s)
		}
	}
}

// A receiver of notifications must not redirect the server's POST, which
// carries the server's certificate, to a host the policy refuses.
func TestHostileNotificationRedirect(t *testing.T) {
	var internal atomic.Int64
	inner := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internal.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(inner.Close)
	var redirected atomic.Int64
	outer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		http.Redirect(w, r, inner.URL+"/metadata", http.StatusTemporaryRedirect)
	}))
	outer.TLS = inner.TLS.Clone()
	outer.StartTLS()
	t.Cleanup(outer.Close)
	outerHost := strings.TrimPrefix(outer.URL, "https://")
	h := newHarness(t, server.Config{NotificationClient: inner.Client(),
		NotificationURIAllowed: func(u *url.URL) bool { return u.Host == outerHost },
		NotificationRetry:      ieee20305.NoRetry()}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	status, hdr, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", outer.URL+"/n")))
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, data)
	}
	sub := hdr.Get("Location")
	if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	// The notification ends the subscription either way: delivered, or
	// refused as a redirect.
	waitFor(t, func() bool { return redirected.Load() > 0 })
	deadline := time.Now().Add(5 * time.Second)
	for internal.Load() == 0 && time.Now().Before(deadline) {
		if _, err := h.srv.Admin().Get(ctx, sub); err != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if n := internal.Load(); n > 0 {
		t.Errorf("the notification was redirected to a host the policy refuses (%d POSTs)", n)
	}
}

// Close ends the notifications in flight to a receiver that never answers,
// or answers with a body that never ends.
func TestHostileReceiverAtClose(t *testing.T) {
	for _, mode := range []string{"stall", "endless"} {
		t.Run(mode, func(t *testing.T) {
			got := make(chan struct{}, 1)
			release := make(chan struct{})
			snk := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case got <- struct{}{}:
				default:
				}
				if mode == "endless" {
					w.WriteHeader(http.StatusCreated)
					chunk := []byte(strings.Repeat("x", 4096))
					for {
						if _, err := w.Write(chunk); err != nil {
							return
						}
						select {
						case <-release:
							return
						default:
						}
					}
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(snk.Close)
			t.Cleanup(func() { close(release) })
			cfg := server.Config{NotificationClient: snk.Client(), NotificationURIAllowed: allowAll,
				NotificationRetry: ieee20305.NoRetry()}
			h := newHarness(t, cfg, "dev")
			ctx := context.Background()
			ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
			if err != nil {
				t.Fatal(err)
			}
			if status, _, data := h.do(t, "dev", http.MethodPost, ed+"/sub", marshal(t, subscription(ed+"/fsa", snk.URL+"/n"))); status != http.StatusCreated {
				t.Fatalf("POST: %d %s", status, data)
			}
			if _, err := h.srv.Admin().AssignFSA(ctx, h.id("dev"), examples.FunctionSetAssignments(0)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("no notification")
			}
			done := make(chan error, 1)
			go func() { done <- h.srv.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Close: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close waits for a receiver that never answers")
			}
		})
	}
}

// owned returns the number of nodes of the device name in the store of h,
// read node by node.
func owned(t *testing.T, h *harness, name string) int {
	t.Helper()
	ctx := context.Background()
	hrefs, err := h.srv.Store().Hrefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, href := range hrefs {
		if node, err := h.srv.Store().Get(ctx, href); err == nil && node.Owner == h.lfdi(name) {
			n++
		}
	}
	return n
}

// A device cannot grow the server through the lists in the resources it
// creates: it posts DERs, and DERPrograms to the list of each, until the
// server refuses. It then owns no more than MaxDeviceResources resources,
// each request beyond is answered 403 and counted, and what it created is
// read as it was posted.
func TestHostileNestedLists(t *testing.T) {
	const most = 60
	h := newHarness(t, server.Config{MaxDeviceResources: most, MaxListItems: 6}, "dev", "other")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev"), Register: true, PIN: 111115})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("other")}); err != nil {
		t.Fatal(err)
	}
	before := owned(t, h, "other")
	var created []string
	refused := uint64(0)
	post := func(list string, doc any) string {
		t.Helper()
		status, hdr, data := h.do(t, "dev", http.MethodPost, list, marshal(t, doc))
		switch status {
		case http.StatusCreated:
			created = append(created, hdr.Get("Location"))
			return hdr.Get("Location")
		case http.StatusForbidden:
			refused++
		default:
			t.Fatalf("POST %s: %d %s", list, status, data)
		}
		return ""
	}
	for range 6 {
		der := post(ed+"/der", &ieee20305.DER{})
		if der == "" {
			continue
		}
		for j := range 6 {
			p := examples.DERProgram(0)
			p.Primacy = ieee20305.PrimacyType(j + 1)
			post(der+"/derp", p)
		}
	}
	// What is left after the last DER that fits is filled one by one.
	for range 6 {
		post(ed+"/lel", examples.LogEvent(0))
	}
	if refused == 0 || len(created) == 0 {
		t.Fatalf("%d resources created, %d refused: the device did not reach its cap", len(created), refused)
	}
	if n := owned(t, h, "dev"); n != most {
		t.Errorf("the device owns %d resources, want %d (MaxDeviceResources)", n, most)
	}
	if c, ok := h.srv.Store().(server.OwnerCounter); ok {
		if n, err := c.CountOwned(ctx, h.lfdi("dev")); err != nil || n != most {
			t.Errorf("CountOwned = %d, %v; want %d", n, err, most)
		}
	}
	if st := h.srv.Stats(); st.RefusedResources != refused {
		t.Errorf("Stats counts %d refused resources, want %d", st.RefusedResources, refused)
	}
	for _, href := range created {
		status, _, data := h.do(t, "dev", http.MethodGet, href, nil)
		if status != http.StatusOK {
			t.Errorf("GET %s: %d %s", href, status, data)
		}
	}
	// At its cap, the device still reads and replaces what it owns.
	if status, _, _ := h.do(t, "dev", http.MethodPut, ed+"/dstat", marshal(t, examples.DeviceStatus(0))); status != http.StatusNoContent {
		t.Errorf("PUT of the DeviceStatus at the cap: %d", status)
	}
	// What it deletes makes room.
	if status, _, _ := h.do(t, "dev", http.MethodDelete, created[0], nil); status != http.StatusNoContent {
		t.Fatalf("DELETE %s: %d", created[0], status)
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/lel", marshal(t, examples.LogEvent(0))); status != http.StatusCreated {
		t.Errorf("POST after a DELETE: %d", status)
	}
	// The cap is each device's own, and none of the operator's.
	if status, _, _ := h.do(t, "other", http.MethodPost, h.srv.Admin().EndDevice(h.id("other"))+"/lel", marshal(t, examples.LogEvent(0))); status != http.StatusCreated {
		t.Errorf("POST of another device: %d", status)
	}
	if n := owned(t, h, "other"); n != before+1 {
		t.Errorf("the other device owns %d resources, want %d", n, before+1)
	}
	for range 3 {
		if _, err := h.srv.Admin().Create(ctx, ed+"/lel", examples.LogEvent(0), h.lfdi("dev")); err != nil {
			t.Fatalf("the operator's Create at the cap of the device: %v", err)
		}
	}
}

// The cap at its edges: a device that owns n resources of at most n+k
// creates k more, and is refused the next.
func TestMaxDeviceResources(t *testing.T) {
	probe := newHarness(t, server.Config{}, "dev")
	ed, err := probe.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: probe.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	base := owned(t, probe, "dev") // the EndDevice and its parts
	for _, tt := range []struct {
		name    string
		most    int
		creates int // of 4 LogEvents posted
	}{
		{"below what it owns", base - 1, 0},
		{"what it owns", base, 0},
		{"one more", base + 1, 1},
		{"two more", base + 2, 2},
		{"default", 0, 4},
		{"none", -1, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, server.Config{MaxDeviceResources: tt.most}, "dev")
			if _, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: h.id("dev")}); err != nil {
				t.Fatal(err)
			}
			got := 0
			for range 4 {
				switch status, _, data := h.do(t, "dev", http.MethodPost, ed+"/lel", marshal(t, examples.LogEvent(0))); status {
				case http.StatusCreated:
					got++
				case http.StatusForbidden:
				default:
					t.Fatalf("POST: %d %s", status, data)
				}
			}
			if got != tt.creates {
				t.Errorf("%d LogEvents created, want %d", got, tt.creates)
			}
			if st := h.srv.Stats(); st.RefusedResources != uint64(4-tt.creates) {
				t.Errorf("Stats counts %d refused, want %d", st.RefusedResources, 4-tt.creates)
			}
		})
	}
}

// What the server creates for a device's requests counts too: mirrors
// with their readings, and the answers to flow reservation requests.
func TestMaxDeviceResourcesOfMirrorsAndReservations(t *testing.T) {
	const most = 80
	h := newHarness(t, server.Config{MaxDeviceResources: most}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.Admin().Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}); err != nil {
		t.Fatal(err)
	}
	mup := examples.MirrorUsagePoint(0)
	lfdi := h.id("dev")
	mup.DeviceLFDI = lfdi[:]
	mup.MirrorMeterReading = nil
	status, hdr, data := h.do(t, "dev", http.MethodPost, "/mup", marshal(t, mup))
	if status != http.StatusCreated {
		t.Fatalf("POST of the mirror: %d %s", status, data)
	}
	mirror := hdr.Get("Location")
	refused := 0
	// Each reading has a MeterReading of its own, with a ReadingType, a
	// Reading and a ReadingSet with a Reading.
	for i := range 40 {
		mmr := examples.MirrorMeterReading(0)
		mmr.MRID = ieee20305.MRIDType{byte(i + 1)}
		mmr.ReadingType = examples.ReadingType(0)
		mmr.Reading = examples.Reading(0)
		set := examples.MirrorReadingSet(0)
		set.MRID = ieee20305.MRIDType{0xA0, byte(i + 1)}
		set.Reading = []ieee20305.Reading{*examples.Reading(0)}
		mmr.MirrorReadingSet = []ieee20305.MirrorReadingSet{*set}
		switch status, _, data := h.do(t, "dev", http.MethodPost, mirror, marshal(t, mmr)); status {
		case http.StatusCreated, http.StatusNoContent:
		case http.StatusForbidden:
			refused++
		default:
			t.Fatalf("POST of reading %d: %d %s", i, status, data)
		}
	}
	if refused == 0 {
		t.Fatal("no reading was refused: the device did not reach its cap")
	}
	if n := owned(t, h, "dev"); n > most || n < most-3 {
		t.Errorf("the device owns %d resources after its readings, want at most %d and all but a few of them", n, most)
	}
	// A flow reservation request needs room for itself and its answer.
	for owned(t, h, "dev") < most {
		if _, err := h.srv.Admin().Create(ctx, ed+"/lel", examples.LogEvent(0), h.lfdi("dev")); err != nil {
			t.Fatal(err)
		}
	}
	if status, _, _ := h.do(t, "dev", http.MethodPost, ed+"/frq", marshal(t, examples.FlowReservationRequest(0))); status != http.StatusForbidden {
		t.Errorf("POST of a flow reservation request at the cap: %d", status)
	}
	if n := owned(t, h, "dev"); n != most {
		t.Errorf("the device owns %d resources, want %d", n, most)
	}
}

// A DERProgram a device posts to the AssociatedDERProgramList of its DER
// is a DERProgram: it is read, listed and the DER's CurrentDERProgram, and
// has no lists of its own.
func TestAssociatedDERProgram(t *testing.T) {
	h := newHarness(t, server.Config{}, "dev")
	ctx := context.Background()
	ed, err := h.srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: h.id("dev")})
	if err != nil {
		t.Fatal(err)
	}
	status, hdr, data := h.do(t, "dev", http.MethodPost, ed+"/der", marshal(t, &ieee20305.DER{}))
	if status != http.StatusCreated {
		t.Fatalf("POST of the DER: %d %s", status, data)
	}
	der := hdr.Get("Location")
	before := owned(t, h, "dev")
	p := examples.DERProgram(0)
	p.Primacy = 3
	status, hdr, data = h.do(t, "dev", http.MethodPost, der+"/derp", marshal(t, p))
	if status != http.StatusCreated {
		t.Fatalf("POST of the DERProgram: %d %s", status, data)
	}
	prog := hdr.Get("Location")
	if n := owned(t, h, "dev"); n != before+1 {
		t.Errorf("the DERProgram added %d resources, want 1", n-before)
	}
	node, err := h.srv.Admin().Get(ctx, prog)
	if err != nil || node.Resource != "DERProgram" {
		t.Fatalf("stored as %+v, %v; want a DERProgram", node, err)
	}
	status, _, data = h.do(t, "dev", http.MethodGet, prog, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", prog, status, data)
	}
	if got := decode[ieee20305.DERProgram](t, data); got.Primacy != 3 || got.GetHref() != prog {
		t.Errorf("GET %s: primacy %d, href %q", prog, got.Primacy, got.GetHref())
	}
	_, _, data = h.do(t, "dev", http.MethodGet, der+"/derp?l=10", nil)
	if l := decode[ieee20305.DERProgramList](t, data); l.All != 1 || len(l.DERProgram) != 1 {
		t.Errorf("the list has %d of %d programs", len(l.DERProgram), l.All)
	}
	status, _, data = h.do(t, "dev", http.MethodGet, der+"/cdp", nil)
	if status != http.StatusOK {
		t.Fatalf("GET of the CurrentDERProgram: %d %s", status, data)
	}
	if got := decode[ieee20305.DERProgram](t, data); got.Primacy != 3 {
		t.Errorf("CurrentDERProgram of primacy %d", got.Primacy)
	}
	if status, _, _ := h.do(t, "dev", http.MethodDelete, prog, nil); status != http.StatusNoContent {
		t.Errorf("DELETE %s: %d", prog, status)
	}
}
