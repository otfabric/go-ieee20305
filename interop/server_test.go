// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// TestServer runs each client image against the server of this module, for
// each edition the client speaks, with every operation it declares.
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
				t.Run(version, func(t *testing.T) {
					t.Parallel()
					serverScenario(t, img, version, cl.Operations, caps)
				})
			}
		})
	}
}

// serverScenario serves the server of this module, seeded from the
// baseline fixture of the client image, and runs the client's operations
// against it.
func serverScenario(t *testing.T, img, version string, ops []string, caps capabilities) {
	f := bakedFixture(t, img, "baseline")
	p := newPKI(t, f.Device.Serial, "host.docker.internal", "localhost", "127.0.0.1")
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{PeerCertificates: tlsccm.PeerCertificates, DefaultEdition: edition})
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
	seed(t, srv, f, security.CertificateLFDI(p.device.cert), edition)
	url := serve(t, srv, p, caps.offers(suiteGCM128))
	for _, op := range []string{"discover", "register", "poll-controls", "report"} {
		if !contains(ops, op) {
			t.Logf("SKIP_UNSUPPORTED %s %s %s", caps.Adapter, version, op)
			continue
		}
		t.Run(op, func(t *testing.T) {
			r := runClient(t, img, op, version, url, p)
			if !r.OK {
				detail := ""
				if r.Error != nil {
					detail = r.Error.Code + ": " + r.Error.Detail
				}
				t.Errorf("%s %s: %s\nrequest %s\nresponse %s", caps.Adapter, op, detail, r.Request, r.Response)
				if os.Getenv("IEEE20305_INTEROP_TRACE") != "" {
					t.Logf("output of the client:\n%s", r.Output)
				}
			}
		})
	}
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

// seed provisions the device of the fixture on srv, with its PIN, and
// assigns it the fixture's DER programs with their controls, which start
// now (plus their start), as a server of the contract does.
func seed(t *testing.T, srv *server.Server, f fixture, lfdi security.LFDI, edition ieee20305.Edition) {
	t.Helper()
	ctx := context.Background()
	a := srv.Admin()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}))
	must(a.Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}))
	tmpl := &ieee20305.EndDevice{}
	if f.Device.DeviceCategory != "" {
		cat, err := hex.DecodeString(f.Device.DeviceCategory)
		must(err)
		dc := ieee20305.DeviceCategoryType(cat)
		tmpl.DeviceCategory = &dc
	}
	_, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Edition: edition, Register: true, PIN: f.Device.PIN, EndDevice: tmpl})
	must(err)
	fsaDoc := &ieee20305.FunctionSetAssignments{MRID: newMRID(t)}
	desc := ieee20305.String32("interop")
	fsaDoc.Description = &desc
	fsa, err := a.AssignFSA(ctx, lfdi, fsaDoc)
	must(err)
	must(a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String()))
	now := time.Now()
	for _, fp := range f.DERPrograms {
		prog := &ieee20305.DERProgram{Primacy: ieee20305.PrimacyType(fp.Primacy)}
		prog.MRID = mrid(t, fp.MRID)
		pd := ieee20305.String32(fp.Description)
		prog.Description = &pd
		href, err := a.Create(ctx, "/derp", prog, "")
		must(err)
		must(a.AddMember(ctx, fsa+"/derp", href))
		if dc := fp.DefaultControl; dc != nil {
			d := &ieee20305.DefaultDERControl{DERControlBase: base(t, dc.Base)}
			d.MRID = mrid(t, dc.MRID)
			dd := ieee20305.String32(dc.Description)
			d.Description = &dd
			must(a.Put(ctx, &server.Node{Href: href + "/dderc", Resource: "DefaultDERControl", Value: d}))
		}
		for _, fc := range fp.Controls {
			c := &ieee20305.DERControl{DERControlBase: base(t, fc.Base)}
			c.MRID = mrid(t, fc.MRID)
			cd := ieee20305.String32(fc.Description)
			c.Description = &cd
			c.CreationTime = ieee20305.TimeType(now.Unix())
			c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix() + int64(fc.Start)), Duration: ieee20305.UInt32(fc.Duration)}
			_, err := a.Create(ctx, href+"/derc", c, "")
			must(err)
		}
	}
}

func mrid(t *testing.T, s string) ieee20305.MRIDType {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("mRID %q: %v", s, err)
	}
	return b
}

func newMRID(t *testing.T) ieee20305.MRIDType {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// base returns the DERControlBase of a fixture's base.
func base(t *testing.T, m map[string]any) ieee20305.DERControlBase {
	t.Helper()
	var b ieee20305.DERControlBase
	for k, v := range m {
		switch k {
		case "opModConnect", "opModEnergize":
			x, ok := v.(bool)
			if !ok {
				t.Fatalf("%s: %v", k, v)
			}
			if k == "opModConnect" {
				b.OpModConnect = &x
			} else {
				b.OpModEnergize = &x
			}
		case "opModMaxLimW":
			n, ok := v.(float64)
			if !ok {
				t.Fatalf("%s: %v", k, v)
			}
			b.OpModMaxLimW = &ieee20305.PerCentControlType{Value: ieee20305.PerCent(n)}
		case "opModFixedW":
			n, ok := v.(float64)
			if !ok {
				t.Fatalf("%s: %v", k, v)
			}
			b.OpModFixedW = &ieee20305.SignedPerCentControlType{Value: ieee20305.SignedPerCent(n)}
		default:
			t.Fatalf("a fixture control %s the suite does not know", k)
		}
	}
	return b
}
