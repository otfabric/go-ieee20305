// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// bodyTransport answers every request with body, without a network.
type bodyTransport struct{ body []byte }

func (b bodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {client.MediaType}},
		Body: io.NopCloser(bytes.NewReader(b.body)), Request: req}, nil
}

func fuzzSeeds(f *testing.F) {
	list := edevPage(0, 3, 10)
	var ed ieee20305.EndDevice
	ed.ConfigurationLink = &ieee20305.ConfigurationLink{}
	ed.ConfigurationLink.Href = "/edev/1/cfg"
	ed.DERListLink = &ieee20305.DERListLink{}
	ed.DERListLink.Href = "https://evil.example/der"
	dc := ieee20305.DeviceCapability{EndDeviceListLink: &ieee20305.EndDeviceListLink{}}
	dc.EndDeviceListLink.Href = "/edev"
	for _, v := range []any{list, &ed, &dc, edevPage(0, 0, ^uint32(0))} {
		data, err := ieee20305.Marshal(v)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`<EndDeviceList xmlns="urn:ieee:std:2030.5:ns" all="4294967295" results="0"/>`))
	f.Add([]byte(`<DeviceCapability xmlns="urn:ieee:std:2030.5:ns"><EndDeviceListLink href="//x"/></DeviceCapability>`))
	f.Add([]byte(""))
}

// FuzzWalk walks a server that answers every href with the same arbitrary
// document: Walk and Iterate never panic and end, and Walk visits nothing
// off the server.
func FuzzWalk(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, body []byte) {
		c, err := client.New(client.Config{Server: "https://utility.example:8443",
			HTTPClient: &http.Client{Transport: bodyTransport{body}}, MaxListItems: 64, MaxResponseBytes: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		followed := 0
		_ = c.Walk(ctx, "/dcap", client.WalkOptions{MaxDepth: 2, PageSize: 16, Follow: func(ieee20305.LinkRef) bool {
			followed++
			return followed <= 32
		}}, func(s client.Step) error {
			if _, err := c.Resolve(s.Href); err != nil {
				t.Fatalf("visited %q off the server", s.Href)
			}
			return nil
		})
		// Iterate ends: at the end of the list, or at MaxListItems.
		_ = c.Iterate(ctx, "/edev", 16, func(any) error { return nil })
	})
}

// FuzzNotificationHandler posts arbitrary bodies to a NotificationHandler:
// it never panics, and answers 201 only for a Notification.
func FuzzNotificationHandler(f *testing.F) {
	n := &ieee20305.Notification{SubscriptionURI: "/edev/1/sub/1"}
	n.SubscribedResource = "/edev/1"
	data, err := ieee20305.Marshal(n)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte(`<Notification xmlns="urn:ieee:std:2030.5:ns"><Resource xsi:type="EndDevice" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"/></Notification>`))
	f.Add([]byte("<"))
	h, err := client.NewNotificationHandler(func(context.Context, *ieee20305.Notification) error { return nil },
		client.NotificationConfig{MaxBytes: 32 << 10, MaxSubscriptions: 8})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/n", bytes.NewReader(body))
		r.Header.Set("Content-Type", client.MediaType)
		h.ServeHTTP(w, r)
		switch w.Code {
		case http.StatusCreated:
			if v, err := ieee20305.Decode(body); err != nil {
				t.Fatalf("201 for a body that does not decode: %v", err)
			} else if _, ok := v.(*ieee20305.Notification); !ok {
				t.Fatalf("201 for a %T", v)
			}
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		default:
			t.Fatalf("status %d", w.Code)
		}
	})
}
