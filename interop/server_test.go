//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/csipaus/csipserver"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// TestServer runs each client image against the server of this module, for
// each edition the client speaks and each fixture it handles, with every
// operation it declares.
func TestServer(t *testing.T) {
	requireDocker(t)
	for _, c := range []struct{ name, env, def string }{
		{"gridappsd", "IEEE20305_INTEROP_GRIDAPPSD_IMAGE", GridAPPSDImage},
		{"py20305", "IEEE20305_INTEROP_PY20305_IMAGE", Py20305Image},
		{"epri", "IEEE20305_INTEROP_EPRI_IMAGE", EPRIImage},
	} {
		t.Run(c.name, func(t *testing.T) {
			img := image(c.env, c.def)
			caps := capabilitiesOf(t, img)
			if !caps.offers(suiteCCM8) && !caps.offers(suiteGCM128) {
				t.Skipf("SKIP_UNSUPPORTED: %s offers no cipher suite of this server", c.name)
			}
			for _, version := range []string{"2018", "2023"} {
				cl := caps.Versions[version].Client
				if !cl.Supported {
					t.Logf("SKIP_UNSUPPORTED %s %s client", c.name, version)
					continue
				}
				for _, fx := range fixturesOf(cl) {
					t.Run(version+"/"+fx, func(t *testing.T) {
						t.Parallel()
						serverScenario(t, img, version, fx, cl.Operations, caps)
					})
				}
			}
		})
	}
}

// fixturesOf returns the fixtures a role handles: the baseline when it
// names none.
func fixturesOf(r role) []string {
	if len(r.Fixtures) == 0 {
		return []string{"baseline"}
	}
	return r.Fixtures
}

// serverScenario serves the server of this module, seeded from the fixture
// fx of the client image, runs the client's operations against it, and
// checks what each did on the server.
func serverScenario(t *testing.T, img, version, fx string, ops []string, caps capabilities) {
	f := bakedFixture(t, img, fx)
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		t.Fatal(err)
	}
	if f.csipAUS() && edition != ieee20305.Edition2018 {
		t.Skipf("SKIP_UNSUPPORTED: CSIP-AUS profiles IEEE 2030.5-2018")
	}
	p := newPKI(t, f.Device.Serial, "host.docker.internal", "localhost", "127.0.0.1")
	cfg := server.Config{PeerCertificates: tlsccm.PeerCertificates, DefaultEdition: edition,
		NotificationClient: server.NewNotificationClient(p.server, p.roots),
		// The listeners of the clients are published on the loopback
		// address, which the default refuses.
		NotificationURIAllowed: func(*url.URL) bool { return true }}
	if os.Getenv("IEEE20305_INTEROP_TRACE") != "" {
		cfg.Logger = ieee20305.NewStdLogger(log.New(testWriter{t}, "", 0), false)
	}
	if f.csipAUS() {
		cfg = csipserver.Configure(cfg)
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if caps.Adapter == "epri" {
		// EPRI's client knows the schema of SEP 2.0.4, which calls the
		// device category loadShedDeviceCategory, and fails to parse an
		// EndDevice with the deviceCategory of IEEE 2030.5-2018. The
		// element is optional; the server leaves it out for EPRI.
		f.Device.DeviceCategory = ""
	}
	lfdi := security.CertificateLFDI(p.device.cert)
	ed := seed(t, srv, f, lfdi, edition)
	s := &served{t: t, srv: srv, f: f, ed: ed, lfdi: lfdi}
	url := serve(t, srv, p, caps.offers(suiteGCM128))
	for _, op := range []string{"discover", "register", "poll-controls", "poll-pricing", "report", "respond",
		"log-event", "put-der", "flow-reservation", "subscribe"} {
		if !contains(ops, op) {
			t.Logf("SKIP_UNSUPPORTED %s %s %s %s", caps.Adapter, version, fx, op)
			continue
		}
		t.Run(op, func(t *testing.T) {
			s.t = t
			o := runOptions{fixture: fx}
			var trigger func()
			if op == "subscribe" {
				o, trigger = s.subscribe(o)
			}
			done := make(chan result, 1)
			go func() { done <- runClient(t, img, op, version, url, p, o) }()
			if trigger != nil {
				trigger()
			}
			r := <-done
			detail := ""
			if r.Error != nil {
				detail = r.Error.Code + ": " + r.Error.Detail
			}
			if !known(caps.Adapter, "client", version+"/"+fx+"/"+op).expect(t, !r.OK, detail) {
				return
			}
			if !r.OK {
				t.Errorf("%s %s: %s\nrequest %s\nresponse %s", caps.Adapter, op, detail, r.Request, r.Response)
				if os.Getenv("IEEE20305_INTEROP_TRACE") != "" {
					t.Logf("output of the client:\n%s", r.Output)
				}
				return
			}
			s.check(op)
		})
	}
}

