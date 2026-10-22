// SPDX-License-Identifier: MIT

package emit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

const listNS = "urn:test:lists"

func l(local string) xsd.QName { return xsd.QName{Space: listNS, Local: local} }

// listSchema has list types, link types at every occurrence, polymorphic
// elements at every occurrence, and scalars with every kind of facet, the
// constructs base.xsd lacks.
const listSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns="urn:test:lists"
  targetNamespace="urn:test:lists" elementFormDefault="qualified" version="1.0">
  <xs:simpleType name="UInt32"><xs:restriction base="xs:unsignedInt"/></xs:simpleType>
  <xs:simpleType name="UInt8"><xs:restriction base="xs:unsignedByte"/></xs:simpleType>
  <xs:simpleType name="Hex4"><xs:restriction base="xs:hexBinary"><xs:length value="4"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="HexMin"><xs:restriction base="xs:hexBinary"><xs:minLength value="2"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Small"><xs:restriction base="xs:byte"><xs:minInclusive value="-3"/><xs:maxInclusive value="3"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Positive"><xs:restriction base="xs:short"><xs:minInclusive value="5"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Code"><xs:restriction base="xs:short"><xs:enumeration value="1"/><xs:enumeration value="2"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Name"><xs:restriction base="xs:string"><xs:length value="3"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Word"><xs:restriction base="xs:string"><xs:minLength value="2"/><xs:pattern value="[a-z.]+"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Kind"><xs:restriction base="xs:string"><xs:enumeration value="x"/><xs:enumeration value="xx"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="Flag"><xs:restriction base="xs:boolean"/></xs:simpleType>
  <xs:complexType name="Level">
    <xs:simpleContent><xs:extension base="UInt8"><xs:attribute name="unit" type="UInt8"/></xs:extension></xs:simpleContent>
  </xs:complexType>
  <xs:complexType name="Resource">
    <xs:sequence><xs:any namespace="##other" processContents="lax" minOccurs="0" maxOccurs="unbounded"/></xs:sequence>
    <xs:attribute name="href" type="xs:anyURI"/>
  </xs:complexType>
  <xs:complexType name="List">
    <xs:complexContent><xs:extension base="Resource">
      <xs:attribute name="all" use="required" type="UInt32"/>
      <xs:attribute name="results" use="required" type="UInt32"/>
    </xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="Item">
    <xs:complexContent><xs:extension base="Resource"><xs:sequence>
      <xs:element name="value" type="UInt32"/>
      <xs:element name="hex" type="Hex4" minOccurs="0"/>
      <xs:element name="hexes" type="HexMin" minOccurs="0" maxOccurs="2"/>
      <xs:element name="small" type="Small" minOccurs="0" default="1"/>
      <xs:element name="positive" type="Positive" minOccurs="0"/>
      <xs:element name="code" type="Code" minOccurs="0"/>
      <xs:element name="name" type="Name" minOccurs="0"/>
      <xs:element name="word" type="Word" minOccurs="0"/>
      <xs:element name="kind" type="Kind" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="flag" type="Flag" minOccurs="0" default="0"/>
      <xs:element name="level" type="Level" minOccurs="0"/>
      <xs:element name="ints" type="xs:int" minOccurs="0" maxOccurs="3"/>
    </xs:sequence></xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="SpecialItem">
    <xs:complexContent><xs:extension base="Item"><xs:sequence>
      <xs:element name="extra" type="UInt32" minOccurs="0"/>
    </xs:sequence></xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="ItemList">
    <xs:complexContent><xs:extension base="List"><xs:sequence>
      <xs:element name="Item" type="Item" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence></xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="Link"><xs:sequence/><xs:attribute name="href" use="required" type="xs:anyURI"/></xs:complexType>
  <xs:complexType name="ListLink">
    <xs:complexContent><xs:extension base="Link"><xs:attribute name="all" type="UInt32"/></xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="ItemListLink"><xs:complexContent><xs:extension base="ListLink"/></xs:complexContent></xs:complexType>
  <xs:complexType name="ItemLink"><xs:complexContent><xs:extension base="Link"/></xs:complexContent></xs:complexType>
  <xs:complexType name="Holder">
    <xs:sequence>
      <xs:element name="ItemListLink" type="ItemListLink"/>
      <xs:element name="OptListLink" type="ListLink" minOccurs="0"/>
      <xs:element name="ListLinks" type="ListLink" minOccurs="0" maxOccurs="2"/>
      <xs:element name="ItemLink" type="ItemLink" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="Link" type="Link"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="Outer">
    <xs:sequence>
      <xs:element name="one" type="Holder"/>
      <xs:element name="two" type="Holder" minOccurs="0"/>
      <xs:element name="many" type="Holder" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="first" type="Item"/>
      <xs:element name="maybe" type="Item" minOccurs="0"/>
      <xs:element name="some" type="Item" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence>
  </xs:complexType>
  <xs:element name="ItemList" type="ItemList"/>
  <xs:element name="Item" type="Item"/>
  <xs:element name="SpecialItem" type="SpecialItem"/>
  <xs:element name="Outer" type="Outer"/>
