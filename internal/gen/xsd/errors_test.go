// SPDX-License-Identifier: MIT

package xsd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

const (
	schemaHead = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">`
	schemaTail = `</xs:schema>`
)

// writeSchemas writes the documents (name to content) to a temporary
// directory and returns the path of s.xsd.
func writeSchemas(t *testing.T, docs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range docs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "s.xsd")
}

// schema wraps a body in the standard schema element.
func schema(body string) string { return schemaHead + body + schemaTail }

// seq is a complex type A with the given sequence items.
func seq(items string) string {
	return `<xs:complexType name="A"><xs:sequence>` + items + `</xs:sequence></xs:complexType>`
}

func TestLoadErrors(t *testing.T) {
	// base is a document to include, import or redefine.
	base := schema(`<xs:complexType name="B"><xs:sequence/></xs:complexType>` +
		`<xs:complexType name="S"><xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent></xs:complexType>`)
	tests := []struct {
		name string
		docs map[string]string
		want string
	}{
		// documents
		{"malformed", map[string]string{"s.xsd": `<xs:schema`}, "s.xsd"},
		{"not a schema", map[string]string{"s.xsd": `<other/>`}, "not xs:schema"},
		{"top-level group", map[string]string{"s.xsd": schema(`<xs:group name="g"/>`)}, "xs:group"},
		{"qualified attributes", map[string]string{"s.xsd": `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" attributeFormDefault="qualified"/>`}, "attributeFormDefault"},
		{"missing include", map[string]string{"s.xsd": schema(`<xs:include schemaLocation="none.xsd"/>`)}, "none.xsd"},
		{"import without location", map[string]string{"s.xsd": schema(`<xs:import namespace="urn:b"/>`)}, "without schemaLocation"},
		{"missing import", map[string]string{"s.xsd": schema(`<xs:import namespace="urn:b" schemaLocation="none.xsd"/>`)}, "none.xsd"},
		{"missing redefine", map[string]string{"s.xsd": schema(`<xs:redefine schemaLocation="none.xsd"/>`)}, "none.xsd"},
		{"redefine simple type", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd"><xs:simpleType name="U"/></xs:redefine>`)}, "xs:simpleType"},
		{"redefine as sequence", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd"><xs:complexType name="B"><xs:sequence/></xs:complexType></xs:redefine>`)}, "must extend the original"},
		{"redefine malformed", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd"><xs:complexType/></xs:redefine>`)}, "without a name"},
		{"redefined twice", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd">` +
			`<xs:complexType name="B"><xs:complexContent><xs:extension base="B"/></xs:complexContent></xs:complexType>` +
			`<xs:complexType name="B"><xs:complexContent><xs:extension base="B"/></xs:complexContent></xs:complexType></xs:redefine>`)}, "redefined twice"},
		{"redefine undeclared", map[string]string{"b.xsd": schema(``), "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd">` +
			`<xs:complexType name="X"><xs:complexContent><xs:extension base="X"/></xs:complexContent></xs:complexType></xs:redefine>`)}, "undeclared type"},
		{"redefine simple content", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd">` +
			`<xs:complexType name="S"><xs:complexContent><xs:extension base="S"/></xs:complexContent></xs:complexType></xs:redefine>`)}, "simple-content"},
		{"redefine bad attribute", map[string]string{"b.xsd": base, "s.xsd": schema(`<xs:redefine schemaLocation="b.xsd">` +
			`<xs:complexType name="B"><xs:complexContent><xs:extension base="B"><xs:attribute name="a" type="Nope"/></xs:extension></xs:complexContent></xs:complexType></xs:redefine>`)}, "not declared"},
		{"duplicate complex type", map[string]string{"s.xsd": schema(seq(``) + seq(``))}, "declared twice"},
		{"duplicate element", map[string]string{"s.xsd": schema(`<xs:element name="e" type="xs:int"/><xs:element name="e" type="xs:int"/>`)}, "declared twice"},

		// simple types
		{"anonymous simple type", map[string]string{"s.xsd": schema(`<xs:simpleType><xs:restriction base="xs:int"/></xs:simpleType>`)}, "anonymous xs:simpleType"},
		{"list", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:list itemType="xs:int"/></xs:simpleType>`)}, "xs:list"},
		{"no restriction", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:annotation/></xs:simpleType>`)}, "only xs:restriction"},
		{"restriction prefix", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="q:int"/></xs:simpleType>`)}, "undeclared namespace prefix"},
		{"bad length", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="xs:string"><xs:length value="x"/></xs:restriction></xs:simpleType>`)}, "bad length facet"},
		{"negative minLength", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="xs:string"><xs:minLength value="-1"/></xs:restriction></xs:simpleType>`)}, "bad length facet"},
		{"bad maxLength", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="xs:string"><xs:maxLength value="many"/></xs:restriction></xs:simpleType>`)}, "bad length facet"},
		{"two patterns", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="xs:string"><xs:pattern value="a"/><xs:pattern value="b"/></xs:restriction></xs:simpleType>`)}, "more than one pattern"},
		{"undeclared simple base", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="Nope"/></xs:simpleType>`)}, "not declared"},
		{"circular simple types", map[string]string{"s.xsd": schema(`<xs:simpleType name="U"><xs:restriction base="V"/></xs:simpleType><xs:simpleType name="V"><xs:restriction base="U"/></xs:simpleType>`)}, "circular simple type"},
		{"simple base with elements", map[string]string{"s.xsd": schema(seq(``) + `<xs:simpleType name="U"><xs:restriction base="A"/></xs:simpleType>`)}, "element content"},

		// complex types
		{"unnamed complex type", map[string]string{"s.xsd": schema(`<xs:complexType/>`)}, "without a name"},
		{"mixed", map[string]string{"s.xsd": schema(`<xs:complexType name="A" mixed="true"/>`)}, "mixed and abstract"},
		{"abstract", map[string]string{"s.xsd": schema(`<xs:complexType name="A" abstract="true"/>`)}, "mixed and abstract"},
		{"all", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:all/></xs:complexType>`)}, "xs:all"},
		{"complex restriction", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent><xs:restriction base="B"/></xs:complexContent></xs:complexType>`)}, "xs:restriction"},
		{"empty complex content", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent/></xs:complexType>`)}, "only complexContent/extension"},
		{"extension choice", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent><xs:extension base="B"><xs:choice/></xs:extension></xs:complexContent></xs:complexType>`)}, "xs:choice"},
		{"extension prefix", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent><xs:extension base="q:B"/></xs:complexContent></xs:complexType>`)}, "undeclared namespace prefix"},
		{"extension of a builtin", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent><xs:extension base="xs:string"/></xs:complexContent></xs:complexType>`)}, "not a complex type"},
		{"simple restriction", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:restriction base="B"/></xs:simpleContent></xs:complexType>`)}, "xs:restriction"},
		{"empty simple content", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent/></xs:complexType>`)}, "only simpleContent/extension"},
		{"simple extension choice", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:extension base="xs:int"><xs:choice/></xs:extension></xs:simpleContent></xs:complexType>`)}, "xs:choice"},
		{"simple extension sequence", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:extension base="xs:int"><xs:sequence/></xs:extension></xs:simpleContent></xs:complexType>`)}, "simpleContent with a sequence"},
		{"simple extension prefix", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:extension base="q:int"/></xs:simpleContent></xs:complexType>`)}, "undeclared namespace prefix"},
		{"simple content of complex content", map[string]string{"s.xsd": schema(seq(``) + `<xs:complexType name="C"><xs:simpleContent><xs:extension base="A"/></xs:simpleContent></xs:complexType>`)}, "simple content extending complex content"},
		{"simple content with attributes", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:extension base="xs:int"><xs:attribute name="a" type="xs:int"/></xs:extension></xs:simpleContent></xs:complexType>` +
			`<xs:complexType name="C"><xs:simpleContent><xs:extension base="A"/></xs:simpleContent></xs:complexType>`)}, "extending simple content with attributes"},
		{"simple content of undeclared", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:simpleContent><xs:extension base="Nope"/></xs:simpleContent></xs:complexType>`)}, "not declared"},
		{"elements added to simple content", map[string]string{"s.xsd": schema(`<xs:complexType name="S"><xs:simpleContent><xs:extension base="xs:int"/></xs:simpleContent></xs:complexType>` +
			`<xs:complexType name="A"><xs:complexContent><xs:extension base="S"><xs:sequence><xs:element name="x" type="xs:int"/></xs:sequence></xs:extension></xs:complexContent></xs:complexType>`)}, "elements added to simple content"},
		{"complex content of simple content", map[string]string{"s.xsd": schema(`<xs:complexType name="C1"><xs:simpleContent><xs:extension base="xs:int"/></xs:simpleContent></xs:complexType>` +
			`<xs:complexType name="B1"><xs:complexContent><xs:extension base="C1"/></xs:complexContent></xs:complexType>` +
			`<xs:complexType name="A1"><xs:complexContent><xs:extension base="B1"/></xs:complexContent></xs:complexType>`)}, "complex content extending simple content"},

		// sequences
		{"sequence occurs", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:sequence minOccurs="0"/></xs:complexType>`)}, "occurrence constraints"},
		{"bad minOccurs", map[string]string{"s.xsd": schema(seq(`<xs:element name="x" type="xs:int" minOccurs="x"/>`))}, "bad occurrence"},
		{"bad maxOccurs", map[string]string{"s.xsd": schema(seq(`<xs:element name="x" type="xs:int" maxOccurs="-1"/>`))}, "bad occurrence"},
		{"fixed element", map[string]string{"s.xsd": schema(seq(`<xs:element name="x" type="xs:int" fixed="1"/>`))}, "fixed and nillable"},
		{"nillable element", map[string]string{"s.xsd": schema(seq(`<xs:element name="x" type="xs:int" nillable="true"/>`))}, "fixed and nillable"},
		{"ref with name", map[string]string{"s.xsd": schema(seq(`<xs:element ref="e" name="x"/>`))}, "element ref with name"},
		{"ref prefix", map[string]string{"s.xsd": schema(seq(`<xs:element ref="q:e"/>`))}, "undeclared namespace prefix"},
		{"element without type", map[string]string{"s.xsd": schema(seq(`<xs:element name="x"/>`))}, "needs a name and a type"},
		{"anonymous simple element", map[string]string{"s.xsd": schema(seq(`<xs:element name="x"><xs:simpleType/></xs:element>`))}, "anonymous type"},
		{"any namespace", map[string]string{"s.xsd": schema(seq(`<xs:any namespace="urn:x"/>`))}, "xs:any namespace"},
		{"malformed item", map[string]string{"s.xsd": schema(seq(`<xs:element name="x"><a></xs:element>`))}, "s.xsd"},
		{"undeclared element type", map[string]string{"s.xsd": schema(seq(`<xs:element name="x" type="Nope"/>`))}, "not declared"},

		// attributes
		{"attribute ref", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:attribute ref="a"/></xs:complexType>`)}, "only named attributes"},
		{"attribute prefix", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:attribute name="a" type="q:int"/></xs:complexType>`)}, "undeclared namespace prefix"},
		{"attribute use", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:attribute name="a" type="xs:int" use="prohibited"/></xs:complexType>`)}, "use=\"prohibited\""},
		{"attribute type", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:attribute name="a" type="Nope"/></xs:complexType>`)}, "not declared"},
		{"malformed attribute", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:attribute name="a" type="xs:int"><b></xs:attribute></xs:complexType>`)}, "s.xsd"},
		{"bad complex attribute", map[string]string{"s.xsd": schema(`<xs:complexType name="A"><xs:complexContent><xs:extension base="A"><xs:attribute ref="a"/></xs:extension></xs:complexContent></xs:complexType>`)}, "only named attributes"},

		// global elements
		{"element ref at top", map[string]string{"s.xsd": schema(`<xs:element ref="e"/>`)}, "global element without a name"},
		{"element type prefix", map[string]string{"s.xsd": schema(`<xs:element name="e" type="q:int"/>`)}, "undeclared namespace prefix"},
		{"element both types", map[string]string{"s.xsd": schema(`<xs:element name="e" type="xs:int"><xs:complexType/></xs:element>`)}, "exactly one of"},
		{"element anonymous error", map[string]string{"s.xsd": schema(`<xs:element name="e"><xs:complexType mixed="true"/></xs:element>`)}, "mixed and abstract"},
		{"element anonymous sequence", map[string]string{"s.xsd": schema(`<xs:element name="e"><xs:complexType><xs:sequence/></xs:complexType></xs:element>`)}, "anonymous type may only extend"},
		{"element undeclared type", map[string]string{"s.xsd": schema(`<xs:element name="e" type="Nope"/>`)}, "not declared"},
		{"element extra attribute", map[string]string{"s.xsd": schema(seq(``) + `<xs:element name="e"><xs:complexType><xs:complexContent><xs:extension base="A"><xs:attribute name="a" type="Nope"/></xs:extension></xs:complexContent></xs:complexType></xs:element>`)}, "not declared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := xsd.Load(writeSchemas(t, tt.docs), xsd.Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := xsd.Load(filepath.Join(t.TempDir(), "none.xsd"), xsd.Options{}); err == nil {
		t.Error("Load of a missing file succeeded")
	}
}

func TestLoadAccepts(t *testing.T) {
	base := schema(`<xs:complexType name="B"><xs:sequence/></xs:complexType>`)
	main := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">` +
		`<xs:include schemaLocation="b.xsd"/><xs:include schemaLocation="b.xsd"/>` +
		`<xs:redefine schemaLocation="b.xsd"><xs:annotation/><xs:complexType name="B"><xs:complexContent><xs:extension base="t:B">` +
		`<xs:sequence><xs:element name="r" type="xs:int"/></xs:sequence></xs:extension></xs:complexContent></xs:complexType></xs:redefine>` +
		`<xs:simpleType name="U"><xs:restriction base="xs:string"><xs:length value="2"/><xs:minLength value="1"/><xs:pattern value="[a-z]+"/></xs:restriction></xs:simpleType>` +
		`<xs:simpleType name="V"><xs:restriction base="t:U"><xs:maxLength value="2"/><xs:minInclusive value="a"/><xs:maxInclusive value="z"/></xs:restriction></xs:simpleType>` +
		`<xs:complexType name="A"><xs:sequence><xs:element name="u" type="t:U" default=""/><xs:any processContents="lax"/><xs:any namespace="##targetNamespace"/></xs:sequence>` +
		`<xs:attribute name="a" type="xs:int" use="required"/><xs:attribute name="o" type="xs:int" use="optional" default=""/><xs:anyAttribute/></xs:complexType>` +
		`<xs:element name="e"><xs:complexType><xs:complexContent><xs:extension base="t:A"><xs:attribute name="x" type="xs:int"/></xs:extension></xs:complexContent></xs:complexType></xs:element>` +
		`</xs:schema>`
	s, err := xsd.Load(writeSchemas(t, map[string]string{"b.xsd": base, "s.xsd": main}), xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Documents) != 2 {
		t.Errorf("documents = %d, want each file loaded once", len(s.Documents))
	}
	red := s.Redefinitions[xsd.QName{Space: "urn:t", Local: "B"}]
	if red == nil || len(red.Particles) != 1 {
		t.Errorf("redefinition = %+v", red)
	}
	a := s.ComplexTypes[xsd.QName{Space: "urn:t", Local: "A"}]
	if !a.AnyAttribute || len(a.Particles) != 3 || a.Particles[0].Element.Name.Space != "" ||
		a.Particles[0].Element.Default == nil || *a.Particles[0].Element.Default != "" {
		t.Errorf("A = %+v", a)
	}
	if w := a.Particles[1].Any; w.Namespace != "##any" || w.ProcessContents != "lax" {
		t.Errorf("wildcard = %+v", w)
	}
	if w := a.Particles[2].Any; w.Namespace != "##targetNamespace" || w.ProcessContents != "strict" {
		t.Errorf("wildcard = %+v", w)
	}
	if !a.Attributes[0].Required || a.Attributes[1].Default == nil {
		t.Errorf("attributes = %+v %+v", a.Attributes[0], a.Attributes[1])
	}
	v := xsd.QName{Space: "urn:t", Local: "V"}
	f := s.EffectiveFacets(v)
	if f.Length == nil || *f.Length != 2 || f.MinLength == nil || f.MaxLength == nil || f.MinInclusive == nil || f.MaxInclusive == nil || f.Pattern != "[a-z]+" {
		t.Errorf("facets of V = %+v", f)
	}
	if s.Enumerations(v) != nil {
		t.Error("V has no enumerations")
	}
	if !s.SimpleBase(xsd.QName{Space: "urn:t", Local: "nope"}).IsZero() {
		t.Error("SimpleBase of an unknown type")
	}
	if _, err := s.Builtin(xsd.QName{Space: "urn:t", Local: "A"}); err == nil {
		t.Error("Builtin of a complex type succeeded")
	}
	if names := s.ElementNames(); len(names) != 1 {
		t.Errorf("elements = %v", names)
	}
}
