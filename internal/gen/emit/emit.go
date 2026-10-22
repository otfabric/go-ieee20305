// SPDX-License-Identifier: MIT

// Package emit turns a loaded XSD set into Go source files.
//
// A base package gets every type of its schema set. A profile package (such
// as CSIP-AUS, which xs:redefines base types) gets the types of its own
// namespaces, every redefined base type, and every base type that contains
// one of those, directly or through its base type; everything else refers to
// the base package.
package emit

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// Config describes the package to generate.
type Config struct {
	Package string
	// Header is the comment block written at the top of every file,
	// including the "Code generated ... DO NOT EDIT." line.
	Header string
	// ProfileVersion is the version of a profile package; empty for a base
	// package.
	ProfileVersion string
	// Base is set for a profile package.
	Base *Base
	// Polymorphic lists, per complex type, the elements whose xsi:type may
	// name any type derived from the declared type.
	Polymorphic map[xsd.QName]map[string]bool
}

// Base identifies the base package a profile package builds on.
type Base struct {
	Package    string
	ImportPath string
	// Set is the schema set of the base package: the types it defines.
	Set *xsd.Set
}

// Files maps file names to formatted Go source.
type Files map[string][]byte

type gen struct {
	cfg      Config
	set      *xsd.Set
	owned    map[xsd.QName]bool
	redef    map[xsd.QName]bool // profile: redefined types emitted here
	affected map[xsd.QName]bool // profile: base types emitted here
	goNames  map[string]xsd.QName
	refd     map[xsd.QName]bool // global elements used by reference
	wrappers map[xsd.QName]bool // declared types of polymorphic elements
}

// Generate produces the Go files of a package from set.
func Generate(set *xsd.Set, cfg Config) (Files, error) {
	g := &gen{cfg: cfg, set: set, owned: map[xsd.QName]bool{}, redef: map[xsd.QName]bool{}, affected: map[xsd.QName]bool{}, goNames: map[string]xsd.QName{}, wrappers: map[xsd.QName]bool{}}
	if err := g.ownership(); err != nil {
		return nil, err
	}
	for owner, names := range cfg.Polymorphic {
		ct, ok := set.ComplexTypes[owner]
		if !ok || !g.owned[owner] {
			return nil, fmt.Errorf("polymorphic element: %s is not a complex type of this package", owner)
		}
		for name := range names {
			found := false
			for _, p := range ct.Particles {
				if p.Element != nil && p.Element.Name.Local == name {
					found = true
					if !g.isStruct(p.Element.Type) || !g.owned[p.Element.Type] {
						return nil, fmt.Errorf("polymorphic element %s of %s must have a complex type of this package", name, owner)
					}
					g.wrappers[p.Element.Type] = true
					if prev, dup := g.goNames[wrapperName(p.Element.Type)]; dup {
						return nil, fmt.Errorf("wrapper %s collides with %s", wrapperName(p.Element.Type), prev)
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("polymorphic element: %s has no element %s", owner, name)
			}
		}
	}
	files := Files{}
	type part struct {
		name string
		fn   func() (string, error)
	}
	for _, p := range []part{
		{"scalars_gen.go", g.scalars},
		{"types_gen.go", g.types},
		{"validate_gen.go", g.validate},
		{"defaults_gen.go", g.defaults},
		{"elements_gen.go", g.elements},
	} {
		body, err := p.fn()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.name, err)
		}
		if body == "" {
			continue
		}
		src, err := g.file(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.name, err)
		}
		files[p.name] = src
	}
	return files, nil
}

// file prepends the header, package clause and imports, and formats.
func (g *gen) file(body string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(g.cfg.Header)
	fmt.Fprintf(&b, "\npackage %s\n\n", g.cfg.Package)
	imports := []string{}
	for _, imp := range []struct{ path, use string }{
		{"encoding/hex", "hex."},
		{"encoding/xml", "xml."},
		{"errors", "errors."},
		{"fmt", "fmt."},
		{"regexp", "regexp."},
		{"sort", "sort."},
		{"strconv", "strconv."},
		{"strings", "strings."},
	} {
		if strings.Contains(body, imp.use) {
			imports = append(imports, strconv.Quote(imp.path))
		}
	}
	if g.cfg.Base != nil && strings.Contains(body, g.cfg.Base.Package+".") {
		imports = append(imports, g.cfg.Base.Package+" "+strconv.Quote(g.cfg.Base.ImportPath))
	}
	if len(imports) > 0 {
		fmt.Fprintf(&b, "import (\n%s\n)\n\n", strings.Join(imports, "\n"))
	}
	b.WriteString(body)
	src, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %w\n%s", err, b.String())
	}
	return src, nil
}