// served is the server of a scenario, with what it was seeded with.
type served struct {
	t    *testing.T
	srv  *server.Server
	f    fixture
	ed   string
	lfdi security.LFDI
}

func (s *served) members(list string) []*server.Node {
	s.t.Helper()
	nodes, err := s.srv.Admin().Members(context.Background(), list)
	if err != nil {
		s.t.Fatal(err)
	}
	return nodes
}

func (s *served) get(href string) *server.Node {
	s.t.Helper()
	n, err := s.srv.Admin().Get(context.Background(), href)
	if err != nil {
		s.t.Fatalf("%s: %v", href, err)
	}
	return n
}

func (s *served) endDevice() *ieee20305.EndDevice {
	s.t.Helper()
	switch v := s.get(s.ed).Value.(type) {
	case *csipaus.EndDevice:
		return &v.EndDevice
	case *ieee20305.EndDevice:
		return v
	default:
		s.t.Fatalf("the EndDevice is a %T", v)
		return nil
	}
}

// check checks what the operation op of a client did on the server.
func (s *served) check(op string) {
	t := s.t
	t.Helper()
	switch op {
	case "report":
		want := s.f.Report.MirrorUsagePoint.MRID
		for _, n := range s.members("/mup") {
			if m, ok := n.Value.(*ieee20305.MirrorUsagePoint); ok && strings.EqualFold(hex.EncodeToString(m.MRID), want) {
				return
			}
		}
		t.Errorf("no MirrorUsagePoint %s", want)
	case "respond":
		for _, rs := range s.members("/rsps") {
			for _, n := range s.members(rs.Href + "/rsp") {
				if r, ok := n.Value.(*ieee20305.DERControlResponse); ok && r.Status != nil && *r.Status == 2 {
					return
				}
			}
		}
		t.Error("no DERControlResponse with status 2 (event started)")
	case "log-event":
		want := s.f.LogEvent
		for _, n := range s.members(s.endDevice().LogEventListLink.Href) {
			if l, ok := n.Value.(*ieee20305.LogEvent); ok && uint16(l.LogEventID) == want.LogEventID &&
				uint8(l.LogEventCode) == want.LogEventCode && uint32(l.LogEventPEN) == want.LogEventPEN {
				return
			}
		}
		t.Errorf("no LogEvent %d", want.LogEventID)
	case "put-der":
		ders := s.members(s.endDevice().DERListLink.Href)
		if len(ders) == 0 {
			t.Fatal("no DER")
		}
		d := ders[0].Value.(*ieee20305.DER)
		links := map[string]string{}
		if d.DERCapabilityLink != nil {
			links["DERCapability"] = d.DERCapabilityLink.Href
		}
		if d.DERSettingsLink != nil {
			links["DERSettings"] = d.DERSettingsLink.Href
		}
		if d.DERStatusLink != nil {
			links["DERStatus"] = d.DERStatusLink.Href
		}
		if d.DERAvailabilityLink != nil {
			links["DERAvailability"] = d.DERAvailabilityLink.Href
		}
		for _, name := range []string{"DERCapability", "DERSettings", "DERStatus", "DERAvailability"} {
			n, err := s.srv.Admin().Get(context.Background(), links[name])
			if links[name] == "" || err != nil || n.Value == nil {
				t.Errorf("the device put no %s: %v", name, err)
			}
		}
		s.checkSettings(d)
	case "flow-reservation":
		want := s.f.FlowReservation.MRID
		for _, n := range s.members(s.endDevice().FlowReservationRequestListLink.Href) {
			if r, ok := n.Value.(*ieee20305.FlowReservationRequest); ok && strings.EqualFold(hex.EncodeToString(r.MRID), want) {
				return
			}
		}
		t.Errorf("no FlowReservationRequest %s", want)
	}
}

