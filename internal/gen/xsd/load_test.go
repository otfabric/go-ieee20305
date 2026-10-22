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
	baseNS = "urn:test:base"
	extNS  = "urn:test:ext"
)

func fixture(name string) string { return filepath.Join("..", "testdata", name) }

func b(local string) xsd.QName { return xsd.QName{Space: baseNS, Local: local} }

func profileOptions() xsd.Options {
	return xsd.Options{ElementRefs: map[xsd.QName]xsd.LocalElement{
		b("Missing"): {Name: b("Missing"), Type: b("Link")},
	}}
}

func TestLoadBase(t *testing.T) {
	s, err := xsd.Load(fixture("base.xsd"), xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Documents) != 1 || s.Documents[0].Version != "1.2" || s.Documents[0].TargetNamespace != baseNS {
		t.Errorf("documents = %+v", s.Documents)
	}
	if len(s.Redefinitions) != 0 {
		t.Errorf("unexpected redefinitions")
	}

	if f := s.EffectiveFacets(b("UInt48")); f.MaxInclusive == nil || *f.MaxInclusive != "281474976710655" {
		t.Errorf("UInt48 facets = %+v", f)
	}
	if bi, err := s.Builtin(b("Flags")); err != nil || bi.Local != "hexBinary" {
		t.Errorf("Builtin(Flags) = %v, %v", bi, err)
	}
	if f := s.EffectiveFacets(b("Flags")); f.MaxLength == nil || *f.MaxLength != 2 {
		t.Errorf("Flags inherits maxLength from HexBinary16: %+v", f)
	}
	if got := s.Enumerations(b("Kind")); strings.Join(got, ",") != "a,b" {
		t.Errorf("Kind enumerations = %v", got)
	}

	if !s.IsScalar(b("Status")) || s.IsScalar(b("PercentControl")) {
		t.Error("Status is scalar simple content; PercentControl has an attribute")
	}
	pc := s.ComplexTypes[b("PercentControl")]
	if pc.Content != xsd.ContentSimpleExtension || pc.Base != b("Percent") || len(pc.Attributes) != 1 {
		t.Errorf("PercentControl not normalized to simple content: %+v", pc)
	}

	res := s.ComplexTypes[b("Resource")]
	if len(res.Particles) != 2 || res.Particles[1].Any == nil || res.Particles[1].Any.Namespace != "##other" || res.Particles[1].MaxOccurs != xsd.Unbounded {
		t.Errorf("Resource particles = %+v", res.Particles)
	}
	dev := s.ComplexTypes[b("Device")]
	if chain := s.BaseChain(dev); len(chain) != 1 || chain[0].Name != b("Resource") {
		t.Errorf("Device base chain = %v", chain)
	}
	var name, status *xsd.LocalElement
	for _, p := range dev.Particles {
		switch p.Element.Name.Local {
		case "name":
			name = p.Element
		case "status":
			status = p.Element
		case "Link":
			if p.MinOccurs != 1 || p.MaxOccurs != 3 {
				t.Errorf("Link occurs = %d..%d", p.MinOccurs, p.MaxOccurs)
			}
		}
	}
	if name == nil || name.Default == nil || *name.Default != "dev" || name.Name.Space != baseNS {
		t.Errorf("name element = %+v", name)
	}
	if status == nil || status.Default == nil || *status.Default != "0" {
		t.Errorf("status default = %+v", status)
	}
	if a := dev.Attributes[0]; a.Name != "pollRate" || a.Required || a.Default == nil || *a.Default != "900" {
		t.Errorf("pollRate = %+v", a)
	}

	el := s.Elements[b("Device")]
	if el.Type != b("Device") || len(el.ExtraAttributes) != 1 || el.ExtraAttributes[0].Name != "schemaVer" {
		t.Errorf("Device element = %+v", el)
	}
}

func TestLoadProfile(t *testing.T) {
	s, err := xsd.Load(fixture("core.xsd"), profileOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Documents) != 3 {
		t.Errorf("loaded %d documents, want core, ext and base once each", len(s.Documents))
	}
	ctl := s.Redefinitions[b("Control")]
	if ctl == nil || len(ctl.Particles) != 3 {
		t.Fatalf("Control redefinition = %+v", ctl)
	}
	want := []struct {
		name xsd.QName
		typ  xsd.QName
	}{
		{xsd.QName{Space: extNS, Local: "limitExtra"}, b("Power")},
		{xsd.QName{Space: extNS, Local: "mode"}, xsd.QName{Space: extNS, Local: "ModeType"}},
		{b("Missing"), b("Link")},
	}
	for i, w := range want {
		el := ctl.Particles[i].Element
		if el.Name != w.name || el.Type != w.typ {
			t.Errorf("particle %d = %s : %s, want %s : %s", i, el.Name, el.Type, w.name, w.typ)
		}
	}
	if link := s.Redefinitions[b("Link")]; link == nil || len(link.Attributes) != 1 || link.Attributes[0].Name != "rel" {
		t.Errorf("Link redefinition = %+v", link)
	}
	if _, ok := s.Elements[xsd.QName{Space: extNS, Local: "Meter"}]; !ok {
		t.Error("ext element Meter missing")
	}
	if len(s.Redefinitions) != 2 || s.ComplexTypes[b("Control")].Particles[0].Element.Name.Local != "limit" {
		t.Error("the original Control must be kept beside its redefinition")
	}
}