// --- ownership ---

func (g *gen) ownership() error {
	s := g.set
	if g.cfg.Base == nil {
		for q := range s.SimpleTypes {
			g.owned[q] = true
		}
		for q := range s.ComplexTypes {
			g.owned[q] = true
		}
		if len(s.Redefinitions) > 0 {
			return fmt.Errorf("a base package cannot have redefinitions")
		}
	} else {
		bs := g.cfg.Base.Set
		inBase := func(q xsd.QName) bool {
			_, st := bs.SimpleTypes[q]
			_, ct := bs.ComplexTypes[q]
			return st || ct
		}
		for q := range s.SimpleTypes {
			if !inBase(q) {
				g.owned[q] = true
			}
		}
		for q := range s.ComplexTypes {
			if !inBase(q) {
				g.owned[q] = true
			}
		}
		for q := range s.Redefinitions {
			g.redef[q] = true
			g.affected[q] = true
		}
		for changed := true; changed; {
			changed = false
			for _, q := range s.ComplexTypeNames() {
				if g.affected[q] || !inBase(q) {
					continue
				}
				if g.contentAffected(s.ComplexTypes[q]) {
					g.affected[q] = true
					changed = true
				}
			}
		}
		for q := range g.affected {
			g.owned[q] = true
		}
	}
	for q := range g.owned {
		name := GoName(q.Local)
		if prev, dup := g.goNames[name]; dup {
			return fmt.Errorf("types %s and %s both map to Go name %s", prev, q, name)
		}
		g.goNames[name] = q
	}
	g.refd = map[xsd.QName]bool{}
	for _, q := range s.ComplexTypeNames() {
		ct := s.ComplexTypes[q]
		for _, p := range g.ownParticles(ct) {
			if p.Element != nil && p.Element.Ref {
				g.refd[p.Element.Name] = true
			}
		}
	}
	return nil
}

// contentAffected reports whether ct must be re-emitted in a profile
// package because its base or one of its elements is.
func (g *gen) contentAffected(ct *xsd.ComplexType) bool {
	if ct.Content == xsd.ContentComplexExtension && g.affected[ct.Base] {
		return true
	}
	for _, p := range ct.Particles {
		if p.Element != nil && g.affected[p.Element.Type] {
			return true
		}
	}
	return false
}

// ownParticles returns the particles a type contributes itself, including
// what a redefinition appends.
func (g *gen) ownParticles(ct *xsd.ComplexType) []*xsd.Particle {
	ps := ct.Particles
	if r, ok := g.set.Redefinitions[ct.Name]; ok {
		ps = append(append([]*xsd.Particle{}, ps...), r.Particles...)
	}
	return ps
}

// --- type references ---

// goType returns the Go type expression for a type reference.
func (g *gen) goType(q xsd.QName) (string, error) {
	if q.IsBuiltin() {
		t, ok := goBuiltin[q.Local]
		if !ok || q.Local == "hexBinary" {
			return "", fmt.Errorf("built-in xs:%s used directly; it needs a named type", q.Local)
		}
		return t, nil
	}
	if g.owned[q] {
		return GoName(q.Local), nil
	}
	if b := g.cfg.Base; b != nil {
		_, st := b.Set.SimpleTypes[q]
		_, ct := b.Set.ComplexTypes[q]
		if st || ct {
			return b.Package + "." + GoName(q.Local), nil
		}
	}
	return "", fmt.Errorf("type %s is defined neither here nor in the base package", q)
}

func (g *gen) basePrefix() string {
	if g.cfg.Base == nil {
		return ""
	}
	return g.cfg.Base.Package + "."
}

// isStruct reports whether q becomes a Go struct.
func (g *gen) isStruct(q xsd.QName) bool {
	ct, ok := g.set.ComplexTypes[q]
	return ok && (ct.Content != xsd.ContentSimpleExtension || len(ct.Attributes) > 0)
}

