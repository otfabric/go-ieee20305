// SPDX-License-Identifier: MIT

package wadl_test

import (
	"path/filepath"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/wadl"
)

func TestParseFile(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(app.Resources) != 3 {
		t.Fatalf("got %d top-level resources, want 3", len(app.Resources))
	}

	dcap := app.Resources[0]
	if dcap.Path != "/dcap" {
		t.Errorf("dcap path = %q, want /dcap", dcap.Path)
	}
	if len(dcap.Methods) != 1 || dcap.Methods[0].Name != "GET" {
		t.Error("dcap should have one GET method")
	}
	if dcap.Methods[0].Response == nil || dcap.Methods[0].Response.Element != "DeviceCapability" {
		t.Error("dcap GET response should be DeviceCapability")
	}
}

func TestParseNestedResources(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	edev := app.Resources[2]
	if edev.Path != "/edev" {
		t.Fatalf("edev path = %q, want /edev", edev.Path)
	}
	if len(edev.Children) != 1 {
		t.Fatalf("edev children = %d, want 1", len(edev.Children))
	}

	instance := edev.Children[0]
	if instance.Path != "/{eDevId}" {
		t.Errorf("instance path = %q, want /{eDevId}", instance.Path)
	}
	if len(instance.Params) != 1 || instance.Params[0].Name != "eDevId" {
		t.Error("instance should have eDevId param")
	}
	if instance.Params[0].Style != "template" {
		t.Errorf("eDevId style = %q, want template", instance.Params[0].Style)
	}

	// DER sub-collection
	if len(instance.Children) != 1 {
		t.Fatalf("instance children = %d, want 1 (der)", len(instance.Children))
	}
	der := instance.Children[0]
	if der.Path != "/der" {
		t.Errorf("der path = %q, want /der", der.Path)
	}
}

func TestParseMethods(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	edev := app.Resources[2]

	// Collection: GET + POST
	if len(edev.Methods) != 2 {
		t.Fatalf("edev methods = %d, want 2", len(edev.Methods))
	}
	if edev.Methods[0].Name != "GET" {
		t.Errorf("edev method[0] = %q, want GET", edev.Methods[0].Name)
	}
	if edev.Methods[1].Name != "POST" {
		t.Errorf("edev method[1] = %q, want POST", edev.Methods[1].Name)
	}
	if edev.Methods[1].Request == nil || edev.Methods[1].Request.Element != "EndDevice" {
		t.Error("POST request should be EndDevice")
	}

	// Instance: GET + PUT + DELETE
	instance := edev.Children[0]
	if len(instance.Methods) != 3 {
		t.Fatalf("instance methods = %d, want 3", len(instance.Methods))
	}
	del := instance.Methods[2]
	if del.Name != "DELETE" {
		t.Errorf("instance method[2] = %q, want DELETE", del.Name)
	}
	if del.Request != nil || del.Response != nil {
		t.Error("DELETE should have no request or response")
	}
}

func TestFlatten(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	flat := app.Flatten()

	paths := make(map[string]bool)
	for _, fr := range flat {
		paths[fr.Path] = true
	}

	want := []string{
		"/dcap",
		"/tm",
		"/edev",
		"/edev/{eDevId}",
		"/edev/{eDevId}/der",
		"/edev/{eDevId}/der/{derId}",
	}
	for _, w := range want {
		if !paths[w] {
			t.Errorf("missing flattened path %q", w)
		}
	}
	if len(flat) != len(want) {
		t.Errorf("got %d flat resources, want %d", len(flat), len(want))
	}
}

func TestParseMediaType(t *testing.T) {
	app, err := wadl.ParseFile(filepath.Join("testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	resp := app.Resources[0].Methods[0].Response
	if resp.MediaType != "application/sep+xml" {
		t.Errorf("mediaType = %q, want application/sep+xml", resp.MediaType)
	}
}

func TestParseInvalidXML(t *testing.T) {
	_, err := wadl.Parse([]byte("not xml"))
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}