func TestLoadUnresolvedRef(t *testing.T) {
	_, err := xsd.Load(fixture("core.xsd"), xsd.Options{})
	if err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Errorf("err = %v, want an unresolved ref to Missing", err)
	}
}

func TestLoadLocations(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"core.xsd", "ext.xsd"} {
		data, err := os.ReadFile(fixture(f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := profileOptions()
	opts.Locations = map[string]string{"base.xsd": fixture("base.xsd")}
	if _, err := xsd.Load(filepath.Join(dir, "core.xsd"), opts); err != nil {
		t.Errorf("Load with a mapped schemaLocation: %v", err)
	}
}

func TestLoadRejectsUnsupported(t *testing.T) {
	const head = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">`
	tests := []struct {
		name, body, want string
	}{
		{"choice", `<xs:complexType name="A"><xs:choice><xs:element name="x" type="xs:string"/></xs:choice></xs:complexType>`, "xs:choice"},
		{"sequence in sequence", `<xs:complexType name="A"><xs:sequence><xs:sequence/></xs:sequence></xs:complexType>`, "xs:sequence in a sequence"},
		{"undeclared type", `<xs:complexType name="A"><xs:sequence><xs:element name="x" type="Nope"/></xs:sequence></xs:complexType>`, "not declared"},
		{"unsupported builtin", `<xs:complexType name="A"><xs:sequence><xs:element name="x" type="xs:dateTime"/></xs:sequence></xs:complexType>`, "xs:dateTime"},
		{"undeclared prefix", `<xs:complexType name="A"><xs:sequence><xs:element name="x" type="q:B"/></xs:sequence></xs:complexType>`, "undeclared namespace prefix"},
		{"anonymous element type", `<xs:complexType name="A"><xs:sequence><xs:element name="x"><xs:complexType/></xs:element></xs:sequence></xs:complexType>`, "anonymous type"},
		{"union", `<xs:simpleType name="U"><xs:union memberTypes="xs:string"/></xs:simpleType>`, "xs:union"},
		{"unknown facet", `<xs:simpleType name="U"><xs:restriction base="xs:int"><xs:totalDigits value="2"/></xs:restriction></xs:simpleType>`, "xs:totalDigits"},
		{"circular base", `<xs:complexType name="A"><xs:complexContent><xs:extension base="B"/></xs:complexContent></xs:complexType><xs:complexType name="B"><xs:complexContent><xs:extension base="A"/></xs:complexContent></xs:complexType>`, "circular"},
		{"complex attribute", `<xs:complexType name="A"><xs:sequence/><xs:attribute name="a" type="A"/></xs:complexType>`, "not a scalar"},
		{"duplicate", `<xs:simpleType name="U"><xs:restriction base="xs:int"/></xs:simpleType><xs:simpleType name="U"><xs:restriction base="xs:int"/></xs:simpleType>`, "declared twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.xsd")
			if err := os.WriteFile(path, []byte(head+tt.body+`</xs:schema>`), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := xsd.Load(path, xsd.Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestQName(t *testing.T) {
	q, err := xsd.ParseQName("{urn:a}B")
	if err != nil || q != (xsd.QName{Space: "urn:a", Local: "B"}) || q.String() != "{urn:a}B" {
		t.Errorf("ParseQName = %v, %v", q, err)
	}
	if q, err := xsd.ParseQName("B"); err != nil || q.Space != "" || q.String() != "B" {
		t.Errorf("ParseQName(B) = %v, %v", q, err)
	}
	for _, bad := range []string{"", "{urn:a", "{urn:a}"} {
		if _, err := xsd.ParseQName(bad); err == nil {
			t.Errorf("ParseQName(%q) succeeded", bad)
		}
	}
	if !(xsd.QName{Space: xsd.XSDNamespace, Local: "int"}).IsBuiltin() {
		t.Error("xs:int is a builtin")
	}
}
