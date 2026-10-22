// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/security"
)

type pki struct {
	roots  *x509.CertPool
	server tls.Certificate
	device tls.Certificate
	lfdi   security.LFDI
}

func newPKI(t *testing.T) pki {
	t.Helper()
	ca, caKey, err := security.TestCA("Test")
	if err != nil {
		t.Fatal(err)
	}
	srv, srvKey, err := security.TestServerCert(ca, caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	dev, devKey, err := security.TestDeviceCert(ca, caKey, "device")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return pki{
		roots:  roots,
		server: tls.Certificate{Certificate: [][]byte{srv.Raw}, PrivateKey: srvKey, Leaf: srv},
		device: tls.Certificate{Certificate: [][]byte{dev.Raw}, PrivateKey: devKey, Leaf: dev},
		lfdi:   security.CertificateLFDI(dev),
	}
}

func ptr[T any](v T) *T { return &v }

func writeDoc(t *testing.T, w http.ResponseWriter, status int, v any) {
	data, err := ieee20305.Marshal(v)
	if err != nil {
		t.Error(err)
		return
	}
	w.Header().Set("Content-Type", client.MediaType)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// server is a mutual-TLS server that answers the documents of the tests.
func server(t *testing.T, p pki, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 || security.CertificateLFDI(r.TLS.PeerCertificates[0]) != p.lfdi {
			http.Error(w, "unknown device", http.StatusForbidden)
			return
		}
		if r.Header.Get("Accept") != client.MediaType {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		handler(w, r)
	}))
	srv.TLS = security.ServerTLSConfig(p.server, p.roots)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, p pki, srv *httptest.Server, cfg client.Config) *client.Client {
	t.Helper()
	cfg.Server = srv.URL
	cfg.HTTPClient = client.NewHTTPClient(p.device, p.roots)
	c, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDeviceCapabilityAndLinks(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dcap":
			dc := ieee20305.DeviceCapability{EndDeviceListLink: &ieee20305.EndDeviceListLink{}}
			dc.EndDeviceListLink.Href, dc.EndDeviceListLink.All = "/edev", ptr(ieee20305.UInt32(1))
			dc.PollRate = ptr(ieee20305.UInt32(300))
			writeDoc(t, w, http.StatusOK, &dc)
		case "/edev":
			if r.URL.Query().Get("s") != "0" || r.URL.Query().Get("l") != "10" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			list := ieee20305.EndDeviceList{EndDevice: []ieee20305.EndDevice{{}}}
			list.All, list.Results = 1, 1
			list.EndDevice[0].SFDI = 987654321005
			writeDoc(t, w, http.StatusOK, &list)
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, p, srv, client.Config{})
	ctx := context.Background()
	dc, err := c.DeviceCapability(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dc.GetPollRate() != 300 || dc.EndDeviceListLink.Href != "/edev" {
		t.Fatalf("DeviceCapability = %+v", dc)
	}
	var list ieee20305.EndDeviceList
	if err := c.GetList(ctx, dc.EndDeviceListLink.Href, client.ListOptions{Limit: 10}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.EndDevice) != 1 || list.EndDevice[0].SFDI != 987654321005 {
		t.Errorf("EndDeviceList = %+v", list)
	}
	v, err := c.Fetch(ctx, "/dcap")
	if _, ok := v.(*ieee20305.DeviceCapability); err != nil || !ok {
		t.Errorf("Fetch = %T, %v", v, err)
	}
}

func TestWrites(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodDelete && r.Header.Get("Content-Type") != client.MediaType {
			t.Errorf("%s Content-Type = %q", r.Method, r.Header.Get("Content-Type"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/edev":
			body, _ := io.ReadAll(r.Body)
			var ed ieee20305.EndDevice
			if err := ieee20305.Unmarshal(body, &ed); err != nil || ed.SFDI != 42 {
				t.Errorf("POST body %s: %v", body, err)
			}
			w.Header().Set("Location", "/edev/7")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/edev/7", r.Method == http.MethodDelete && r.URL.Path == "/edev/7":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	})
	c := newClient(t, p, srv, client.Config{})
	ctx := context.Background()
	var ed ieee20305.EndDevice
	ed.SFDI = 42
	loc, err := c.Post(ctx, "/edev", &ed)
	if err != nil || loc != "/edev/7" {
		t.Fatalf("Post = %q, %v", loc, err)
	}
	if err := c.Put(ctx, loc, &ed); err != nil {
		t.Errorf("Put: %v", err)
	}
	if err := c.Delete(ctx, loc); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

func TestErrors(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/error":
			writeDoc(t, w, http.StatusBadRequest, &ieee20305.Error{ReasonCode: 1})
		case "/missing":
			http.NotFound(w, r)
		case "/big":
			w.Header().Set("Content-Type", client.MediaType)
			_, _ = io.WriteString(w, "<Time>"+strings.Repeat(" ", 2048)+"</Time>")
		case "/exi":
			w.Header().Set("Content-Type", "application/sep-exi")
			_, _ = w.Write([]byte{0x80})
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html/>")
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/away":
			http.Redirect(w, r, "https://elsewhere.example/dcap", http.StatusFound)
		case "/wrong":
			writeDoc(t, w, http.StatusOK, &ieee20305.Time{})
		}
	})
	c := newClient(t, p, srv, client.Config{MaxResponseBytes: 1024})
	ctx := context.Background()
	var dc ieee20305.DeviceCapability

	err := c.Get(ctx, "/error", &dc)
	var se *ieee20305.StatusError
	if !errors.As(err, &se) || se.StatusCode != 400 || se.Document == nil || se.Document.ReasonCode != 1 || !errors.Is(err, ieee20305.ErrBadRequest) {
		t.Errorf("400 with Error document: %v", err)
	}
	if err := c.Get(ctx, "/missing", &dc); !errors.Is(err, ieee20305.ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	for _, path := range []string{"/big", "/exi", "/html", "/away", "/wrong", "https://elsewhere.example/dcap", "http://" + strings.TrimPrefix(srv.URL, "https://") + "/dcap"} {
		if err := c.Get(ctx, path, &dc); !errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("%s: err = %v, want ErrProtocol", path, err)
		}
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := c.Get(short, "/slow", &dc); !errors.Is(err, ieee20305.ErrTimeout) {
		t.Errorf("timeout: %v", err)
	}
}

func TestUnknownDeviceIsRefused(t *testing.T) {
	p := newPKI(t)
	other := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	cfg := client.Config{Server: srv.URL, HTTPClient: client.NewHTTPClient(other.device, p.roots)}
	c, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var dc ieee20305.DeviceCapability
	if err := c.Get(context.Background(), "/dcap", &dc); err == nil {
		t.Error("a device certificate of another CA was accepted")
	}
}

func TestCSIPAUSCodec(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, _ *http.Request) {
		ctl := csipaus.DERControl{}
		ctl.DERControlBase.OpModExpLimW = &ieee20305.ActivePower{Multiplier: 3, Value: 5}
		data, _ := csipaus.Marshal(&ctl)
		w.Header().Set("Content-Type", client.MediaType)
		_, _ = w.Write(data)
	})
	c := newClient(t, p, srv, client.Config{Codec: csipaus.Codec{}})
	var ctl csipaus.DERControl
	if err := c.Get(context.Background(), "/derc/1", &ctl); err != nil || ctl.DERControlBase.OpModExpLimW == nil || ctl.DERControlBase.OpModExpLimW.Value != 5 {
		t.Errorf("CSIP-AUS DERControl = %+v, %v", ctl.DERControlBase, err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	hc := &http.Client{}
	for _, cfg := range []client.Config{
		{Server: "http://example.test", HTTPClient: hc},
		{Server: "https://user:pw@example.test", HTTPClient: hc},
		{Server: "https://example.test/?q=1", HTTPClient: hc},
		{Server: "::", HTTPClient: hc},
		{Server: "https://example.test"},
	} {
		if _, err := client.New(cfg); !errors.Is(err, ieee20305.ErrConfiguration) {
			t.Errorf("New(%+v) err = %v, want ErrConfiguration", cfg, err)
		}
	}
}

func TestResolve(t *testing.T) {
	c, err := client.New(client.Config{Server: "https://example.test:8443/sep2/", HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	for href, want := range map[string]string{
		"/edev/3":                            "https://example.test:8443/edev/3",
		"edev/3":                             "https://example.test:8443/sep2/edev/3",
		"https://EXAMPLE.test:8443/dcap?s=1": "https://EXAMPLE.test:8443/dcap?s=1",
	} {
		if u, err := c.Resolve(href); err != nil || u.String() != want {
			t.Errorf("Resolve(%q) = %v, %v; want %s", href, u, err, want)
		}
	}
	for _, href := range []string{"https://example.test/dcap", "//other.test/x", "http://example.test:8443/dcap"} {
		if _, err := c.Resolve(href); !errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("Resolve(%q) err = %v", href, err)
		}
	}
}
