// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// testDevice returns a device certificate.
func testDevice(t testing.TB, name string) *x509.Certificate {
	t.Helper()
	ca, key, err := security.TestCA("server test CA")
	if err != nil {
		t.Fatal(err)
	}
	cert, _, err := security.TestDeviceCert(ca, key, name)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// harness is a server on loopback, without TLS: the client certificate of a
// request is the one named by its X-Test-Device header.
type harness struct {
	srv     *server.Server
	http    *httptest.Server
	devices map[string]*x509.Certificate
}

func newHarness(t testing.TB, cfg server.Config, names ...string) *harness {
	t.Helper()
	h := &harness{devices: map[string]*x509.Certificate{}}
	for _, n := range names {
		h.devices[n] = testDevice(t, n)
	}
	cfg.PeerCertificates = func(r *http.Request) ([]*x509.Certificate, error) {
		if c, ok := h.devices[r.Header.Get("X-Test-Device")]; ok {
			return []*x509.Certificate{c}, nil
		}
		return nil, nil
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	h.http = httptest.NewServer(srv)
	t.Cleanup(func() {
		h.http.Close()
		_ = srv.Close()
	})
	return h
}

func (h *harness) lfdi(name string) string { return security.CertificateLFDI(h.devices[name]).String() }

// do sends a request as the device name and returns the status, headers
// and body.
func (h *harness) do(t testing.TB, device, method, path string, body []byte, header ...string) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.http.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Device", device)
	if body != nil {
		req.Header.Set("Content-Type", server.MediaType)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, data
}

var idPattern = regexp.MustCompile(`\{id\d+\}`)

// samplePath turns a WADL sample path into a path: every {idN} becomes 1.
func samplePath(p string) string { return idPattern.ReplaceAllString(p, "1") }

// getElement returns the document element GET answers with on spec.
func getElement(spec ieee20305.ResourceSpec) string {
	for _, m := range spec.Methods {
		if m.Method == http.MethodGet {
			return m.Response
		}
	}
	return ""
}

// distinct returns the resources of the WADL, without those whose sample
// path an earlier one has: the subtypes of Response share
// /rsps/{id1}/rsp/{id2}, where one resource stands for all of them.
func distinct() []ieee20305.ResourceSpec {
	seen := map[string]bool{}
	var out []ieee20305.ResourceSpec
	for _, spec := range ieee20305.Resources {
		p := samplePath(spec.SamplePath)
		if !seen[p] {
			seen[p] = true
			out = append(out, spec)
		}
	}
	return out
}

// seedAll creates every resource of the WADL at its sample path, owned by
// the device owner, with an example document; a list holds the resource at
// its own path plus /1 when there is one.
func seedAll(t testing.TB, h *harness, owner string) {
	t.Helper()
	ctx := context.Background()
	a := h.srv.Admin()
	for _, spec := range distinct() {
		p := samplePath(spec.SamplePath)
		if p == "/dcap" || p == "/tm" {
			continue
		}
		v := examples.Element(getElement(spec), 1)
		if v == nil {
			t.Fatalf("%s: no example of %s", spec.ID, getElement(spec))
		}
		if l, ok := v.(ieee20305.Lister); ok {
			if err := l.SetListItems(nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Put(ctx, &server.Node{Href: p, Resource: spec.ID, Value: v, Owner: owner}); err != nil {
			t.Fatalf("%s: %v", spec.ID, err)
		}
	}
	for _, spec := range distinct() {
		p := samplePath(spec.SamplePath)
		n, err := a.Get(ctx, p)
		if err != nil || n.Value == nil {
			continue
		}
		if _, ok := n.Value.(ieee20305.Lister); !ok {
			continue
		}
		if _, err := a.Get(ctx, p+"/1"); err == nil {
			if err := a.AddMember(ctx, p, p+"/1"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConformance(t *testing.T) {
	for _, edition := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		t.Run(edition.String(), func(t *testing.T) {
			h := newHarness(t, server.Config{DefaultEdition: edition, NotificationClient: &http.Client{}, NotificationRetryDelay: time.Millisecond}, "dev")
			seedAll(t, h, h.lfdi("dev"))
			// An operator, who may use every method the server supports.
			op, err := security.ParseLFDI(h.lfdi("dev"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: op, Role: server.RoleOperator, Edition: edition}); err != nil {
				t.Fatal(err)
			}
			codec := edition.Codec()
			for _, spec := range distinct() {
				p := samplePath(spec.SamplePath)
				inEdition := ieee20305.InEdition(spec.ID, edition)
				for _, m := range spec.Methods {
					var body []byte
					if m.Request != "" {
						v := examples.Element(m.Request, 1)
						if v == nil {
							t.Fatalf("%s %s: no example of %s", m.Method, spec.ID, m.Request)
						}
						if sub, ok := v.(*ieee20305.Subscription); ok {
							// A subscription of a resource there is, sent
							// where no one listens.
							sub.SubscribedResource, sub.NotificationURI, sub.Encoding = "/edev", "https://127.0.0.1:1/ntfy", 0
						}
						if m, ok := v.(*ieee20305.MirrorUsagePoint); ok {
							m.DeviceLFDI = make(ieee20305.HexBinary160, 20)
						}
						if r, ok := v.(*ieee20305.Response); ok {
							// A Response of a device: an LFDI.
							r.EndDeviceLFDI = make(ieee20305.HexBinary160, 20)
						}
						if ed, ok := v.(*ieee20305.EndDevice); ok {
							// The sFDI of an EndDevice is that of its lFDI.
							l := security.LFDI{0x2a, byte(edition)}
							lfdi := ieee20305.HexBinary160(l[:])
							ed.LFDI, ed.SFDI = &lfdi, ieee20305.SFDIType(l.SFDI())
						}
						out, err := ieee20305.MarshalEdition(v, edition)
						if err != nil {
							t.Fatal(err)
						}
						body = out
					}
					status, hdr, data := h.do(t, "dev", m.Method, p, body)
					where := m.Method + " " + spec.ID + " " + p
					if !inEdition {
						if status != http.StatusNotFound {
							t.Errorf("%s: %d for a resource not of IEEE 2030.5-%v", where, status, edition)
						}
						continue
					}
					if status >= 500 {
						t.Errorf("%s: %d %s", where, status, data)
						continue
					}
					switch m.Conformance {
					case ieee20305.ConformanceError, ieee20305.ConformanceDiscouraged:
						if _, ok := hdr["Allow"]; status != http.StatusMethodNotAllowed || !ok {
							t.Errorf("%s (%v in the WADL): %d, Allow %q", where, m.Conformance, status, hdr.Get("Allow"))
						}
						continue
					default:
					}
					switch m.Method {
					case http.MethodGet:
						if status != http.StatusOK || hdr.Get("Content-Type") != server.MediaType {
							t.Errorf("%s: %d %s", where, status, hdr.Get("Content-Type"))
							continue
						}
						v, err := codec.Decode(data)
						if err != nil {
							t.Errorf("%s: %v", where, err)
							continue
						}
						if name, _ := ieee20305.ElementName(v); name.Local != m.Response {
							t.Errorf("%s: a %s, want %s", where, name.Local, m.Response)
						}
						if edition == ieee20305.Edition2018 && bytes.Contains(data, []byte("_r2_3")) {
							t.Errorf("%s: a 2023 element in a 2018 document", where)
						}
					case http.MethodHead:
						if status != http.StatusOK || len(data) != 0 {
							t.Errorf("%s: %d with %d bytes", where, status, len(data))
						}
					case http.MethodPut:
						if status != http.StatusNoContent && status != http.StatusCreated {
							t.Errorf("%s: %d %s", where, status, data)
						}
					case http.MethodPost:
						// Non-list resources take a POST through the function
						// sets; a list creates an item.
						if status != http.StatusCreated && status != http.StatusNoContent && status != http.StatusMethodNotAllowed {
							t.Errorf("%s: %d %s", where, status, data)
						}
						if status == http.StatusCreated && !strings.HasPrefix(hdr.Get("Location"), p+"/") {
							t.Errorf("%s: Location %q", where, hdr.Get("Location"))
						}
					case http.MethodDelete:
						if status != http.StatusNoContent && status != http.StatusNotFound {
							t.Errorf("%s: %d %s", where, status, data)
						}
					}
				}
			}
		})
	}
}
