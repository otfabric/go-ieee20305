// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// tests emits generated_gen_test.go: tests of the generated code of the
// package that use the example builders of GenerateExamples. They check that
// every example validates and survives an XML round trip, that every facet
// and occurrence constraint is enforced, and the schema defaults, document
// elements and polymorphic elements.
func (g *gen) tests() ([]byte, error) {
	g.exQual = "examples."
	defer func() { g.exQual = "" }()
	pkg := g.cfg.Package
	verr := g.basePkgPrefix() + "ValidationError"
	var b strings.Builder

	// Examples: valid, and an XML round trip.
	b.WriteString("type exampleCase struct {\n\tname  string\n\tbuild func(d int) interface{ Validate() error }\n\tfresh func() any\n}\n\n")
	b.WriteString("var exampleCases = []exampleCase{\n")
	for _, q := range g.ownedSorted(true) {
		n := GoName(q.Local)
		fmt.Fprintf(&b, "\t{%q, func(d int) interface{ Validate() error } { return examples.%s(d) }, func() any { return new(%s.%s) }},\n", n, n, pkg, n)
	}
	b.WriteString("}\n\n")
	b.WriteString(strings.ReplaceAll(`func TestGeneratedExamples(t *testing.T) {
	for _, c := range exampleCases {
		for _, d := range []int{0, 2} {
			v := c.build(d)
			if err := v.Validate(); err != nil {
				t.Errorf("%s(%d): %v", c.name, d, err)
				continue
			}
			out, err := xml.Marshal(v)
			if err != nil {
				t.Errorf("%s(%d): marshal: %v", c.name, d, err)
				continue
			}
			back := c.fresh()
			if err := xml.Unmarshal(out, back); err != nil {
				t.Errorf("%s(%d): unmarshal: %v\n%s", c.name, d, err, out)
				continue
			}
			if !reflect.DeepEqual(v, back) {
				t.Errorf("%s(%d): the round trip changed the value\n%s", c.name, d, out)
			}
			clone := PKG.Clone(v)
			if !reflect.DeepEqual(v, clone) {
				t.Errorf("%s(%d): Clone differs", c.name, d)
			}
			if after, err := xml.Marshal(clone); err != nil || string(after) != string(out) {
				t.Errorf("%s(%d): the clone encodes differently", c.name, d)
			}
			if s, ok := clone.(interface{ Strip2018() }); ok {
				s.Strip2018()
				if err := clone.(interface{ Validate() error }).Validate(); err != nil {
					t.Errorf("%s(%d): invalid after Strip2018: %v", c.name, d, err)
				}
				if _, err := xml.Marshal(clone); err != nil {
					t.Errorf("%s(%d): Strip2018: %v", c.name, d, err)
				}
				if !reflect.DeepEqual(v, c.build(d)) {
					t.Errorf("%s(%d): Strip2018 of the clone changed the original", c.name, d)
				}
			}
		}
	}
	if PKG.Clone(struct{}{}) != nil {
		t.Error("Clone of a foreign value")
	}
}

`, "PKG", pkg))

	// Invalid values: every Invalid builder, and every field that can be
	// made invalid on its own.
	b.WriteString("var invalidCases = []struct {\n\tname string\n\terr  func() error\n}{\n")
	for _, q := range g.ownedSorted(true) {
		n := GoName(q.Local)
		if !g.invalidable(ref{q: q}) {
			continue
		}
		fmt.Fprintf(&b, "\t{%q, func() error { return examples.Invalid%s().Validate() }},\n", n, n)
		p, err := g.plan(q)
		if err != nil {
			return nil, err
		}
		if p.embed != "" {
			if er := g.embedRef(q); g.invalidable(er) {
				fmt.Fprintf(&b, "\t{%q, func() error { v := examples.%s(2); v.%s = *%s; return v.Validate() }},\n", n+"."+p.embedName, n, p.embedName, er.call(true, ""))
			}
		}
		for _, f := range p.fields {
			var expr string
			if f.any {
				if f.min > 0 {
					expr = "nil"
				}
			} else {
				expr = g.invalidField(f)
			}
			if expr == "" {
				continue
			}
			fmt.Fprintf(&b, "\t{%q, func() error { const d = 2; v := examples.%s(d); v.%s = %s; return v.Validate() }},\n", n+"."+f.goName, n, f.goName, expr)
		}
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, `func TestGeneratedInvalid(t *testing.T) {
	for _, c := range invalidCases {
		var ve *%s
		if err := c.err(); !errors.As(err, &ve) {
			t.Errorf("%%s: Validate = %%v, want a ValidationError", c.name, err)
		}
	}
}

`, verr)

	// Scalars: facets, and the text methods of hexadecimal types.
	b.WriteString("var scalarCases = []struct {\n\tname    string\n\tvalid   interface{ Validate() error }\n\tinvalid []interface{ Validate() error }\n}{\n")
	var hexTypes []xsd.QName
	for _, q := range g.ownedSorted(false) {
		if g.isHex(q) {
			hexTypes = append(hexTypes, q)
		}
		if !g.hasValidate(q) {
			continue
		}
		s, err := g.sample(q)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\t{%q, %s, []interface{ Validate() error }{%s}},\n", GoName(q.Local), s.valid, strings.Join(s.invalid, ", "))
	}
	b.WriteString("}\n\n")
	b.WriteString(`func TestGeneratedScalars(t *testing.T) {
	for _, c := range scalarCases {
		if err := c.valid.Validate(); err != nil {
			t.Errorf("%s: valid value: %v", c.name, err)
		}
		for i, v := range c.invalid {
			if v.Validate() == nil {
				t.Errorf("%s: invalid value %d (%v) accepted", c.name, i, v)
			}
		}
	}
}

`)
	if len(hexTypes) > 0 {
		b.WriteString("func TestGeneratedHexText(t *testing.T) {\n")
		for _, q := range hexTypes {
			n := GoName(q.Local)
			fmt.Fprintf(&b, `	{
		v := %[1]s.%[2]s{0xAB, 0x01}
		text, err := v.MarshalText()
		if err != nil || string(text) != "AB01" || v.String() != "AB01" {
			t.Errorf("%[2]s: MarshalText = %%q, %%v; String = %%q", text, err, v.String())
		}
		var back %[1]s.%[2]s
		if err := back.UnmarshalText([]byte(" ab01 ")); err != nil || !reflect.DeepEqual(back, v) {
			t.Errorf("%[2]s: UnmarshalText = %%v, %%v", back, err)
		}
		if err := back.UnmarshalText([]byte("zz")); err == nil {
			t.Errorf("%[2]s: UnmarshalText accepted zz")
		}
	}
`, pkg, n)
		}
		b.WriteString("}\n\n")
	}

	// Schema defaults.
	var defaults strings.Builder
	for _, q := range g.ownedSorted(true) {
		p, err := g.plan(q)
		if err != nil {
			return nil, err
		}
		for _, f := range p.fields {
			if f.defVal == nil || f.kind != kindPointer {
				continue
			}
			bi, err := g.set.Builtin(f.typ)
			if err != nil {
				return nil, err
			}
			t := strings.TrimPrefix(f.goType, "*")
			if !strings.Contains(t, ".") && !isGoBuiltin(t) {
				t = pkg + "." + t
			}
			lit, err := literal(bi.Local, *f.defVal, t)
			if err != nil {
				return nil, err
			}
			if bi.Local != "hexBinary" && !isGoBuiltin(t) {
				lit = t + "(" + lit + ")"
			}
			n := GoName(q.Local)
			fmt.Fprintf(&defaults, `	{
		var none *%[1]s.%[2]s
		if got := none.Get%[3]s(); !reflect.DeepEqual(got, %[4]s) {
			t.Errorf("%[2]s.Get%[3]s() of nil = %%v", got)
		}
		if got := new(%[1]s.%[2]s).Get%[3]s(); !reflect.DeepEqual(got, %[4]s) {
			t.Errorf("%[2]s.Get%[3]s() of an absent value = %%v", got)
		}
		v := examples.%[2]s(1)
		if got := v.Get%[3]s(); !reflect.DeepEqual(got, *v.%[3]s) {
			t.Errorf("%[2]s.Get%[3]s() = %%v, want %%v", got, *v.%[3]s)
		}
	}
`, pkg, n, f.goName, lit)
		}
	}
	if defaults.Len() > 0 {
		b.WriteString("func TestGeneratedDefaults(t *testing.T) {\n")
		b.WriteString(defaults.String())
		b.WriteString("}\n\n")
	}

	// Document elements.
	roots, err := g.roots()
	if err != nil {
		return nil, err
	}
	b.WriteString("func TestGeneratedElements(t *testing.T) {\n\tfor _, c := range []struct {\n\t\tname xml.Name\n\t\tok   func(any) bool\n\t}{\n")
	for _, el := range roots {
		fmt.Fprintf(&b, "\t\t{xml.Name{Space: %q, Local: %q}, func(v any) bool { _, ok := v.(*%s.%s); return ok }},\n", el.Name.Space, el.Name.Local, pkg, GoName(el.Type.Local))
	}
	fmt.Fprintf(&b, `	} {
		v := %[1]s.NewElement(c.name)
		if !c.ok(v) {
			t.Errorf("NewElement(%%v) = %%T", c.name, v)
			continue
		}
		if got, ok := %[1]s.ElementName(v); !ok || got != c.name {
			t.Errorf("ElementName(%%T) = %%v, %%v", v, got, ok)
		}
		if got, ok := %[1]s.ElementName(reflect.ValueOf(v).Elem().Interface()); !ok || got != c.name {
			t.Errorf("ElementName of a %%T value = %%v, %%v", v, got, ok)
		}
	}
	if v := %[1]s.NewElement(xml.Name{Space: "urn:none", Local: "None"}); v != nil {
		t.Errorf("NewElement of an unknown name = %%T", v)
	}
	if _, ok := %[1]s.ElementName(struct{}{}); ok {
		t.Error("ElementName of a non-document type")
	}
}

`, pkg)

	// Polymorphic elements.
	for _, q := range sortedQNames(g.wrappers) {
		w := wrapperName(q)
		fmt.Fprintf(&b, "func TestGenerated%s(t *testing.T) {\n\tfor _, v := range []any{\n", w)
		for _, d := range g.derived(q) {
			fmt.Fprintf(&b, "\t\texamples.%s(1),\n", GoName(d.Local))
		}
		fmt.Fprintf(&b, `	} {
		w := %[1]s.%[2]s{Value: v}
		out, err := xml.Marshal(w)
		if err != nil {
			t.Errorf("%%T: %%v", v, err)
			continue
		}
		var back %[1]s.%[2]s
		if err := xml.Unmarshal(out, &back); err != nil || !reflect.DeepEqual(back.Value, v) {
			t.Errorf("%%T: round trip: %%v\n%%s", v, err, out)
		}
		if err := w.Validate(); err != nil {
			t.Errorf("%%T: Validate: %%v", v, err)
		}
	}
	if _, err := xml.Marshal(%[1]s.%[2]s{Value: struct{}{}}); err == nil {
		t.Error("a value of another type is encoded")
	}
	var back %[1]s.%[2]s
	if err := xml.Unmarshal([]byte(%[3]s), &back); err == nil {
		t.Error("an unknown xsi:type is decoded")
	}
	if err := (%[1]s.%[2]s{}).Validate(); err != nil {
		t.Errorf("Validate of no value: %%v", err)
	}
`, pkg, w, strconv.Quote(`<x xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="NoSuchType"></x>`))
		if g.invalidable(ref{q: q}) {
			fmt.Fprintf(&b, "\tif err := (%s.%s{Value: examples.Invalid%s()}).Validate(); err == nil {\n\t\tt.Error(\"Validate of an invalid value\")\n\t}\n", pkg, w, GoName(q.Local))
		}
		b.WriteString("}\n\n")
	}
	if err := g.genericTests(&b); err != nil {
		return nil, err
	}

	body := b.String()
	if strings.Contains(body, "generatedPtr(") {
		body += "func generatedPtr[T any](v T) *T { return &v }\n"
	}
	var src strings.Builder
	src.WriteString(g.cfg.Header)
	fmt.Fprintf(&src, "\npackage %s_test\n\nimport (\n", pkg)
	imports := []string{strconv.Quote("encoding/xml"), strconv.Quote("errors"), strconv.Quote("reflect"), strconv.Quote("testing"),
		pkg + " " + strconv.Quote(g.cfg.ImportPath), "examples " + strconv.Quote(g.cfg.Examples.ImportPath)}
	if g.cfg.Base != nil {
		if strings.Contains(body, g.cfg.Base.Package+".") {
			imports = append(imports, g.cfg.Base.Package+" "+strconv.Quote(g.cfg.Base.ImportPath))
		}
		if strings.Contains(body, "baseexamples.") {
			if g.cfg.Base.Examples == nil {
				return nil, fmt.Errorf("the base package has no examples")
			}
			imports = append(imports, "baseexamples "+strconv.Quote(g.cfg.Base.Examples.ImportPath))
		}
	}
	sort.Strings(imports)
	src.WriteString(strings.Join(imports, "\n"))
	src.WriteString("\n)\n\n")
	src.WriteString(body)
	return formatSource(src.String())
}

