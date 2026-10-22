// SPDX-License-Identifier: MIT

package xsd_test

import (
	"path/filepath"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func TestParseFile(t *testing.T) {
	path := filepath.Join("testdata", "minimal.xsd")
	s, err := xsd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if s.Namespace != "urn:ieee:std:2030.5:ns" {
		t.Errorf("Namespace = %q, want %q", s.Namespace, "urn:ieee:std:2030.5:ns")
	}
	if s.Version != "2023" {
		t.Errorf("Version = %q, want %q", s.Version, "2023")
	}

	if got := len(s.SimpleTypes); got != 4 {
		t.Errorf("len(SimpleTypes) = %d, want 4", got)
	}
	if got := len(s.ComplexTypes); got != 5 {
		t.Errorf("len(ComplexTypes) = %d, want 5", got)
	}
}

func TestParseSimpleTypes(t *testing.T) {
	path := filepath.Join("testdata", "minimal.xsd")
	s, err := xsd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	stMap := make(map[string]*xsd.SimpleType)
	for _, st := range s.SimpleTypes {
		stMap[st.Name] = st
	}

	t.Run("UInt32", func(t *testing.T) {
		st := stMap["UInt32"]
		if st == nil {
			t.Fatal("UInt32 not found")
		}
		if st.Base != "xs:unsignedInt" {
			t.Errorf("Base = %q, want %q", st.Base, "xs:unsignedInt")
		}
	})

	t.Run("HexBinary160", func(t *testing.T) {
		st := stMap["HexBinary160"]
		if st == nil {
			t.Fatal("HexBinary160 not found")
		}
		if st.Base != "xs:hexBinary" {
			t.Errorf("Base = %q, want %q", st.Base, "xs:hexBinary")
		}
		if st.MinLength != 20 || st.MaxLength != 20 {
			t.Errorf("Length = [%d, %d], want [20, 20]", st.MinLength, st.MaxLength)
		}
	})

	t.Run("TimeType", func(t *testing.T) {
		st := stMap["TimeType"]
		if st == nil {
			t.Fatal("TimeType not found")
		}
		if st.Doc != "Seconds since epoch" {
			t.Errorf("Doc = %q, want %q", st.Doc, "Seconds since epoch")
		}
	})

	t.Run("DeviceStatus_enum", func(t *testing.T) {
		st := stMap["DeviceStatus"]
		if st == nil {
			t.Fatal("DeviceStatus not found")
		}
		if !st.IsEnum() {
			t.Error("IsEnum() = false, want true")
		}
		if got := len(st.Enumerations); got != 3 {
			t.Errorf("len(Enumerations) = %d, want 3", got)
		}
	})
}

func TestParseComplexTypes(t *testing.T) {
	path := filepath.Join("testdata", "minimal.xsd")
	s, err := xsd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	ctMap := make(map[string]*xsd.ComplexType)
	for _, ct := range s.ComplexTypes {
		ctMap[ct.Name] = ct
	}

	t.Run("Resource_abstract", func(t *testing.T) {
		ct := ctMap["Resource"]
		if ct == nil {
			t.Fatal("Resource not found")
		}
		if !ct.Abstract {
			t.Error("Abstract = false, want true")
		}
		if ct.ContentKind != xsd.ContentSequence {
			t.Errorf("ContentKind = %d, want ContentSequence", ct.ContentKind)
		}
	})

	t.Run("IdentifiedObject_extension", func(t *testing.T) {
		ct := ctMap["IdentifiedObject"]
		if ct == nil {
			t.Fatal("IdentifiedObject not found")
		}
		if ct.ContentKind != xsd.ContentComplexExtend {
			t.Errorf("ContentKind = %d, want ContentComplexExtend", ct.ContentKind)
		}
		if ct.BaseType != "Resource" {
			t.Errorf("BaseType = %q, want %q", ct.BaseType, "Resource")
		}
		if got := len(ct.Sequence); got != 2 {
			t.Errorf("len(Sequence) = %d, want 2", got)
		}
	})

	t.Run("List_attributes", func(t *testing.T) {
		ct := ctMap["List"]
		if ct == nil {
			t.Fatal("List not found")
		}
		if got := len(ct.Attributes); got != 2 {
			t.Errorf("len(Attributes) = %d, want 2", got)
		}
		if ct.Attributes[0].Name != "all" {
			t.Errorf("Attributes[0].Name = %q, want %q", ct.Attributes[0].Name, "all")
		}
		if ct.Attributes[0].Use != "required" {
			t.Errorf("Attributes[0].Use = %q, want %q", ct.Attributes[0].Use, "required")
		}
	})

	t.Run("EndDevice_inheritance", func(t *testing.T) {
		ct := ctMap["EndDevice"]
		if ct == nil {
			t.Fatal("EndDevice not found")
		}
		if ct.BaseType != "IdentifiedObject" {
			t.Errorf("BaseType = %q, want %q", ct.BaseType, "IdentifiedObject")
		}
		if got := len(ct.Sequence); got != 2 {
			t.Errorf("len(Sequence) = %d, want 2 (own elements only)", got)
		}
	})

	t.Run("EndDeviceList_unbounded", func(t *testing.T) {
		ct := ctMap["EndDeviceList"]
		if ct == nil {
			t.Fatal("EndDeviceList not found")
		}
		if got := len(ct.Sequence); got != 1 {
			t.Fatalf("len(Sequence) = %d, want 1", got)
		}
		el := ct.Sequence[0]
		if el.MaxOccurs != -1 {
			t.Errorf("MaxOccurs = %d, want -1 (unbounded)", el.MaxOccurs)
		}
		if el.MinOccurs != 0 {
			t.Errorf("MinOccurs = %d, want 0", el.MinOccurs)
		}
	})
}

func TestParseFileNotFound(t *testing.T) {
	_, err := xsd.ParseFile("testdata/nonexistent.xsd")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestParseMalformed(t *testing.T) {
	_, err := xsd.Parse([]byte("<not valid xml"))
	if err == nil {
		t.Fatal("expected error for malformed XML")
	}
}
