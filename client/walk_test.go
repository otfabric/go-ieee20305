// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

func TestIterateAndWalkEdges(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dcap":
			dc := ieee20305.DeviceCapability{EndDeviceListLink: &ieee20305.EndDeviceListLink{}}
			dc.EndDeviceListLink.Href = "/edev"
			dc.MirrorUsagePointListLink = &ieee20305.MirrorUsagePointListLink{}
			dc.MirrorUsagePointListLink.Href = "https://elsewhere.example/mup"
			dc.SelfDeviceLink = &ieee20305.SelfDeviceLink{}
			dc.SelfDeviceLink.Href = "/missing"
			writeDoc(t, w, http.StatusOK, &dc)
		case "/edev":
			// A server that ignores s and always answers the same page,
			// claiming more items than it ever sends.
			var ed ieee20305.EndDevice
			ed.SFDI = 1
			list := ieee20305.EndDeviceList{EndDevice: []ieee20305.EndDevice{ed}}
			list.All, list.Results = 5, 1
			writeDoc(t, w, http.StatusOK, &list)
		case "/tm":
			writeDoc(t, w, http.StatusOK, &ieee20305.Time{})
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, p, srv, client.Config{})
	ctx := context.Background()
	if _, err := c.FetchList(ctx, "/tm", client.ListOptions{}); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("FetchList of a resource: %v", err)
	}
	if _, err := c.FetchList(ctx, "/nope", client.ListOptions{SFDI: 5}); err == nil {
		t.Error("FetchList of a missing list")
	}
	n := 0
	if err := c.Iterate(ctx, "/edev", 1, func(any) error { n++; return nil }); err != nil || n != 5 {
		t.Errorf("Iterate: %d items, %v", n, err)
	}
	stop := errors.New("stop")
	if err := c.Iterate(ctx, "/edev", 0, func(any) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("Iterate with an error: %v", err)
	}
	var hrefs []string
	if err := c.Walk(ctx, "/dcap", client.WalkOptions{MaxDepth: 1, Follow: func(l ieee20305.LinkRef) bool { return l.Resource != "SelfDevice" }}, func(s client.Step) error {
		hrefs = append(hrefs, s.Href)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(hrefs) != 2 || hrefs[0] != "/dcap" || hrefs[1] != "/edev" {
		t.Errorf("Walk visited %v (not another server, not what Follow refuses)", hrefs)
	}
	if err := c.Walk(ctx, "/dcap", client.WalkOptions{}, func(client.Step) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("Walk with an error: %v", err)
	}
	if err := c.Walk(ctx, "/nope", client.WalkOptions{}, func(client.Step) error { return nil }); err == nil {
		t.Error("Walk from a missing root")
	}
	if _, err := c.Time(ctx); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Time without a TimeLink: %v", err)
	}
}