// isHex reports whether a scalar derives from xs:hexBinary.
func (g *gen) isHex(q xsd.QName) bool {
	b, err := g.set.Builtin(q)
	return err == nil && b.Local == "hexBinary"
}

// hasValidate reports whether the Go type of q has a Validate method.
func (g *gen) hasValidate(q xsd.QName) bool {
	if g.isStruct(q) {
		return true
	}
	if q.IsBuiltin() {
		return false
	}
	f := g.set.EffectiveFacets(q)
	return f.Length != nil || f.MinLength != nil || f.MaxLength != nil || f.MinInclusive != nil ||
		f.MaxInclusive != nil || f.Pattern != "" || len(g.set.Enumerations(q)) > 0
}

func (g *gen) ownedSorted(structs bool) []xsd.QName {
	var out []xsd.QName
	for q := range g.owned {
		if _, isCT := g.set.ComplexTypes[q]; (isCT && g.isStruct(q)) == structs {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return GoName(out[i].Local) < GoName(out[j].Local) })
	return out
}

func describe(q xsd.QName) string {
	return fmt.Sprintf("the XML Schema type %s in namespace %s", q.Local, q.Space)
}

// --- scalars ---

func (g *gen) scalars() (string, error) {
	var b strings.Builder
	for _, q := range g.ownedSorted(false) {
		name := GoName(q.Local)
		bi, err := g.set.Builtin(q)
		if err != nil {
			return "", err
		}
		under := goBuiltin[bi.Local]
		fmt.Fprintf(&b, "// %s is generated from %s.\ntype %s %s\n\n", name, describe(q), name, under)
		if g.isHex(q) {
			fmt.Fprintf(&b, `// MarshalText encodes v as upper-case hexadecimal.
func (v %[1]s) MarshalText() ([]byte, error) {
	return []byte(strings.ToUpper(hex.EncodeToString(v))), nil
}

// UnmarshalText decodes hexadecimal text.
func (v *%[1]s) UnmarshalText(text []byte) error {
	b, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		return err
	}
	*v = b
	return nil
}

// String returns v as upper-case hexadecimal.
func (v %[1]s) String() string {
	return strings.ToUpper(hex.EncodeToString(v))
}

`, name)
		}
	}
	return b.String(), nil
}

// --- structs ---

type field struct {
	goName  string
	goType  string
	tag     string
	xmlName string // for validation paths
	typ     xsd.QName
	kind    int // kindValue, kindPointer, kindSlice
	min     int
	max     int
	any     bool
	defVal  *string
}

const (
	kindValue = iota
	kindPointer
	kindSlice
)

// structPlan is the Go layout of a struct type.
type structPlan struct {
	embed     string // embedded type expression, if any
	embedName string // its field name
	fields    []field
}

