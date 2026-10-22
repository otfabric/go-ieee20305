// SPDX-License-Identifier: MIT

package wadl_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/wadl"
)

func TestParseFile(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("..", "testdata", "app.wadl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Resources) != 2 {
		t.Fatalf("got %d resources", len(app.Resources))
	}
	list := app.Resources[0]
	if list.ID != "DeviceList" || list.SamplePath != "/dev" || len(list.Methods) != 3 {
		t.Fatalf("DeviceList = %+v", list)
	}
	get, post, del := list.Methods[0], list.Methods[1], list.Methods[2]
	if get.Name != "GET" || get.Mode != "M" || !reflect.DeepEqual(get.QueryParams, []string{"s", "l"}) {
		t.Errorf("GET = %+v", get)
	}
	if get.ResponseElement() != "DeviceList" || get.RequestElement() != "" {
		t.Errorf("GET elements = %q %q", get.RequestElement(), get.ResponseElement())
	}
	if rep := get.Responses[0].Representations; len(rep) != 2 || rep[0].ElementSpace != "urn:test:base" || rep[1].MediaType != "application/sep-exi" {
		t.Errorf("GET representations = %+v", rep)
	}
	if post.Mode != "O" || post.RequestElement() != "Device" || post.ResponseElement() != "" ||
		!reflect.DeepEqual(post.Statuses(), []int{201}) || !reflect.DeepEqual(post.Responses[0].HeaderParams, []string{"location"}) {
		t.Errorf("POST = %+v", post)
	}
	if del.Mode != "E" || !reflect.DeepEqual(del.Statuses(), []int{400, 405}) {
		t.Errorf("DELETE = %+v", del)
	}
	if dev := app.Resources[1]; dev.SamplePath != "/dev/{id1}" || dev.Methods[1].Mode != "D" {
		t.Errorf("Device = %+v", dev)
	}
}

func TestParseErrors(t *testing.T) {
	const head = `<application xmlns="http://wadl.dev.java.net/2009/02" xmlns:wx="urn:ieee:std:2030.5:wadlExt" xmlns:t="urn:t"><resources>`
	const tail = `</resources></application>`
	tests := map[string]string{
		"not wadl":         `<application xmlns="urn:other"/>`,
		"missing id":       head + `<resource/>` + tail,
		"duplicate id":     head + `<resource id="A"/><resource id="A"/>` + tail,
		"unknown mode":     head + `<resource id="A"><method name="GET" wx:mode="X"/></resource>` + tail,
		"undeclared":       head + `<resource id="A"><method name="GET" wx:mode="M"><response><representation element="q:B"/></response></method></resource>` + tail,
		"bad status":       head + `<resource id="A"><method name="GET" wx:mode="M"><response status="ok"/></method></resource>` + tail,
		"unknown element":  head + `<resource id="A"><nope/></resource>` + tail,
		"template request": head + `<resource id="A"><method name="GET" wx:mode="M"><request><param name="x" style="template"/></request></method></resource>` + tail,
	}
	for name, doc := range tests {
		if _, err := wadl.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse succeeded", name)
		}
	}
	if _, err := wadl.Parse([]byte(strings.Repeat("<", 3))); err == nil {
		t.Error("malformed XML parsed")
	}
}
