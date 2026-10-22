// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// generic emits generic_gen.go: what servers and clients need to handle any
// resource without knowing its type.
//
//   - Clone methods: a deep copy of every struct type.
//   - AppendLinks methods: the links (types derived from Link) a value holds,
//     with the WADL resource each points to.
//   - List methods: the items of every list type, and its all/results counts.
//   - TypeBase and Derives: the derivation of types and document elements.
func (g *gen) generic() (string, error) {
	var b strings.Builder
	structs := g.ownedSorted(true)
	for _, q := range structs {
		if err := g.cloneMethod(&b, q); err != nil {
			return "", err
		}
	}
	fmt.Fprintf(&b, "// Clone returns a deep copy of v, a pointer to a struct type of this\n// package%s, or nil for any other value.\nfunc Clone(v any) any {\n\tswitch v := v.(type) {\n", g.cloneFallbackDoc())
	for _, q := range structs {
		fmt.Fprintf(&b, "\tcase *%s:\n\t\treturn v.Clone()\n", GoName(q.Local))
	}
	if g.cfg.Base != nil {
		fmt.Fprintf(&b, "\t}\n\treturn %s.Clone(v)\n}\n\n", g.cfg.Base.Package)
	} else {
		b.WriteString("\t}\n\treturn nil\n}\n\n")
	}
	for _, q := range sortedQNames(g.wrappers) {
		w := wrapperName(q)
		fmt.Fprintf(&b, "// Clone returns a deep copy of a.\nfunc (a %s) Clone() %s {\n\tswitch v := a.Value.(type) {\n", w, w)
		for _, d := range g.derived(q) {
			fmt.Fprintf(&b, "\tcase *%s:\n\t\treturn %s{Value: v.Clone()}\n", GoName(d.Local), w)
		}
		b.WriteString("\t}\n\treturn a\n}\n\n")
	}
	memo := map[xsd.QName]int{}
	for _, q := range structs {
		if !g.hasLinks(q, memo) {
			continue
		}
		if err := g.linksMethod(&b, q, memo); err != nil {
			return "", err
		}
	}
	for _, q := range structs {
		if err := g.listMethods(&b, q); err != nil {
			return "", err
		}
	}
	g.derivations(&b, structs)
	return b.String(), nil
}

func (g *gen) cloneFallbackDoc() string {
	if g.cfg.Base != nil {
		return " or of package " + g.cfg.Base.Package
	}
	return ""
}

// ancestors returns the local names of the types q derives from, nearest
// first.
func (g *gen) ancestors(q xsd.QName) []string {
	var out []string
	for cur := q; ; {
		ct := g.set.ComplexTypes[cur]
		if ct == nil || ct.Content != xsd.ContentComplexExtension {
			return out
		}
		cur = ct.Base
		out = append(out, cur.Local)
	}
}

func (g *gen) derivesFrom(q xsd.QName, base string) bool {
	if q.Local == base {
		return true
	}
	for _, a := range g.ancestors(q) {
		if a == base {
			return true
		}
	}
	return false
}

// localType returns the Go type of the element type of f as code in the
// generated package writes it.
func localType(f field) string { return strings.TrimLeft(f.goType, "[]*") }