func (g *gen) plan(q xsd.QName) (*structPlan, error) {
	ct := g.set.ComplexTypes[q]
	p := &structPlan{}
	var particles []*xsd.Particle
	var attrs []*xsd.Attribute
	switch {
	case ct.Content == xsd.ContentSimpleExtension:
		t, err := g.goType(ct.Base)
		if err != nil {
			return nil, err
		}
		p.fields = append(p.fields, field{goName: "Value", goType: t, tag: `xml:",chardata"`, typ: ct.Base, kind: kindValue})
		attrs = ct.Attributes
	case g.redef[q] && !g.contentAffected(ct):
		p.embed = g.cfg.Base.Package + "." + GoName(q.Local)
		r := g.set.Redefinitions[q]
		particles, attrs = r.Particles, r.Attributes
	default:
		if ct.Content == xsd.ContentComplexExtension {
			t, err := g.goType(ct.Base)
			if err != nil {
				return nil, err
			}
			p.embed = t
		}
		particles = ct.Particles
		attrs = ct.Attributes
		if r, ok := g.set.Redefinitions[q]; ok && g.redef[q] {
			particles = append(append([]*xsd.Particle{}, particles...), r.Particles...)
			attrs = append(append([]*xsd.Attribute{}, attrs...), r.Attributes...)
		}
	}
	if p.embed != "" {
		p.embedName = p.embed[strings.LastIndexByte(p.embed, '.')+1:]
	}
	for _, pa := range particles {
		if pa.Any != nil {
			f := field{goName: "Extensions", goType: "[]" + g.basePrefix() + "ExtensionElement", tag: `xml:",any"`, xmlName: "xs:any", kind: kindSlice, min: pa.MinOccurs, max: pa.MaxOccurs, any: true}
			p.fields = append(p.fields, f)
			continue
		}
		el := pa.Element
		t, err := g.goType(el.Type)
		if err != nil {
			return nil, fmt.Errorf("%s: element %s: %w", q, el.Name.Local, err)
		}
		if g.cfg.Polymorphic[q][el.Name.Local] {
			t = wrapperName(el.Type)
		}
		f := field{goName: GoName(el.Name.Local), xmlName: el.Name.Local, typ: el.Type, min: pa.MinOccurs, max: pa.MaxOccurs, defVal: el.Default}
		tagName := el.Name.Local
		if el.Name.Space != "" {
			tagName = el.Name.Space + " " + el.Name.Local
		}
		switch {
		case pa.MaxOccurs == xsd.Unbounded || pa.MaxOccurs > 1:
			f.kind, f.goType, f.tag = kindSlice, "[]"+t, fmt.Sprintf(`xml:"%s,omitempty"`, tagName)
		case pa.MinOccurs == 0:
			f.kind, f.goType, f.tag = kindPointer, "*"+t, fmt.Sprintf(`xml:"%s,omitempty"`, tagName)
		default:
			f.kind, f.goType, f.tag = kindValue, t, fmt.Sprintf(`xml:"%s"`, tagName)
		}
		p.fields = append(p.fields, f)
	}
	for _, a := range attrs {
		t, err := g.goType(a.Type)
		if err != nil {
			return nil, fmt.Errorf("%s: attribute %s: %w", q, a.Name, err)
		}
		f := field{goName: GoName(a.Name), xmlName: "@" + a.Name, typ: a.Type, defVal: a.Default}
		if a.Required {
			f.kind, f.goType, f.tag = kindValue, t, fmt.Sprintf(`xml:"%s,attr"`, a.Name)
		} else {
			f.kind, f.goType, f.tag = kindPointer, "*"+t, fmt.Sprintf(`xml:"%s,attr,omitempty"`, a.Name)
		}
		p.fields = append(p.fields, f)
	}
	return p, nil
}

// promotedNames returns the Go names of the fields reachable through the
// embedded base chain of q, as emitted. The names of embedded types deeper
// than the first are not included: an own field may shadow them.
func (g *gen) promotedNames(q xsd.QName) (map[string]bool, error) {
	names := map[string]bool{}
	ct := g.set.ComplexTypes[q]
	var walk func(xsd.QName, bool) error
	walk = func(t xsd.QName, withRedef bool) error {
		c := g.set.ComplexTypes[t]
		if c.Content == xsd.ContentComplexExtension {
			if err := walk(c.Base, g.owned[c.Base]); err != nil {
				return err
			}
		}
		ps := c.Particles
		as := c.Attributes
		if r, ok := g.set.Redefinitions[t]; ok && withRedef && g.redef[t] {
			ps = append(append([]*xsd.Particle{}, ps...), r.Particles...)
			as = append(append([]*xsd.Attribute{}, as...), r.Attributes...)
		}
		for _, p := range ps {
			if p.Any != nil {
				if names["Extensions"] {
					return fmt.Errorf("%s: more than one xs:any along the base chain", q)
				}
				names["Extensions"] = true
			} else {
				names[GoName(p.Element.Name.Local)] = true
			}
		}
		for _, a := range as {
			names[GoName(a.Name)] = true
		}
		return nil
	}
	switch {
	case g.redef[q] && !g.contentAffected(ct):
		// Embeds the base package's version of q itself.
		if err := walk(q, false); err != nil {
			return nil, err
		}
		names[GoName(q.Local)] = true
	case ct.Content == xsd.ContentComplexExtension:
		if err := walk(ct.Base, g.owned[ct.Base]); err != nil {
			return nil, err
		}
		names[GoName(ct.Base.Local)] = true
	}
	return names, nil
}

