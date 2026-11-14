// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// longDescription is a DERControl that decodes and that Validate refuses:
// its description has 100 octets, of at most 32.
const longDescription = `<DERControl xmlns="urn:ieee:std:2030.5:ns" href="/derp/1/derc/1"><mRID>000000000000000000000000000000AA</mRID>` +
	`<description>xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx</description>` +
	`<creationTime>1</creationTime>` +
	`<EventStatus><currentStatus>0</currentStatus><dateTime>1</dateTime><potentiallySuperseded>false</potentiallySuperseded></EventStatus>` +
	`<interval><duration>30</duration><start>2000000000</start></interval><DERControlBase/></DERControl>`

// TestClientDoesNotValidate: the client decodes what the server sends and
// does not check it against the facets of the schema, as SECURITY.md says:
// Get, Fetch, Iterate and a NotificationHandler hand over a document that
// Validate refuses, and the application validates what it acts on.
func TestClientDoesNotValidate(t *testing.T) {
	ctx := context.Background()
	refused := func(what string, v any) {
		t.Helper()
		c, ok := v.(*ieee20305.DERControl)
		if !ok || c.Description == nil || len(*c.Description) != 100 {
			t.Fatalf("%s handed over %T, want the DERControl with its description", what, v)
		}
		if err := c.Validate(); !errors.Is(err, ieee20305.ErrInvalid) {
			t.Fatalf("%s: Validate = %v, want ErrInvalid", what, err)
		}
	}
	c, err := client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: answers{doc: longDescription}}})
	if err != nil {
		t.Fatal(err)
	}
	var got ieee20305.DERControl
	if err := c.Get(ctx, "/derp/1/derc/1", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	refused("Get", &got)
	v, err := c.Fetch(ctx, "/derp/1/derc/1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	refused("Fetch", v)

	item := strings.Replace(longDescription, ` xmlns="urn:ieee:std:2030.5:ns"`, "", 1)
	list := `<DERControlList xmlns="urn:ieee:std:2030.5:ns" href="/derp/1/derc" all="1" results="1">` + item + `</DERControlList>`
	c, err = client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: answers{doc: list}}})
	if err != nil {
		t.Fatal(err)
	}
	items := 0
	if err := c.Iterate(ctx, "/derp/1/derc", 0, func(it any) error {
		items++
		refused("Iterate", it)
		return nil
	}); err != nil || items != 1 {
		t.Fatalf("Iterate: %d items, %v", items, err)
	}

	handled := 0
	h, err := client.NewNotificationHandler(func(_ context.Context, n *ieee20305.Notification) error {
		handled++
		if n.Resource == nil {
			t.Fatal("the notification has no resource")
		}
		refused("the NotificationHandler", n.Resource.Value)
		return nil
	}, client.NotificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	note := `<Notification xmlns="urn:ieee:std:2030.5:ns" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<subscribedResource>/derp/1/derc/1</subscribedResource>` +
		strings.Replace(strings.Replace(item, "<DERControl ", `<Resource xsi:type="DERControl" `, 1), "</DERControl>", "</Resource>", 1) +
		`<status>0</status><subscriptionURI>/edev/1/sub/1</subscriptionURI></Notification>`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/ntfy", strings.NewReader(note)))
	if w.Code != http.StatusCreated || handled != 1 {
		t.Fatalf("the notification: status %d, handled %d times", w.Code, handled)
	}
}