func (g *gen) cloneMethod(b *strings.Builder, q xsd.QName) error {
	p, err := g.plan(q)
	if err != nil {
		return err
	}
	name := GoName(q.Local)
	fmt.Fprintf(b, "// Clone returns a deep copy of v, or nil.\nfunc (v *%s) Clone() *%s {\n\tif v == nil {\n\t\treturn nil\n\t}\n\tc := new(%s)\n", name, name, name)
	if p.embed != "" {
		fmt.Fprintf(b, "\tc.%s = *v.%s.Clone()\n", p.embedName, p.embedName)
	}
	for _, f := range p.fields {
		t := localType(f)
		switch {
		case f.any:
			fmt.Fprintf(b, "\tc.%[1]s = %[2]sCloneExtensions(v.%[1]s)\n", f.goName, g.basePrefix())
		case g.isWrapper(f) || g.isStruct(f.typ):
			switch f.kind {
			case kindValue:
				if g.isWrapper(f) {
					fmt.Fprintf(b, "\tc.%[1]s = v.%[1]s.Clone()\n", f.goName)
				} else {
					fmt.Fprintf(b, "\tc.%[1]s = *v.%[1]s.Clone()\n", f.goName)
				}
			case kindPointer:
				if g.isWrapper(f) {
					fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tw := v.%[1]s.Clone()\n\t\tc.%[1]s = &w\n\t}\n", f.goName)
				} else {
					fmt.Fprintf(b, "\tc.%[1]s = v.%[1]s.Clone()\n", f.goName)
				}
			default:
				deref := "*"
				if g.isWrapper(f) {
					deref = ""
				}
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tc.%[1]s = make([]%[2]s, len(v.%[1]s))\n\t\tfor i := range v.%[1]s {\n\t\t\tc.%[1]s[i] = %[3]sv.%[1]s[i].Clone()\n\t\t}\n\t}\n", f.goName, t, deref)
			}
		case g.isHex(f.typ):
			switch f.kind {
			case kindValue:
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tc.%[1]s = append(%[2]s{}, v.%[1]s...)\n\t}\n", f.goName, t)
			case kindPointer:
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tx := append(%[2]s{}, (*v.%[1]s)...)\n\t\tc.%[1]s = &x\n\t}\n", f.goName, t)
			default:
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tc.%[1]s = make([]%[2]s, len(v.%[1]s))\n\t\tfor i, x := range v.%[1]s {\n\t\t\tif x != nil {\n\t\t\t\tc.%[1]s[i] = append(%[2]s{}, x...)\n\t\t\t}\n\t\t}\n\t}\n", f.goName, t)
			}
		default:
			switch f.kind {
			case kindValue:
				fmt.Fprintf(b, "\tc.%[1]s = v.%[1]s\n", f.goName)
			case kindPointer:
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tx := *v.%[1]s\n\t\tc.%[1]s = &x\n\t}\n", f.goName)
			default:
				fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tc.%[1]s = append([]%[2]s{}, v.%[1]s...)\n\t}\n", f.goName, t)
			}
		}
	}
	b.WriteString("\treturn c\n}\n\n")
	return nil
}

// linkBase returns "Link" or "ListLink" when q is a link type, the most
// derived of the two.
func (g *gen) linkBase(q xsd.QName) string {
	switch {
	case g.derivesFrom(q, "ListLink"):
		return "ListLink"
	case g.derivesFrom(q, "Link"):
		return "Link"
	}
	return ""
}

// hasLinks reports whether a value of the struct type q can hold links,
// itself or in a nested struct (not through a polymorphic element).
func (g *gen) hasLinks(q xsd.QName, memo map[xsd.QName]int) bool {
	switch memo[q] {
	case 1:
		return true
	case 2, 3:
		return false
	}
	memo[q] = 3
	result := false
	if p, err := g.plan(q); err == nil {
		if p.embed != "" && g.embedHasLinks(q, memo) {
			result = true
		}
		for _, f := range p.fields {
			if f.any || g.isWrapper(f) || !g.isStruct(f.typ) {
				continue
			}
			if g.linkBase(f.typ) != "" || g.fieldHasLinks(f.typ, memo) {
				result = true
			}
		}
	}
	memo[q] = 2
	if result {
		memo[q] = 1
	}
	return result
}

// embedHasLinks reports whether the embedded base of the struct type q can
// hold links: a base of the base package answers for its own types.
func (g *gen) embedHasLinks(q xsd.QName, memo map[xsd.QName]int) bool {
	r := g.embedRef(q)
	if r.fromBase {
		bg := g.baseGen()
		return bg != nil && bg.hasLinks(r.q, map[xsd.QName]int{})
	}
	return g.hasLinks(r.q, memo)
}

// fieldHasLinks is hasLinks for the type of a field, which may be a type of
// the base package.
func (g *gen) fieldHasLinks(t xsd.QName, memo map[xsd.QName]int) bool {
	if !g.owned[t] && g.cfg.Base != nil {
		bg := g.baseGen()
		return bg != nil && bg.hasLinks(t, map[xsd.QName]int{})
	}
	return g.hasLinks(t, memo)
}