// genericTests emits the tests of generic_gen.go: list methods, links and
// derivations.
func (g *gen) genericTests(b *strings.Builder) error {
	pkg := g.cfg.Package
	lister := g.basePkgPrefix() + "Lister"
	var lists []xsd.QName
	for _, q := range g.ownedSorted(true) {
		if _, ok := g.listItems(q); ok {
			lists = append(lists, q)
		}
	}
	if len(lists) > 0 {
		b.WriteString("func TestGeneratedLists(t *testing.T) {\n\tfor _, l := range []" + lister + "{\n")
		for _, q := range lists {
			fmt.Fprintf(b, "\t\texamples.%s(2),\n", GoName(q.Local))
		}
		b.WriteString(`	} {
		items := l.ListItems()
		if len(items) == 0 {
			t.Errorf("%T: no items", l)
			continue
		}
		if err := l.SetListItems(append(items, l.NewListItem())); err != nil || len(l.ListItems()) != len(items)+1 {
			t.Errorf("%T: SetListItems: %v", l, err)
		}
		first := reflect.ValueOf(items[0]).Elem().Interface()
		if err := l.SetListItems([]any{first}); err != nil || len(l.ListItems()) != 1 {
			t.Errorf("%T: SetListItems of a value: %v", l, err)
		}
		if err := l.SetListItems([]any{struct{}{}}); err == nil {
			t.Errorf("%T: SetListItems accepted a foreign item", l)
		}
		if l.ListItemElement() == "" {
			t.Errorf("%T: no item element", l)
		}
		l.SetListCounts(7, 1)
		v := reflect.ValueOf(l).Elem()
		if v.FieldByName("All").Uint() != 7 || v.FieldByName("Results").Uint() != 1 {
			t.Errorf("%T: SetListCounts", l)
		}
	}
}

`)
	}
	memo := map[xsd.QName]int{}
	var linkers []xsd.QName
	for _, q := range g.ownedSorted(true) {
		if g.hasLinks(q, memo) {
			linkers = append(linkers, q)
		}
	}
	if len(linkers) > 0 {
		ref := g.basePkgPrefix() + "LinkRef"
		b.WriteString("func TestGeneratedLinks(t *testing.T) {\n\tfor _, v := range []interface{ AppendLinks([]" + ref + ") []" + ref + " }{\n")
		for _, q := range linkers {
			fmt.Fprintf(b, "\t\texamples.%s(3),\n", GoName(q.Local))
		}
		b.WriteString(`	} {
		for _, l := range v.AppendLinks(nil) {
			generic := l.Type == "Link" || l.Type == "ListLink"
			if l.Element == "" || l.Type == "" || l.Href == "" || (l.Resource == "") != generic {
				t.Errorf("%T: incomplete link %+v", v, l)
			}
		}
	}
}

`)
	}
	roots, err := g.roots()
	if err != nil {
		return err
	}
	b.WriteString("func TestGeneratedDerivations(t *testing.T) {\n\tfor _, e := range []string{\n")
	for _, el := range roots {
		fmt.Fprintf(b, "\t\t%q,\n", el.Name.Local)
	}
	fmt.Fprintf(b, `	} {
		if !%[1]s.Derives(e, e) {
			t.Errorf("Derives(%%q, %%q) = false", e, e)
		}
	}
	if %[1]s.Derives("NoSuchElement", "NoSuchElement") {
		t.Error("Derives of an unknown element")
	}
	if _, ok := %[1]s.TypeBase("NoSuchType"); ok {
		t.Error("TypeBase of an unknown type")
	}
}
`, pkg)
	return nil
}
