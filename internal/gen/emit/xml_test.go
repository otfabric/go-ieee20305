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

func TestEmitXMLGen(t *testing.T) {
	r := loadTestSchema(t)

	outDir := t.TempDir()
	e := emit.New(r, outDir, "ieee20305", "test.xsd", "abc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "xml_gen.go"))
	if err != nil {
		t.Fatalf("read xml_gen.go: %v", err)
	}
	content := string(data)

	// HexBinary160 is a hex binary type in the minimal schema.
	t.Run("HexBinary160_marshal", func(t *testing.T) {
		if !strings.Contains(content, "func (v HexBinary160) MarshalXML(") {
			t.Error("missing MarshalXML for HexBinary160")
		}
		if !strings.Contains(content, "func (v *HexBinary160) UnmarshalXML(") {
			t.Error("missing UnmarshalXML for HexBinary160")
		}
		if !strings.Contains(content, "func (v HexBinary160) MarshalXMLAttr(") {
			t.Error("missing MarshalXMLAttr for HexBinary160")
		}
		if !strings.Contains(content, "func (v HexBinary160) String() string") {
			t.Error("missing String for HexBinary160")
		}
	})

	// UInt32 and TimeType are integer named types in the minimal schema.
	for _, typeName := range []string{"UInt32", "TimeType"} {
		t.Run(typeName+"_marshal", func(t *testing.T) {
			if !strings.Contains(content, "func (v "+typeName+") MarshalXML(") {
				t.Errorf("missing MarshalXML for %s", typeName)
			}
			if !strings.Contains(content, "func (v *"+typeName+") UnmarshalXML(") {
				t.Errorf("missing UnmarshalXML for %s", typeName)
			}
		})
	}

	// DeviceStatus is an enum — should NOT get marshal methods.
	t.Run("DeviceStatus_no_marshal", func(t *testing.T) {
		if strings.Contains(content, "func (v DeviceStatus) MarshalXML(") {
			t.Error("unexpected MarshalXML for enum type DeviceStatus")
		}
	})
}

func TestEmitPrimitivesFixedArrays(t *testing.T) {
	r := loadTestSchema(t)

	outDir := t.TempDir()
	e := emit.New(r, outDir, "ieee20305", "test.xsd", "abc123")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "primitives_gen.go"))
	if err != nil {
		t.Fatalf("read primitives_gen.go: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "type HexBinary160 [20]byte") {
		t.Error("primitives_gen.go missing \"type HexBinary160 [20]byte\"")
	}
}

func TestEmitXMLSkippedWhenNoTypes(t *testing.T) {
	s := &xsd.Schema{
		Namespace: "urn:test",
		ComplexTypes: []*xsd.ComplexType{
			{Name: "Simple", ContentKind: xsd.ContentSequence},
		},
	}
	r, err := xsd.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	outDir := t.TempDir()
	e := emit.New(r, outDir, "test", "test.xsd", "abc")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "xml_gen.go")); err == nil {
		t.Error("xml_gen.go should not be generated when there are no hex/int types")
	}
}