// checkSettings checks the DERSettings a client put against the fixture.
func (s *served) checkSettings(d *ieee20305.DER) {
	want := s.f.DER.Settings.SetMaxW
	if d.DERSettingsLink == nil || want == nil {
		return
	}
	n, err := s.srv.Admin().Get(context.Background(), d.DERSettingsLink.Href)
	if err != nil {
		return
	}
	var got ieee20305.ActivePower
	switch v := n.Value.(type) {
	case *ieee20305.DERSettings:
		got = v.SetMaxW
	case *csipaus.DERSettings:
		got = v.SetMaxW
	default:
		s.t.Errorf("DERSettings is a %T", n.Value)
		return
	}
	if int64(got.Value) != want.Value || int8(got.Multiplier) != want.Multiplier {
		s.t.Errorf("DERSettings setMaxW %d*10^%d, want %d*10^%d", got.Value, got.Multiplier, want.Value, want.Multiplier)
	}
}

// subscribe publishes the notification listener of a subscribe run on a
// port of the loopback address, and returns the options of the run and a
// trigger: it waits for the subscription, then changes the device's DER
// programs, so that the server notifies the client.
func (s *served) subscribe(o runOptions) (runOptions, func()) {
	t := s.t
	port := freePort(t)
	o.docker = append(o.docker, "--publish", fmt.Sprintf("127.0.0.1:%d:8443", port))
	o.flags = append(o.flags, "--notify-listen", "0.0.0.0:8443",
		"--notify-host", fmt.Sprintf("127.0.0.1:%d", port), "--timeout-ms", "90000")
	return o, func() {
		ctx := context.Background()
		subs := s.endDevice().SubscriptionListLink.Href
		deadline := time.Now().Add(2 * time.Minute)
		for len(s.members(subs)) == 0 {
			if time.Now().After(deadline) {
				t.Error("the client created no subscription")
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		// The listener is up before the client subscribes; give the
		// published port a moment all the same.
		time.Sleep(time.Second)
		fsa := s.members(s.endDevice().FunctionSetAssignmentsListLink.Href)[0].Value.(*ieee20305.FunctionSetAssignments)
		prog := &ieee20305.DERProgram{Primacy: 9}
		prog.MRID = ieee20305.MRIDType{0x5B, 0x5C}
		desc := ieee20305.String32("notified")
		prog.Description = &desc
		a := s.srv.Admin()
		href, err := a.Create(ctx, "/derp", prog, "")
		if err == nil {
			err = a.AddMember(ctx, fsa.DERProgramListLink.Href, href)
		}
		if err != nil {
			t.Error(err)
		}
	}
}

// freePort returns a TCP port of the loopback address that is free now.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// serve serves srv on every interface with CCM_8 (and GCM when allowed),
// for the containers, and returns the URL of its DeviceCapability as they
// reach it.
func serve(t *testing.T, srv *server.Server, p *pki, gcm bool) string {
	t.Helper()
	inner, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	quiet := log.New(io.Discard, "", 0)
	l, err := tlsccm.NewListener(inner, p.server, p.roots, tlsccm.Options{AllowGCM: gcm})
	if err != nil {
		t.Fatal(err)
	}
	var h http.Handler = srv
	if os.Getenv("IEEE20305_INTEROP_TRACE") != "" {
		h = trace(t, srv)
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ErrorLog: quiet}
	tlsccm.ConfigureServer(hs)
	go func() { _ = hs.Serve(l) }()
	t.Cleanup(func() { _ = hs.Close() })
	return fmt.Sprintf("https://host.docker.internal:%d/dcap", inner.Addr().(*net.TCPAddr).Port)
}

// trace logs the requests to h and its answers, with
// IEEE20305_INTEROP_TRACE set.
func trace(t *testing.T, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		t.Logf("%s %s -> %d\n%s", r.Method, r.URL, rec.status, rec.body.String())
	})
}

type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// testWriter writes the lines of a log to t.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}