</xs:schema>
`

// loadSchema writes a schema document and loads it.
func loadSchema(t *testing.T, doc string) *xsd.Set {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.xsd")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := xsd.Load(path, xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// listSupport adds to support what the generated tests of list types use.
const listSupport = `
type Lister interface {
	ListItems() []any
	SetListItems(items []any) error
	NewListItem() any
	ListItemElement() string
	SetListCounts(all, results uint32)
}
`

func generateLists(t *testing.T) (pkg, ex emit.Files) {
	t.Helper()
	s := loadSchema(t, listSchema)
	// In 2018, level was a plain UInt8: Strip2018 drops the attribute of
	// its 2023 type.
	level := `<xs:complexType name="Level">
    <xs:simpleContent><xs:extension base="UInt8"><xs:attribute name="unit" type="UInt8"/></xs:extension></xs:simpleContent>
  </xs:complexType>`
	if !strings.Contains(listSchema, level) {
		t.Fatal("listSchema has no Level type")
	}
	s18 := loadSchema(t, strings.NewReplacer(`name="level" type="Level"`, `name="level" type="UInt8"`, level, "").Replace(listSchema))
	cfg := emit.Config{
		Package:       "base",
		ImportPath:    module,
		Header:        header,
		Polymorphic:   map[xsd.QName]map[string]bool{l("Outer"): {"first": true, "maybe": true, "some": true}},
		Examples:      baseExamples,
		Edition2018:   s18,
		Resources2018: []string{"ItemList"},
	}
	pkg, err := emit.Generate(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ex, err = emit.GenerateExamples(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, ex
}

func TestListsLinksAndWrappers(t *testing.T) {
	pkg, ex := generateLists(t)
	generic := string(pkg["generic_gen.go"])
	for _, want := range []string{
		"func (v *ItemList) ListItems() []any",
		"case *SpecialItem:\n\t\t\tout = append(out, x.Item)",
		`Type: "ItemListLink", Resource: "ItemList", Href: x.Href, All: x.All}`,
		`Type: "ItemLink", Resource: "Item", Href: x.Href}`,
		"out = v.Two.AppendLinks(out)",
		"c.First = v.First.Clone()",
	} {
		if !strings.Contains(generic, want) {
			t.Errorf("generic_gen.go lacks %q", want)
		}
	}
	if tests := string(pkg["generated_gen_test.go"]); !strings.Contains(tests, "func TestGeneratedLists") {
		t.Error("no tests of the list methods")
	}
	if !strings.Contains(string(ex["examples_gen.go"]), "func InvalidHolder()") {
		t.Error("Holder is invalidable with too many ListLinks")
	}

	if testing.Short() {
		t.Skip("builds a module")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte("module "+module+"\n\ngo 1.23\n"))
	write("support.go", []byte(support+listSupport))
	for name, src := range pkg {
		write(name, src)
	}
	for name, src := range ex {
		write(filepath.Join("examples", name), src)
	}
	cmd := exec.Command(goTool, "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code does not build or behave:\n%s", out)
	}
}

// TestHexBinaryEnumeration: the Validate method of a type derived from
// xs:hexBinary with enumerations compiles, though a []byte type is not
// comparable.
func TestHexBinaryEnumeration(t *testing.T) {
	s := loadSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:h" version="1">`+
		`<xs:simpleType name="HexEnum"><xs:restriction base="xs:hexBinary"><xs:enumeration value="0A"/></xs:restriction></xs:simpleType>`+
		`</xs:schema>`)
	files, err := emit.Generate(s, emit.Config{Package: "h", Header: header})
	if err != nil {
		t.Fatal(err)
	}
	if src := string(files["validate_gen.go"]); strings.Contains(src, "case HexEnum{") {
		t.Errorf("a slice cannot be a switch case:\n%s", src)
	}
}