var reservedMethods = map[string]bool{"Validate": true, "String": true, "MarshalText": true, "UnmarshalText": true}

func (g *gen) types() (string, error) {
	var b strings.Builder
	for _, q := range g.ownedSorted(true) {
		p, err := g.plan(q)
		if err != nil {
			return "", err
		}
		inherited, err := g.promotedNames(q)
		if err != nil {
			return "", err
		}
		own := map[string]bool{}
		for _, f := range p.fields {
			if own[f.goName] || inherited[f.goName] || reservedMethods[f.goName] || strings.HasPrefix(f.goName, "Get") {
				return "", fmt.Errorf("%s: Go field name %s collides with another field or method", q, f.goName)
			}
			own[f.goName] = true
		}
		name := GoName(q.Local)
		fmt.Fprintf(&b, "// %s is generated from %s.\ntype %s struct {\n", name, describe(q), name)
		if p.embed != "" {
			fmt.Fprintf(&b, "\t%s\n", p.embed)
		}
		for _, f := range p.fields {
			fmt.Fprintf(&b, "\t%s %s `%s`\n", f.goName, f.goType, f.tag)
		}
		b.WriteString("}\n\n")
	}
	for _, q := range sortedQNames(g.wrappers) {
		g.wrapper(&b, q)
	}
	return b.String(), nil
}

func wrapperName(q xsd.QName) string { return "Any" + GoName(q.Local) }

