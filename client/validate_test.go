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

// TestClientValidates: the client checks what the server sends against the
// facets of the schema, as SECURITY.md says: Get, Fetch and Iterate fail
// with ErrProtocol wrapping the *ValidationError (so ErrInvalid too), and a
// NotificationHandler answers 400 without calling its function.
func TestClientValidates(t *testing.T) {
	ctx := context.Background()
	refused := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ieee20305.ErrProtocol) || !errors.Is(err, ieee20305.ErrInvalid) {
			t.Fatalf("%s: %v, want ErrProtocol and ErrInvalid", what, err)
		}
		var ve *ieee20305.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("%s: %v wraps no *ValidationError", what, err)
		}
	}
	c, err := client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: answers{doc: longDescription}}})
	if err != nil {
		t.Fatal(err)
	}
	var got ieee20305.DERControl
	refused("Get", c.Get(ctx, "/derp/1/derc/1", &got))
	_, err = c.Fetch(ctx, "/derp/1/derc/1")
	refused("Fetch", err)

	item := strings.Replace(longDescription, ` xmlns="urn:ieee:std:2030.5:ns"`, "", 1)
	list := `<DERControlList xmlns="urn:ieee:std:2030.5:ns" href="/derp/1/derc" all="1" results="1">` + item + `</DERControlList>`
	c, err = client.New(client.Config{Server: "https://server.example", HTTPClient: &http.Client{Transport: answers{doc: list}}})
	if err != nil {
		t.Fatal(err)
	}
	items := 0
	refused("Iterate", c.Iterate(ctx, "/derp/1/derc", 0, func(any) error {
		items++
		return nil
	}))
	if items != 0 {
		t.Fatalf("Iterate handed over %d items", items)
	}

	handled := 0
	h, err := client.NewNotificationHandler(func(context.Context, *ieee20305.Notification) error {
		handled++
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
	if w.Code != http.StatusBadRequest || handled != 0 {
		t.Fatalf("the notification: status %d, handled %d times", w.Code, handled)
	}
}
