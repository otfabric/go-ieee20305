// SPDX-License-Identifier: MIT

package xsd_test

import (
	"path/filepath"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func loadResolved(t *testing.T) *xsd.Resolved {
	t.Helper()
	path := filepath.Join("testdata", "minimal.xsd")
	s, err := xsd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	r, err := xsd.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func TestResolveLookupMaps(t *testing.T) {
	r := loadResolved(t)

	if _, ok := r.SimpleTypeMap["UInt32"]; !ok {
		t.Error("SimpleTypeMap missing UInt32")
	}
	if _, ok := r.ComplexTypeMap["EndDevice"]; !ok {
		t.Error("ComplexTypeMap missing EndDevice")
	}
}

func TestBaseChain(t *testing.T) {
	r := loadResolved(t)

	ct := r.ComplexTypeMap["EndDevice"]
	chain := r.BaseChain(ct)

	var names []string
	for _, base := range chain {
		names = append(names, base.Name)
	}

	// EndDevice → IdentifiedObject → Resource
	if len(names) != 2 {
		t.Fatalf("BaseChain = %v, want [IdentifiedObject Resource]", names)
	}
	if names[0] != "IdentifiedObject" {
		t.Errorf("BaseChain[0] = %q, want %q", names[0], "IdentifiedObject")
	}
	if names[1] != "Resource" {
		t.Errorf("BaseChain[1] = %q, want %q", names[1], "Resource")
	}
}

func TestAllSequenceElements(t *testing.T) {
	r := loadResolved(t)

	ct := r.ComplexTypeMap["EndDevice"]
	all := r.AllSequenceElements(ct)

	// Resource has no elements, IdentifiedObject has 2 (mRID, description),
	// EndDevice has 2 (sFDI, changedTime) = 4 total.
	if got := len(all); got != 4 {
		t.Errorf("len(AllSequenceElements) = %d, want 4", got)
	}

	var names []string
	for _, el := range all {
		names = append(names, el.Name)
	}
	want := []string{"mRID", "description", "sFDI", "changedTime"}
	for i, n := range want {
		if i >= len(names) || names[i] != n {
			t.Errorf("AllSequenceElements[%d] = %q, want %q (full: %v)", i, names[i], n, names)
			break
		}
	}
}

func TestAllAttributes(t *testing.T) {
	r := loadResolved(t)

	ct := r.ComplexTypeMap["List"]
	attrs := r.AllAttributes(ct)

	if got := len(attrs); got != 2 {
		t.Errorf("len(AllAttributes) = %d, want 2", got)
	}
}

func TestResolveBrokenRef(t *testing.T) {
	s := &xsd.Schema{
		ComplexTypes: []*xsd.ComplexType{
			{
				Name:        "Broken",
				ContentKind: xsd.ContentComplexExtend,
				BaseType:    "NonExistent",
			},
		},
	}
	_, err := xsd.Resolve(s)
	if err == nil {
		t.Fatal("expected error for unresolved base type")
	}
}

func TestIsBuiltinXSD(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"xs:string", true},
		{"xs:unsignedInt", true},
		{"xsd:boolean", true},
		{"UInt32", false},
		{"Resource", false},
		{"", false},
		{"x", false},
	}
	for _, tt := range tests {
		if got := xsd.IsBuiltinXSD(tt.name); got != tt.want {
			t.Errorf("IsBuiltinXSD(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
