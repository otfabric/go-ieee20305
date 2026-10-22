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

// inline wraps a schema body in a document of namespace urn:e.
func inline(body string) string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns="urn:e" targetNamespace="urn:e" elementFormDefault="qualified" version="1">` +
		`<xs:simpleType name="U"><xs:restriction base="xs:unsignedInt"/></xs:simpleType>` + body + `</xs:schema>`
}

func e(local string) xsd.QName { return xsd.QName{Space: "urn:e", Local: local} }

func TestGenerateRejects(t *testing.T) {
	ext := func(name, base, seq string) string {
		return `<xs:complexType name="` + name + `"><xs:complexContent><xs:extension base="` + base + `"><xs:sequence>` + seq + `</xs:sequence></xs:extension></xs:complexContent></xs:complexType>`
	}
	tests := []struct {
		name string
		body string
		poly map[xsd.QName]map[string]bool
		want string
	}{
		{"go name collision", `<xs:complexType name="device"/><xs:complexType name="Device"/>`, nil, "both map to Go name"},
		{"wrapper collision", `<xs:complexType name="Item"/><xs:complexType name="AnyItem"/>` +
			`<xs:complexType name="H"><xs:sequence><xs:element name="i" type="Item"/></xs:sequence></xs:complexType>`,
			map[xsd.QName]map[string]bool{e("H"): {"i": true}}, "collides"},
		{"hexBinary used directly", `<xs:complexType name="A"><xs:sequence><xs:element name="h" type="xs:hexBinary"/></xs:sequence></xs:complexType>`, nil, "needs a named type"},
		{"hexBinary attribute", `<xs:complexType name="A"><xs:attribute name="h" type="xs:hexBinary"/></xs:complexType>`, nil, "needs a named type"},
		{"hexBinary content", `<xs:complexType name="A"><xs:simpleContent><xs:extension base="xs:hexBinary"><xs:attribute name="a" type="U"/></xs:extension></xs:simpleContent></xs:complexType>`, nil, "needs a named type"},
		{"bad pattern", `<xs:simpleType name="P"><xs:restriction base="xs:string"><xs:pattern value="["/></xs:restriction></xs:simpleType>`, nil, "pattern"},
		{"decimal bound", `<xs:simpleType name="B"><xs:restriction base="xs:int"><xs:minInclusive value="1.5"/></xs:restriction></xs:simpleType>`, nil, "not an integer"},
		{"bad enumeration", `<xs:simpleType name="N"><xs:restriction base="xs:int"><xs:enumeration value="x"/></xs:restriction></xs:simpleType>`, nil, "enumeration"},
		{"bad default", `<xs:complexType name="A"><xs:sequence><xs:element name="b" type="xs:boolean" minOccurs="0" default="maybe"/></xs:sequence></xs:complexType>`, nil, "default"},
		{"extra attribute", `<xs:complexType name="A"/><xs:element name="A"><xs:complexType><xs:complexContent><xs:extension base="A"><xs:attribute name="other" type="U"/></xs:extension></xs:complexContent></xs:complexType></xs:element>`, nil, "extra attribute other"},
		{"reserved field", `<xs:complexType name="A"><xs:sequence><xs:element name="validate" type="U"/></xs:sequence></xs:complexType>`, nil, "collides"},
		{"getter field", `<xs:complexType name="A"><xs:sequence><xs:element name="getX" type="U"/></xs:sequence></xs:complexType>`, nil, "collides"},
		{"shadowed field", `<xs:complexType name="A"><xs:sequence><xs:element name="x" type="U"/></xs:sequence></xs:complexType>` + ext("B", "A", `<xs:element name="x" type="U"/>`), nil, "collides"},
		{"two wildcards", `<xs:complexType name="Z1"><xs:sequence><xs:any/></xs:sequence></xs:complexType>` + ext("Z2", "Z1", `<xs:any/>`) + ext("A", "Z2", ``), nil, "more than one xs:any"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := loadSchema(t, inline(tt.body))
			_, err := emit.Generate(s, emit.Config{Package: "p", Header: header, Polymorphic: tt.poly})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestGenerateRejectsPackages(t *testing.T) {
	s := loadSchema(t, inline(`<xs:complexType name="A"/><xs:element name="A" type="A"/>`))
	if _, err := emit.Generate(s, emit.Config{Package: "1p", Header: header}); err == nil || !strings.Contains(err.Error(), "gofmt") {
		t.Errorf("a malformed package name: %v", err)
	}
	if _, err := emit.GenerateExamples(s, emit.Config{Package: "p", Header: header}); err == nil {
		t.Error("GenerateExamples without Examples succeeded")
	}
	if _, err := emit.GenerateExamples(s, emit.Config{Package: "p", ImportPath: "x/p", Header: header,
		Examples: &emit.Examples{Package: "1e", ImportPath: "x/e"}}); err == nil || !strings.Contains(err.Error(), "gofmt") {
		t.Errorf("a malformed examples package name: %v", err)
	}

	// A profile must add exactly one namespace.
	cfg := emit.Config{Package: "q", Header: header, Base: &emit.Base{Package: "p", ImportPath: "x/p", Set: s}}
	if _, err := emit.Generate(s, cfg); err == nil || !strings.Contains(err.Error(), "exactly one namespace") {
		t.Errorf("a profile without a namespace: %v", err)
	}

	// A base package has one namespace and no redefinitions.
	dir := t.TempDir()
	other := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:o"><xs:complexType name="O"/></xs:schema>`
	if err := os.WriteFile(filepath.Join(dir, "o.xsd"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	two := strings.Replace(inline(``), `version="1">`, `version="1"><xs:import namespace="urn:o" schemaLocation="o.xsd"/>`, 1)
	if err := os.WriteFile(filepath.Join(dir, "two.xsd"), []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := xsd.Load(filepath.Join(dir, "two.xsd"), xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emit.Generate(s2, emit.Config{Package: "p", Header: header}); err == nil || !strings.Contains(err.Error(), "single namespace") {
		t.Errorf("a base package of two namespaces: %v", err)
	}
	red := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns="urn:o" targetNamespace="urn:o"><xs:redefine schemaLocation="o.xsd">` +
		`<xs:complexType name="O"><xs:complexContent><xs:extension base="O"/></xs:complexContent></xs:complexType></xs:redefine></xs:schema>`
	if err := os.WriteFile(filepath.Join(dir, "red.xsd"), []byte(red), 0o644); err != nil {
		t.Fatal(err)
	}
	s3, err := xsd.Load(filepath.Join(dir, "red.xsd"), xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emit.Generate(s3, emit.Config{Package: "p", Header: header}); err == nil || !strings.Contains(err.Error(), "redefinitions") {
		t.Errorf("a base package with a redefinition: %v", err)
	}
}

func TestGenerateExamplesRejects(t *testing.T) {
	// No candidate matches the pattern.
	s := loadSchema(t, inline(`<xs:simpleType name="P"><xs:restriction base="xs:string"><xs:pattern value="zzz"/></xs:restriction></xs:simpleType>`+
		`<xs:complexType name="A"><xs:sequence><xs:element name="p" type="P"/></xs:sequence></xs:complexType>`))
	cfg := emit.Config{Package: "p", ImportPath: "x/p", Header: header, Examples: baseExamples}
	if _, err := emit.GenerateExamples(s, cfg); err == nil || !strings.Contains(err.Error(), "patternCandidates") {
		t.Errorf("GenerateExamples = %v", err)
	}
	if _, err := emit.Generate(s, cfg); err == nil || !strings.Contains(err.Error(), "patternCandidates") {
		t.Errorf("Generate with tests = %v", err)
	}

	// A profile whose types embed base types needs the base examples.
	bs, err := xsd.Load(fixture("base.xsd"), xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := xsd.Load(fixture("core.xsd"), xsd.Options{ElementRefs: map[xsd.QName]xsd.LocalElement{
		b("Missing"): {Name: b("Missing"), Type: b("Link")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pcfg := emit.Config{Package: "profile", ImportPath: module + "/profile", Header: header, ProfileVersion: "9.9",
		Base: &emit.Base{Package: "base", ImportPath: module, Set: bs}, Examples: profileExamples}
	if _, err := emit.GenerateExamples(ps, pcfg); err == nil || !strings.Contains(err.Error(), "no examples") {
		t.Errorf("profile examples without base examples: %v", err)
	}
}