// linkResource is the WADL resource a link type points to: its name without
// the Link suffix; none for the generic Link and ListLink types.
func linkResource(q xsd.QName) string {
	if q.Local == "Link" || q.Local == "ListLink" {
		return ""
	}
	return strings.TrimSuffix(q.Local, "Link")
}

func (g *gen) linksMethod(b *strings.Builder, q xsd.QName, memo map[xsd.QName]int) error {
	p, err := g.plan(q)
	if err != nil {
		return err
	}
	ref := g.basePrefix() + "LinkRef"
	name := GoName(q.Local)
	fmt.Fprintf(b, "// AppendLinks appends the links of v to out, in document order, and\n// returns the extended slice.\nfunc (v *%s) AppendLinks(out []%s) []%s {\n\tif v == nil {\n\t\treturn out\n\t}\n", name, ref, ref)
	if p.embed != "" && g.embedHasLinks(q, memo) {
		fmt.Fprintf(b, "\tout = v.%s.AppendLinks(out)\n", p.embedName)
	}
	for _, f := range p.fields {
		if f.any || g.isWrapper(f) || !g.isStruct(f.typ) {
			continue
		}
		var one string
		switch g.linkBase(f.typ) {
		case "ListLink":
			one = fmt.Sprintf("%s{Element: %q, Type: %q, Resource: %q, Href: x.Href, All: x.All}", ref, f.xmlName, f.typ.Local, linkResource(f.typ))
		case "Link":
			one = fmt.Sprintf("%s{Element: %q, Type: %q, Resource: %q, Href: x.Href}", ref, f.xmlName, f.typ.Local, linkResource(f.typ))
		default:
			if !g.fieldHasLinks(f.typ, memo) {
				continue
			}
		}
		switch {
		case one == "" && f.kind == kindValue:
			fmt.Fprintf(b, "\tout = v.%s.AppendLinks(out)\n", f.goName)
		case one == "" && f.kind == kindPointer:
			fmt.Fprintf(b, "\tout = v.%s.AppendLinks(out)\n", f.goName)
		case one == "":
			fmt.Fprintf(b, "\tfor i := range v.%[1]s {\n\t\tout = v.%[1]s[i].AppendLinks(out)\n\t}\n", f.goName)
		case f.kind == kindValue:
			fmt.Fprintf(b, "\tif x := &v.%s; true {\n\t\tout = append(out, %s)\n\t}\n", f.goName, one)
		case f.kind == kindPointer:
			fmt.Fprintf(b, "\tif x := v.%s; x != nil {\n\t\tout = append(out, %s)\n\t}\n", f.goName, one)
		default:
			fmt.Fprintf(b, "\tfor i := range v.%s {\n\t\tx := &v.%s[i]\n\t\tout = append(out, %s)\n\t}\n", f.goName, f.goName, one)
		}
	}
	b.WriteString("\treturn out\n}\n\n")
	return nil
}

// listItems returns the item field of a list type: the one slice of a
// struct type among its own fields; ok is false for other types.
func (g *gen) listItems(q xsd.QName) (field, bool) {
	if !g.derivesFrom(q, "List") && !g.derivesFrom(q, "SubscribableList") {
		return field{}, false
	}
	if q.Local == "List" || q.Local == "SubscribableList" {
		return field{}, false
	}
	p, err := g.plan(q)
	if err != nil {
		return field{}, false
	}
	var items []field
	for _, f := range p.fields {
		if f.kind == kindSlice && !f.any && g.isStruct(f.typ) {
			items = append(items, f)
		}
	}
	if len(items) != 1 {
		return field{}, false
	}
	return items[0], true
}

