// SPDX-License-Identifier: MIT

package emit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func loadExtension(t *testing.T) *xsd.ResolvedExtension {
	t.Helper()
	basePath := filepath.Join("..", "xsd", "testdata", "minimal.xsd")
	extPath := filepath.Join("..", "xsd", "testdata", "extension.xsd")
	eb, err := xsd.ParseExtension(basePath, extPath)
	if err != nil {
		t.Fatalf("ParseExtension: %v", err)
	}
	re, err := xsd.ResolveExtension(eb)
	if err != nil {
		t.Fatalf("ResolveExtension: %v", err)
	}
	return re
}

func TestExtensionEmitTypes(t *testing.T) {
	re := loadExtension(t)

	outDir := t.TempDir()
	e := emit.NewExtension(re, outDir, "csipaus",
		"github.com/otfabric/go-ieee20305", "ieee20305",
		"extension.xsd", "extabc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "types_gen.go"))
	if err != nil {
		t.Fatalf("read types_gen.go: %v", err)
	}
	content := string(data)

	// Should import the base package.
	if !strings.Contains(content, `ieee20305 "github.com/otfabric/go-ieee20305"`) {
		t.Error("missing base package import")
	}

	// ConnectionPoint should embed the base EndDevice with qualified name.
	if !strings.Contains(content, "ieee20305.EndDevice") {
		t.Error("ConnectionPoint should embed ieee20305.EndDevice")
	}

	// Extension-only fields should use base types with qualified names.
	if !strings.Contains(content, "*ieee20305.UInt32") {
		t.Error("exportLimitW should use *ieee20305.UInt32")
	}

	// Extension-only types should use local type names.
	if !strings.Contains(content, "*NMI") {
		t.Error("nmi should use *NMI (local extension type)")
	}

	// Should NOT contain base types like List, EndDevice as standalone structs.
	if strings.Contains(content, "type EndDevice struct") {
		t.Error("should not re-emit base type EndDevice")
	}
	if strings.Contains(content, "type List struct") {
		t.Error("should not re-emit base type List")
	}
}

func TestExtensionEmitPrimitives(t *testing.T) {
	re := loadExtension(t)

	outDir := t.TempDir()
	e := emit.NewExtension(re, outDir, "csipaus",
		"github.com/otfabric/go-ieee20305", "ieee20305",
		"extension.xsd", "extabc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "primitives_gen.go"))
	if err != nil {
		t.Fatalf("read primitives_gen.go: %v", err)
	}
	content := string(data)

	// NMI should be a primitive type alias.
	if !strings.Contains(content, "type NMI string") {
		t.Error("missing NMI type alias")
	}

	// ExportLimitMode should be an enum.
	if !strings.Contains(content, "type ExportLimitMode string") {
		t.Error("missing ExportLimitMode enum type")
	}
	if !strings.Contains(content, `ExportLimitModeSTATIC`) {
		t.Error("missing STATIC enum const")
	}

	// Should NOT contain base simple types.
	if strings.Contains(content, "type UInt32") {
		t.Error("should not re-emit base type UInt32")
	}
}

func TestExtensionNoXMLWhenNoHexTypes(t *testing.T) {
	re := loadExtension(t)

	outDir := t.TempDir()
	e := emit.NewExtension(re, outDir, "csipaus",
		"github.com/otfabric/go-ieee20305", "ieee20305",
		"extension.xsd", "extabc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// The extension has no hex binary types, so xml_gen.go should not be emitted.
	if _, err := os.Stat(filepath.Join(outDir, "xml_gen.go")); err == nil {
		t.Error("xml_gen.go should not be emitted when extension has no hex types")
	}
}
