// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// editions emits editions_gen.go: Strip2018 on every struct type, which
// removes what IEEE 2030.5-2018 does not define, and the resources of the
// 2018 WADL. The generated types follow the 2023 schema; a document of them
// with nothing but what 2018 defines is a 2018 document.
//
// Generation fails on a difference stripping cannot bridge: an element 2023
// requires that 2018 lacks, or an element whose 2023 type does not derive
// from its 2018 type.
func (g *gen) editions() (string, error) {
	s18 := g.cfg.Edition2018
	if s18 == nil {
		return "", nil
	}
	ns18 := ""
	for _, d := range s18.Documents {
		ns18 = d.TargetNamespace
	}
	types18 := map[string]*xsd.ComplexType{}
	for q, ct := range s18.ComplexTypes {
		types18[q.Local] = ct
	}
	simple18 := map[string]bool{}
	for q := range s18.SimpleTypes {
		simple18[q.Local] = true
	}
	// What a 2018 document has in place of each struct type: the 2018 type
	// of the same name, or, for a type that replaces a 2018 element type
	// (PerCentControlType for PerCent, say), that type; nil when only
	// elements 2018 lacks refer to the type.
	refs := map[xsd.QName]*[]flatItem{}
	items := func(ct *xsd.ComplexType) *[]flatItem {
		its := flatItems(s18, ct)
		return &its
	}
	for _, q := range g.ownedSorted(true) {
		if ct, ok := types18[q.Local]; ok {
			refs[q] = items(ct)
		}
	}
	// profileAdded reports whether a profile package adds the field f to
	// q: the base package's version of q has no such element. A profile's
	// additions are its own concern, not 2018's.
	profileAdded := func(q xsd.QName, f field) bool {
		if g.cfg.Base == nil {
			return false
		}
		ct, ok := g.cfg.Base.Set.ComplexTypes[q]
		if !ok {
			return false
		}
		for _, it := range flatItems(g.cfg.Base.Set, ct) {
			if it.name == f.xmlName {
				return false
			}
		}
		return true
	}
	var b strings.Builder
	var problems []string
	type pending struct {
		q   xsd.QName
		f   field
		t18 xsd.QName
	}
	var replacements []pending
	for _, q := range g.ownedSorted(true) {
		ref := refs[q]
		if ref == nil {
			continue
		}
		in18 := map[string]xsd.QName{}
		for _, it := range *ref {
			in18[it.name] = it.typ
		}
		fields, err := g.flatFields(q)
		if err != nil {
			return "", err
		}
		for _, f := range fields {
			if f.any || f.xmlName == "" || (f.ns != "" && f.ns != ns18) || profileAdded(q, f) {
				continue
			}
			t18, ok := in18[f.xmlName]
			if !ok {
				if f.kind == kindValue {
					problems = append(problems, fmt.Sprintf("%s.%s: required in 2023, absent in 2018", q.Local, f.xmlName))
				}
				continue
			}
			if t18.Local != f.typ.Local {
				bothSimple := simple18[t18.Local] && simple18[f.typ.Local]
				if !g.derivesFromAny(f.typ, t18.Local) && !bothSimple {
					problems = append(problems, fmt.Sprintf("%s.%s: 2023 type %s does not derive from 2018 type %s", q.Local, f.xmlName, f.typ.Local, t18.Local))
					continue
				}
				if g.isStruct(f.typ) && g.owned[f.typ] && refs[f.typ] == nil {
					replacements = append(replacements, pending{q, f, t18})
				}
			}
		}
	}
	for _, r := range replacements {
		if ct, ok := types18[r.t18.Local]; ok {
			refs[r.f.typ] = items(ct)
		} else {
			// A simple 2018 type: only the content of the 2023 type stays.
			refs[r.f.typ] = &[]flatItem{}
		}
	}
	for _, q := range g.ownedSorted(true) {
		name := GoName(q.Local)
		fmt.Fprintf(&b, "// Strip2018 removes from v what IEEE 2030.5-2018 does not define.\nfunc (v *%s) Strip2018() {\n\tif v == nil {\n\t\treturn\n\t}\n", name)
		if ref := refs[q]; ref != nil {
			in18 := map[string]bool{}
			for _, it := range *ref {
				in18[it.name] = true
			}
			fields, err := g.flatFields(q)
			if err != nil {
				return "", err
			}
			for _, f := range fields {
				switch {
				case f.any || f.xmlName == "":
					// Extensions stay, and so does the content of a simple
					// content type.
				case f.ns != "" && f.ns != ns18, profileAdded(q, f):
					// An element of a profile namespace, such as CSIP-AUS,
					// or one a profile adds.
					if g.isStruct(f.typ) || g.isWrapper(f) {
						g.stripNested(&b, f)
					}
				case !in18[f.xmlName]:
					if f.kind != kindValue {
						fmt.Fprintf(&b, "\tv.%s = nil\n", f.goName)
					}
				case g.isStruct(f.typ) || g.isWrapper(f):
					g.stripNested(&b, f)
				}
			}
		}
		b.WriteString("}\n\n")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return "", fmt.Errorf("2018 and 2023 differ in ways a 2018 document cannot carry:\n\t%s", strings.Join(problems, "\n\t"))
	}
	if g.cfg.Base == nil {
		ids := append([]string(nil), g.cfg.Resources2018...)
		sort.Strings(ids)
		b.WriteString("// resources2018 are the resources of the IEEE 2030.5-2018 WADL.\nvar resources2018 = map[string]bool{\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "\t%q: true,\n", id)
		}
		b.WriteString("}\n\n// InEdition2018 reports whether the WADL resource id is a resource of\n// IEEE 2030.5-2018.\nfunc InEdition2018(id string) bool { return resources2018[id] }\n")
	}
	return b.String(), nil
}