func sortedQNames(m map[xsd.QName]bool) []xsd.QName {
	out := make([]xsd.QName, 0, len(m))
	for q := range m {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// derived returns q and every struct type of this package derived from it,
// sorted by Go name.
func (g *gen) derived(q xsd.QName) []xsd.QName {
	var out []xsd.QName
	for _, s := range g.ownedSorted(true) {
		for cur := s; ; {
			if cur == q {
				out = append(out, s)
				break
			}
			ct := g.set.ComplexTypes[cur]
			if ct.Content != xsd.ContentComplexExtension {
				break
			}
			cur = ct.Base
		}
	}
	return out
}

// wrapper emits the Go type of a polymorphic element of declared type q.
func (g *gen) wrapper(b *strings.Builder, q xsd.QName) {
	name, base := wrapperName(q), GoName(q.Local)
	fmt.Fprintf(b, `// %[1]s is a %[2]s element whose xsi:type may name any type derived from
// %[2]s. Value is a pointer to the generated type, such as *%[2]s.
type %[1]s struct {
	Value any
}

// MarshalXML encodes Value with an xsi:type attribute naming its type.
func (a %[1]s) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	name, ok := xsiType%[1]s(a.Value)
	if !ok {
		return fmt.Errorf("%%T is not derived from %[2]s", a.Value)
	}
	if name != %[2]q {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Space: "http://www.w3.org/2001/XMLSchema-instance", Local: "type"}, Value: name})
	}
	return e.EncodeElement(a.Value, start)
}

// UnmarshalXML decodes the element into a new value of the type its
// xsi:type names, or of %[2]s without one. A namespace prefix in the
// xsi:type value is ignored.
func (a *%[1]s) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	typ := %[2]q
	for _, at := range start.Attr {
		if at.Name.Space == "http://www.w3.org/2001/XMLSchema-instance" && at.Name.Local == "type" {
			typ = at.Value[strings.IndexByte(at.Value, ':')+1:]
		}
	}
	v := new%[1]s(typ)
	if v == nil {
		return fmt.Errorf("xsi:type %%q is not a type derived from %[2]s", typ)
	}
	if err := d.DecodeElement(v, &start); err != nil {
		return err
	}
	a.Value = v
	return nil
}

// Validate validates Value.
func (a %[1]s) Validate() error {
	if v, ok := a.Value.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}

func xsiType%[1]s(v any) (string, bool) {
	switch v.(type) {
`, name, base)
	types := g.derived(q)
	for _, t := range types {
		fmt.Fprintf(b, "\tcase *%[1]s, %[1]s:\n\t\treturn %[2]q, true\n", GoName(t.Local), t.Local)
	}
	fmt.Fprintf(b, "\t}\n\treturn \"\", false\n}\n\nfunc new%s(typ string) any {\n\tswitch typ {\n", name)
	for _, t := range types {
		fmt.Fprintf(b, "\tcase %q:\n\t\treturn new(%s)\n", t.Local, GoName(t.Local))
	}
	b.WriteString("\t}\n\treturn nil\n}\n\n")
}

// --- validation ---

func (g *gen) validate() (string, error) {
	var b strings.Builder
	errType := g.basePrefix() + "ValidationError"
	for _, q := range g.ownedSorted(false) {
		if !g.hasValidate(q) {
			continue
		}
		if err := g.validateScalar(&b, q, errType); err != nil {
			return "", err
		}
	}
	for _, q := range g.ownedSorted(true) {
		p, err := g.plan(q)
		if err != nil {
			return "", err
		}
		name := GoName(q.Local)
		fmt.Fprintf(&b, "// Validate reports the first constraint of the schema that v violates.\nfunc (v *%s) Validate() error {\n", name)
		if p.embed != "" {
			fmt.Fprintf(&b, "\tif err := v.%s.Validate(); err != nil {\n\t\treturn err\n\t}\n", p.embedName)
		}
		for _, f := range p.fields {
			needs := !f.any && g.hasValidate(f.typ)
			path := strconv.Quote(f.xmlName)
			switch f.kind {
			case kindValue:
				if needs {
					if f.goName == "Value" && f.xmlName == "" {
						fmt.Fprintf(&b, "\tif err := v.Value.Validate(); err != nil {\n\t\treturn err\n\t}\n")
					} else {
						fmt.Fprintf(&b, "\tif err := v.%s.Validate(); err != nil {\n\t\treturn validationField(%s, err)\n\t}\n", f.goName, path)
					}
				}
			case kindPointer:
				if needs {
					fmt.Fprintf(&b, "\tif v.%s != nil {\n\t\tif err := v.%s.Validate(); err != nil {\n\t\t\treturn validationField(%s, err)\n\t\t}\n\t}\n", f.goName, f.goName, path)
				}
			case kindSlice:
				if f.min > 0 {
					fmt.Fprintf(&b, "\tif len(v.%s) < %d {\n\t\treturn &%s{Field: %s, Reason: \"fewer than %d occurrences\"}\n\t}\n", f.goName, f.min, errType, path, f.min)
				}
				if f.max > 1 {
					fmt.Fprintf(&b, "\tif len(v.%s) > %d {\n\t\treturn &%s{Field: %s, Reason: \"more than %d occurrences\"}\n\t}\n", f.goName, f.max, errType, path, f.max)
				}
				if needs {
					fmt.Fprintf(&b, "\tfor i := range v.%s {\n\t\tif err := v.%s[i].Validate(); err != nil {\n\t\t\treturn validationField(%s+\"[\"+strconv.Itoa(i)+\"]\", err)\n\t\t}\n\t}\n", f.goName, f.goName, path)
				}
			}
		}
		b.WriteString("\treturn nil\n}\n\n")
	}
	fmt.Fprintf(&b, `// validationField prefixes the field path of a validation error.
func validationField(field string, err error) error {
	var ve *%[1]s
	if !errors.As(err, &ve) {
		return err
	}
	if ve.Field != "" {
		if strings.HasPrefix(ve.Field, "[") {
			field += ve.Field
		} else {
			field += "." + ve.Field
		}
	}
	return &%[1]s{Field: field, Reason: ve.Reason}
}
`, errType)
	return b.String(), nil
}

func (g *gen) validateScalar(b *strings.Builder, q xsd.QName, errType string) error {
	name := GoName(q.Local)
	bi, _ := g.set.Builtin(q)
	f := g.set.EffectiveFacets(q)
	fail := func(reason string) string {
		return fmt.Sprintf("return &%s{Reason: %s}", errType, strconv.Quote(reason))
	}
	if f.Pattern != "" {
		if _, err := regexp.Compile("^(?:" + f.Pattern + ")$"); err != nil {
			return fmt.Errorf("%s: pattern %q: %w", q, f.Pattern, err)
		}
		fmt.Fprintf(b, "var pattern%s = regexp.MustCompile(%s)\n\n", name, strconv.Quote("^(?:"+f.Pattern+")$"))
	}
	fmt.Fprintf(b, "// Validate reports whether v violates a facet of %s.\nfunc (v %s) Validate() error {\n", q.Local, name)
	unit := "characters"
	if g.isHex(q) {
		unit = "bytes"
	} else if bi.Local == "string" || bi.Local == "anyURI" {
		unit = "octets"
	}
	if f.Length != nil {
		fmt.Fprintf(b, "\tif len(v) != %d {\n\t\t%s\n\t}\n", *f.Length, fail(fmt.Sprintf("length is not %d %s", *f.Length, unit)))
	}
	if f.MinLength != nil {
		fmt.Fprintf(b, "\tif len(v) < %d {\n\t\t%s\n\t}\n", *f.MinLength, fail(fmt.Sprintf("shorter than %d %s", *f.MinLength, unit)))
	}
	if f.MaxLength != nil {
		fmt.Fprintf(b, "\tif len(v) > %d {\n\t\t%s\n\t}\n", *f.MaxLength, fail(fmt.Sprintf("longer than %d %s", *f.MaxLength, unit)))
	}
	for _, bound := range []struct {
		val *string
		op  string
		msg string
	}{{f.MinInclusive, "<", "less than"}, {f.MaxInclusive, ">", "greater than"}} {
		if bound.val == nil {
			continue
		}
		if _, err := strconv.ParseInt(*bound.val, 10, 64); err != nil {
			if _, err := strconv.ParseUint(*bound.val, 10, 64); err != nil {
				return fmt.Errorf("%s: bound %q is not an integer", q, *bound.val)
			}
		}
		fmt.Fprintf(b, "\tif v %s %s {\n\t\t%s\n\t}\n", bound.op, *bound.val, fail(bound.msg+" "+*bound.val))
	}
	if f.Pattern != "" {
		fmt.Fprintf(b, "\tif !pattern%s.MatchString(string(v)) {\n\t\t%s\n\t}\n", name, fail("does not match the pattern "+f.Pattern))
	}
	if enums := g.set.Enumerations(q); len(enums) > 0 {
		lits := make([]string, len(enums))
		for i, e := range enums {
			lit, err := literal(bi.Local, e, name)
			if err != nil {
				return fmt.Errorf("%s: enumeration: %w", q, err)
			}
			lits[i] = lit
		}
		fmt.Fprintf(b, "\tswitch v {\n\tcase %s:\n\tdefault:\n\t\t%s\n\t}\n", strings.Join(lits, ", "), fail("not one of the enumerated values"))
	}
	b.WriteString("\treturn nil\n}\n\n")
	return nil
}

// literal renders an XSD lexical value as a Go constant of a scalar type.
func literal(builtin, v, typeName string) (string, error) {
	v = strings.TrimSpace(v)
	switch builtin {
	case "string", "anyURI":
		return strconv.Quote(v), nil
	case "boolean":
		switch v {
		case "true", "1":
			return "true", nil
		case "false", "0":
			return "false", nil
		}
		return "", fmt.Errorf("bad boolean %q", v)
	case "hexBinary":
		raw, err := hex.DecodeString(v)
		if err != nil {
			return "", fmt.Errorf("bad hexBinary %q", v)
		}
		parts := make([]string, len(raw))
		for i, c := range raw {
			parts[i] = fmt.Sprintf("0x%02X", c)
		}
		return typeName + "{" + strings.Join(parts, ", ") + "}", nil
	case "byte", "short", "int", "long":
		bits := map[string]int{"byte": 8, "short": 16, "int": 32, "long": 64}[builtin]
		if _, err := strconv.ParseInt(v, 10, bits); err != nil {
			return "", fmt.Errorf("bad %s %q", builtin, v)
		}
		return v, nil
	default:
		bits := map[string]int{"unsignedByte": 8, "unsignedShort": 16, "unsignedInt": 32, "unsignedLong": 64}[builtin]
		if _, err := strconv.ParseUint(v, 10, bits); err != nil {
			return "", fmt.Errorf("bad %s %q", builtin, v)
		}
		return v, nil
	}
}

// --- defaults ---

func (g *gen) defaults() (string, error) {
	var b strings.Builder
	for _, q := range g.ownedSorted(true) {
		p, err := g.plan(q)
		if err != nil {
			return "", err
		}
		for _, f := range p.fields {
			if f.defVal == nil || f.kind != kindPointer {
				continue
			}
			bi, err := g.set.Builtin(f.typ)
			if err != nil {
				return "", err
			}
			t := strings.TrimPrefix(f.goType, "*")
			lit, err := literal(bi.Local, *f.defVal, t)
			if err != nil {
				return "", fmt.Errorf("%s.%s default: %w", q, f.xmlName, err)
			}
			fmt.Fprintf(&b, "// Get%[2]s returns %[2]s, or its schema default %[4]s when it is absent.\nfunc (v *%[1]s) Get%[2]s() %[3]s {\n\tif v != nil && v.%[2]s != nil {\n\t\treturn *v.%[2]s\n\t}\n\treturn %[4]s\n}\n\n",
				GoName(q.Local), f.goName, t, lit)
		}
	}
	return b.String(), nil
}

// --- root elements ---

func (g *gen) elements() (string, error) {
	var b strings.Builder
	var roots []*xsd.Element
	for _, n := range g.set.ElementNames() {
		el := g.set.Elements[n]
		if g.refd[n] || !g.owned[el.Type] || !g.isStruct(el.Type) {
			continue
		}
		for _, a := range el.ExtraAttributes {
			if a.Name != "schemaVer" {
				return "", fmt.Errorf("element %s: extra attribute %s is not supported", n, a.Name)
			}
		}
		roots = append(roots, el)
	}
	if g.cfg.Base == nil {
		ns, version := "", ""
		for _, d := range g.set.Documents {
			if ns != "" && d.TargetNamespace != ns {
				return "", fmt.Errorf("a base package must have a single namespace")
			}
			ns, version = d.TargetNamespace, d.Version
		}
		fmt.Fprintf(&b, "// Namespace is the XML namespace of the types in this package.\nconst Namespace = %q\n\n", ns)
		fmt.Fprintf(&b, "// SchemaVersion is the version of the schema the types in this package\n// are generated from, written as the schemaVer attribute of documents.\nconst SchemaVersion = %q\n\n", version)
	} else {
		var spaces []string
		seen := map[string]bool{}
		for _, d := range g.set.Documents {
			ns := d.TargetNamespace
			if seen[ns] {
				continue
			}
			seen[ns] = true
			if baseHasNamespace(g.cfg.Base.Set, ns) {
				continue
			}
			spaces = append(spaces, ns)
		}
		if len(spaces) != 1 {
			return "", fmt.Errorf("a profile package must add exactly one namespace, found %v", spaces)
		}
		fmt.Fprintf(&b, "// Namespace is the XML namespace of the elements this profile adds.\nconst Namespace = %q\n\n", spaces[0])
		fmt.Fprintf(&b, "// ProfileVersion is the version of the profile the types in this package\n// are generated from.\nconst ProfileVersion = %q\n\n", g.cfg.ProfileVersion)
	}
	b.WriteString("// ElementName returns the name of the document element of v, which must be\n// a pointer to or a value of a document type of this package.\nfunc ElementName(v any) (xml.Name, bool) {\n\tswitch v.(type) {\n")
	for _, el := range roots {
		t := GoName(el.Type.Local)
		fmt.Fprintf(&b, "\tcase *%[1]s, %[1]s:\n\t\treturn xml.Name{Space: %[2]q, Local: %[3]q}, true\n", t, el.Name.Space, el.Name.Local)
	}
	b.WriteString("\t}\n\treturn xml.Name{}, false\n}\n\n")
	b.WriteString("// NewElement returns a pointer to a new value of the document type with the\n// given element name, or nil if this package has none.\nfunc NewElement(name xml.Name) any {\n\tswitch name {\n")
	for _, el := range roots {
		fmt.Fprintf(&b, "\tcase xml.Name{Space: %q, Local: %q}:\n\t\treturn new(%s)\n", el.Name.Space, el.Name.Local, GoName(el.Type.Local))
	}
	b.WriteString("\t}\n\treturn nil\n}\n")
	return b.String(), nil
}

func baseHasNamespace(s *xsd.Set, ns string) bool {
	for _, d := range s.Documents {
		if d.TargetNamespace == ns {
			return true
		}
	}
	return false
}