func (g *gen) listMethods(b *strings.Builder, q xsd.QName) error {
	f, ok := g.listItems(q)
	if !ok {
		return nil
	}
	name, item := GoName(q.Local), localType(f)
	u32 := g.basePrefix() + "UInt32"
	fmt.Fprintf(b, `// ListItems returns pointers to the items of the list.
func (v *%[1]s) ListItems() []any {
	out := make([]any, len(v.%[2]s))
	for i := range v.%[2]s {
		out[i] = &v.%[2]s[i]
	}
	return out
}

// SetListItems replaces the items of the list with items, each a %[3]s
// or a pointer to one.
func (v *%[1]s) SetListItems(items []any) error {
	out := make([]%[3]s, 0, len(items))
	for i, it := range items {
		switch x := it.(type) {
		case *%[3]s:
			out = append(out, *x)
		case %[3]s:
			out = append(out, x)
		default:
			return fmt.Errorf("item %%d of %[1]s is a %%T, not a %[3]s", i, it)
		}
	}
	v.%[2]s = out
	return nil
}

// NewListItem returns a pointer to a new item of the list.
func (v *%[1]s) NewListItem() any { return new(%[3]s) }

// ListItemElement returns the element name of the items of the list.
func (v *%[1]s) ListItemElement() string { return %[4]q }

// SetListCounts sets the all and results attributes of the list.
func (v *%[1]s) SetListCounts(all, results uint32) {
	v.All = %[5]s(all)
	v.Results = %[5]s(results)
}

`, name, f.goName, item, f.xmlName, u32)
	return nil
}

// derivations emits the base of every struct type and the type of every
// document element, and Derives.
func (g *gen) derivations(b *strings.Builder, structs []xsd.QName) {
	bases := map[string]string{}
	for _, q := range structs {
		if a := g.ancestors(q); len(a) > 0 {
			bases[q.Local] = a[0]
		}
	}
	b.WriteString("// typeBases maps every struct type of this package that extends another to\n// the type it extends.\nvar typeBases = map[string]string{\n")
	for _, k := range sortedKeys(bases) {
		fmt.Fprintf(b, "\t%q: %q,\n", k, bases[k])
	}
	b.WriteString("}\n\n")
	elems := map[string]string{}
	for _, n := range g.set.ElementNames() {
		el := g.set.Elements[n]
		if g.owned[el.Type] && g.isStruct(el.Type) {
			elems[n.Local] = el.Type.Local
		}
	}
	b.WriteString("// elementTypes maps the document elements of this package to their types.\nvar elementTypes = map[string]string{\n")
	for _, k := range sortedKeys(elems) {
		fmt.Fprintf(b, "\t%q: %q,\n", k, elems[k])
	}
	b.WriteString("}\n\n")
	if g.cfg.Base == nil {
		b.WriteString(`// TypeBase returns the type the struct type named typ extends, if any.
func TypeBase(typ string) (string, bool) {
	b, ok := typeBases[typ]
	return b, ok
}

// Derives reports whether the type of the document element elem is the type
// of the document element of, or derives from it. A server accepts such a
// document where the standard expects base, such as a DERControlResponse
// posted to a list of Response.
func Derives(elem, of string) bool {
	t, ok := elementTypes[elem]
	bt, ok2 := elementTypes[of]
	if !ok || !ok2 {
		return false
	}
	for seen := 0; t != "" && seen < 64; seen++ {
		if t == bt {
			return true
		}
		t = typeBases[t]
	}
	return false
}
`)
	} else {
		fmt.Fprintf(b, `// TypeBase returns the type the struct type named typ extends, if any,
// looking in this package and then in package %[1]s.
func TypeBase(typ string) (string, bool) {
	if b, ok := typeBases[typ]; ok {
		return b, true
	}
	return %[1]s.TypeBase(typ)
}

// Derives reports whether the type of the document element elem is the type
// of the document element of, or derives from it, with the elements and
// types of this package and of package %[1]s.
func Derives(elem, of string) bool {
	t, ok := elementTypes[elem]
	if !ok {
		return %[1]s.Derives(elem, of)
	}
	bt, ok := elementTypes[of]
	if !ok {
		return false
	}
	for seen := 0; t != "" && seen < 64; seen++ {
		if t == bt {
			return true
		}
		t, _ = TypeBase(t)
	}
	return false
}
`, g.cfg.Base.Package)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
