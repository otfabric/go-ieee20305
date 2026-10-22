// SPDX-License-Identifier: MIT

package emit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/wadl"
)

func loadWADL(t *testing.T) *wadl.Application {
	t.Helper()
	app, err := wadl.ParseFile(filepath.Join("..", "wadl", "testdata", "minimal.wadl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return app
}

func TestResourceEmitTable(t *testing.T) {
	app := loadWADL(t)
	outDir := t.TempDir()
	e := emit.NewResourceEmitter(app, outDir, "ieee20305", "sep.wadl", "abc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "resources_gen.go"))
	if err != nil {
		t.Fatalf("read resources_gen.go: %v", err)
	}
	content := string(data)

	// Header
	if !strings.Contains(content, "DO NOT EDIT") {
		t.Error("missing DO NOT EDIT header")
	}
	if !strings.Contains(content, "sep.wadl") {
		t.Error("missing source filename in header")
	}

	// Resource type definitions
	if !strings.Contains(content, "type Resource struct") {
		t.Error("missing Resource struct")
	}
	if !strings.Contains(content, "type ResourceMethod struct") {
		t.Error("missing ResourceMethod struct")
	}

	// Resource table entries
	if !strings.Contains(content, `Path: "/dcap"`) {
		t.Error("missing /dcap resource")
	}
	if !strings.Contains(content, `Path: "/edev/{eDevId}"`) {
		t.Error("missing /edev/{eDevId} resource")
	}
	if !strings.Contains(content, `Path: "/edev/{eDevId}/der/{derId}"`) {
		t.Error("missing /edev/{eDevId}/der/{derId} resource")
	}

	// Methods
	if !strings.Contains(content, `Method: "GET"`) {
		t.Error("missing GET method")
	}
	if !strings.Contains(content, `Method: "POST"`) {
		t.Error("missing POST method")
	}
	if !strings.Contains(content, `Method: "DELETE"`) {
		t.Error("missing DELETE method")
	}

	// Type references
	if !strings.Contains(content, `ResponseType: "DeviceCapability"`) {
		t.Error("missing DeviceCapability response type")
	}
	if !strings.Contains(content, `ResponseType: "EndDeviceList"`) {
		t.Error("missing EndDeviceList response type")
	}

	// Lookup helper
	if !strings.Contains(content, "func ResourceByPath") {
		t.Error("missing ResourceByPath function")
	}
	if !strings.Contains(content, "resourceIndex") {
		t.Error("missing resourceIndex map")
	}
}

func TestResourceEmitSorted(t *testing.T) {
	app := loadWADL(t)
	outDir := t.TempDir()
	e := emit.NewResourceEmitter(app, outDir, "ieee20305", "sep.wadl", "abc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "resources_gen.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	// /dcap should come before /edev and /tm should come after /edev
	dcapIdx := strings.Index(content, `"/dcap"`)
	edevIdx := strings.Index(content, `"/edev"`)
	tmIdx := strings.Index(content, `"/tm"`)
	if dcapIdx > edevIdx || edevIdx > tmIdx {
		t.Error("resources not sorted by path")
	}
}

func TestResourceEmitEmpty(t *testing.T) {
	app := &wadl.Application{}
	outDir := t.TempDir()
	e := emit.NewResourceEmitter(app, outDir, "ieee20305", "sep.wadl", "abc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// No file should be emitted for empty WADL.
	if _, err := os.Stat(filepath.Join(outDir, "resources_gen.go")); err == nil {
		t.Error("resources_gen.go should not be emitted for empty WADL")
	}
}