// flatFields returns the fields of the struct type q and of its embedded
// bases, which Go promotes to q: the generator rejects a name that would
// shadow another along the chain.
func (g *gen) flatFields(q xsd.QName) ([]field, error) {
	p, err := g.plan(q)
	if err != nil {
		return nil, err
	}
	var out []field
	if p.embed != "" {
		ct := g.set.ComplexTypes[q]
		base := ct.Base
		if g.redef[q] && !g.contentAffected(ct) {
			// A redefinition embeds the base package's version of q.
			bg := g.baseGen()
			if bg == nil {
				return nil, fmt.Errorf("%s: no base package", q)
			}
			inner, err := bg.flatFields(q)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
		} else {
			inner, err := g.flatFields(base)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
		}
	}
	return append(out, p.fields...), nil
}

// stripNested emits the Strip2018 call on a struct or polymorphic field.
func (g *gen) stripNested(b *strings.Builder, f field) {
	if g.isWrapper(f) {
		switch f.kind {
		case kindValue:
			fmt.Fprintf(b, "\tif s, ok := v.%s.Value.(interface{ Strip2018() }); ok {\n\t\ts.Strip2018()\n\t}\n", f.goName)
		case kindPointer:
			fmt.Fprintf(b, "\tif v.%[1]s != nil {\n\t\tif s, ok := v.%[1]s.Value.(interface{ Strip2018() }); ok {\n\t\t\ts.Strip2018()\n\t\t}\n\t}\n", f.goName)
		default:
			fmt.Fprintf(b, "\tfor i := range v.%[1]s {\n\t\tif s, ok := v.%[1]s[i].Value.(interface{ Strip2018() }); ok {\n\t\t\ts.Strip2018()\n\t\t}\n\t}\n", f.goName)
		}
		return
	}
	switch f.kind {
	case kindValue, kindPointer:
		fmt.Fprintf(b, "\tv.%s.Strip2018()\n", f.goName)
	default:
		fmt.Fprintf(b, "\tfor i := range v.%[1]s {\n\t\tv.%[1]s[i].Strip2018()\n\t}\n", f.goName)
	}
}

// ancestorsAll returns the local names of the bases of q, nearest first,
// following simple content too.
func (g *gen) ancestorsAll(q xsd.QName) []string {
	var out []string
	for cur, seen := q, 0; seen < 64; seen++ {
		ct := g.set.ComplexTypes[cur]
		if ct == nil || ct.Base.IsZero() || ct.Base.IsBuiltin() {
			return out
		}
		cur = ct.Base
		out = append(out, cur.Local)
	}
	return out
}

// derivesFromAny reports whether q derives from the type named base,
// through complex or simple content.
func (g *gen) derivesFromAny(q xsd.QName, base string) bool {
	for _, a := range g.ancestorsAll(q) {
		if a == base {
			return true
		}
	}
	return false
}

type flatItem struct {
	name string
	typ  xsd.QName
}

// flatItems returns the elements and attributes (as "@name") of ct with
// those of its bases.
func flatItems(s *xsd.Set, ct *xsd.ComplexType) []flatItem {
	chain := append([]*xsd.ComplexType{ct}, s.BaseChain(ct)...)
	var out []flatItem
	for i := len(chain) - 1; i >= 0; i-- {
		for _, p := range chain[i].Particles {
			if p.Element != nil {
				out = append(out, flatItem{p.Element.Name.Local, p.Element.Type})
			}
		}
		for _, a := range chain[i].Attributes {
			out = append(out, flatItem{"@" + a.Name, a.Type})
		}
	}
	return out
}
