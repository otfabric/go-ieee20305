// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// Examples configures the example builders of a package and the tests
// generated from them.
type Examples struct {
	// Package and ImportPath are those of the package of example builders,
	// which Examples writes; the generated tests of the package import it.
	Package    string
	ImportPath string
}

// sample is what the generator knows of the values of a scalar type: a
// valid Go expression and expressions that violate one facet each.
type sample struct {
	valid   string
	invalid []string
}

// patternCandidates are tried, in order, for a value that matches a
// pattern facet; generation fails when none does, so that a new pattern
// gets a candidate here.
var patternCandidates = []string{"1.0", "1", "1.1", "0", "a", "A1", "a.b", "1-1"}

// intRanges are the value ranges of the integer built-ins.
var intRanges = map[string][2]string{
	"byte":          {"-128", "127"},
	"short":         {"-32768", "32767"},
	"int":           {"-2147483648", "2147483647"},
	"long":          {"-9223372036854775808", "9223372036854775807"},
	"unsignedByte":  {"0", "255"},
	"unsignedShort": {"0", "65535"},
	"unsignedInt":   {"0", "4294967295"},
	"unsignedLong":  {"0", "18446744073709551615"},
}

// qual returns the Go type of q as code outside its package writes it.
func (g *gen) qual(q xsd.QName) (string, error) {
	t, err := g.goType(q)
	if err != nil {
		return "", err
	}
	if q.IsBuiltin() || strings.Contains(t, ".") {
		return t, nil
	}
	return g.cfg.Package + "." + t, nil
}

// sample returns the sample values of the scalar type q.
func (g *gen) sample(q xsd.QName) (sample, error) {
	bi, err := g.set.Builtin(q)
	if err != nil {
		return sample{}, err
	}
	t, err := g.qual(q)
	if err != nil {
		return sample{}, err
	}
	var f xsd.Facets
	var enums []string
	if !q.IsBuiltin() {
		f = g.set.EffectiveFacets(q)
		enums = g.set.Enumerations(q)
	}
	conv := func(lit string) string {
		if q.IsBuiltin() && bi.Local != "hexBinary" {
			if bi.Local == "string" || bi.Local == "anyURI" || bi.Local == "boolean" {
				return lit
			}
			return t + "(" + lit + ")"
		}
		return t + "(" + lit + ")"
	}
	switch bi.Local {
	case "boolean":
		return sample{valid: conv("true")}, nil
	case "string", "anyURI":
		return g.stringSample(q, f, enums, conv)
	case "hexBinary":
		return hexSample(q, f, enums, t)
	default:
		return intSample(q, bi.Local, f, enums, conv)
	}
}

func (g *gen) stringSample(q xsd.QName, f xsd.Facets, enums []string, conv func(string) string) (sample, error) {
	var s sample
	quote := func(v string) string { return conv(strconv.Quote(v)) }
	if len(enums) > 0 {
		s.valid = quote(strings.TrimSpace(enums[0]))
		bad := "x"
		for contains(enums, bad) {
			bad += "x"
		}
		s.invalid = append(s.invalid, quote(bad))
		return s, nil
	}
	n := 1
	switch {
	case f.Length != nil:
		n = *f.Length
	case f.MinLength != nil && *f.MinLength > n:
		n = *f.MinLength
	}
	if f.MaxLength != nil && n > *f.MaxLength {
		n = *f.MaxLength
	}
	valid := strings.Repeat("a", n)
	if f.Pattern != "" {
		re := regexp.MustCompile("^(?:" + f.Pattern + ")$")
		valid = ""
		for _, c := range patternCandidates {
			if re.MatchString(c) && fitsLength(len(c), f) {
				valid = c
				break
			}
		}
		if valid == "" {
			return s, fmt.Errorf("%s: no example matches the pattern %q; add a candidate to patternCandidates", q, f.Pattern)
		}
		for _, c := range []string{"", "!", "x!"} {
			if !re.MatchString(c) {
				s.invalid = append(s.invalid, quote(c))
				break
			}
		}
	}
	s.valid = quote(valid)
	if f.Length != nil {
		s.invalid = append(s.invalid, quote(strings.Repeat("a", *f.Length+1)))
	}
	if f.MinLength != nil && *f.MinLength > 0 {
		s.invalid = append(s.invalid, quote(strings.Repeat("a", *f.MinLength-1)))
	}
	if f.MaxLength != nil {
		s.invalid = append(s.invalid, quote(strings.Repeat("a", *f.MaxLength+1)))
	}
	return s, nil
}

func fitsLength(n int, f xsd.Facets) bool {
	return (f.Length == nil || n == *f.Length) && (f.MinLength == nil || n >= *f.MinLength) && (f.MaxLength == nil || n <= *f.MaxLength)
}

func hexSample(q xsd.QName, f xsd.Facets, enums []string, t string) (sample, error) {
	bytesOf := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = "0x01"
		}
		return t + "{" + strings.Join(parts, ", ") + "}"
	}
	var s sample
	if len(enums) > 0 {
		lit, err := literal("hexBinary", enums[0], t)
		if err != nil {
			return s, fmt.Errorf("%s: %w", q, err)
		}
		s.valid = lit
		s.invalid = append(s.invalid, t+"{0xDE, 0xAD, 0xBE, 0xEF, 0x00}")
		return s, nil
	}
	n := 1
	switch {
	case f.Length != nil:
		n = *f.Length
	case f.MinLength != nil && *f.MinLength > n:
		n = *f.MinLength
	}
	if f.MaxLength != nil && n > *f.MaxLength {
		n = *f.MaxLength
	}
	s.valid = bytesOf(n)
	if f.Length != nil {
		s.invalid = append(s.invalid, bytesOf(*f.Length+1))
	}
	if f.MinLength != nil && *f.MinLength > 0 {
		s.invalid = append(s.invalid, bytesOf(*f.MinLength-1))
	}
	if f.MaxLength != nil {
		s.invalid = append(s.invalid, bytesOf(*f.MaxLength+1))
	}
	return s, nil
}

func intSample(q xsd.QName, builtin string, f xsd.Facets, enums []string, conv func(string) string) (sample, error) {
	rng, ok := intRanges[builtin]
	if !ok {
		return sample{}, fmt.Errorf("%s: no example values for xs:%s", q, builtin)
	}
	typeMin, _ := new(big.Int).SetString(rng[0], 10)
	typeMax, _ := new(big.Int).SetString(rng[1], 10)
	lo, hi := new(big.Int).Set(typeMin), new(big.Int).Set(typeMax)
	if f.MinInclusive != nil {
		if _, ok := lo.SetString(*f.MinInclusive, 10); !ok {
			return sample{}, fmt.Errorf("%s: minInclusive %q", q, *f.MinInclusive)
		}
	}
	if f.MaxInclusive != nil {
		if _, ok := hi.SetString(*f.MaxInclusive, 10); !ok {
			return sample{}, fmt.Errorf("%s: maxInclusive %q", q, *f.MaxInclusive)
		}
	}
	var s sample
	if len(enums) > 0 {
		s.valid = conv(strings.TrimSpace(enums[0]))
		for v := new(big.Int).Set(lo); v.Cmp(hi) <= 0; v.Add(v, big.NewInt(1)) {
			if !contains(enums, v.String()) {
				s.invalid = append(s.invalid, conv(v.String()))
				break
			}
		}
		return s, nil
	}
	v := big.NewInt(1)
	if v.Cmp(lo) < 0 {
		v.Set(lo)
	}
	if v.Cmp(hi) > 0 {
		v.Set(hi)
	}
	s.valid = conv(v.String())
	if f.MinInclusive != nil && lo.Cmp(typeMin) > 0 {
		s.invalid = append(s.invalid, conv(new(big.Int).Sub(lo, big.NewInt(1)).String()))
	}
	if f.MaxInclusive != nil && hi.Cmp(typeMax) < 0 {
		s.invalid = append(s.invalid, conv(new(big.Int).Add(hi, big.NewInt(1)).String()))
	}
	return s, nil
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if strings.TrimSpace(e) == v {
			return true
		}
	}
	return false
}

// ref names the example builder of a struct type: in this package, or in
// the base package's examples.
type ref struct {
	q        xsd.QName
	fromBase bool
	own      string // the qualifier of this package's builders
}

func (g *gen) refOf(q xsd.QName) ref { return ref{q: q, fromBase: !g.owned[q], own: g.exQual} }

// embedRef returns the example builder of the embedded base of q.
func (g *gen) embedRef(q xsd.QName) ref {
	ct := g.set.ComplexTypes[q]
	if g.redef[q] && !g.contentAffected(ct) {
		// A redefinition embeds the base package's version of q itself.
		return ref{q: q, fromBase: true, own: g.exQual}
	}
	return g.refOf(ct.Base)
}

// call returns the call of the example builder of r (or of its Invalid
// builder), as code in the examples package writes it.
func (r ref) call(invalid bool, depth string) string {
	prefix := r.own
	if r.fromBase {
		prefix = "baseexamples."
	}
	if invalid {
		return prefix + "Invalid" + GoName(r.q.Local) + "()"
	}
	return prefix + GoName(r.q.Local) + "(" + depth + ")"
}

// baseGen returns the generator of the base package, for the examples of
// its types.
func (g *gen) baseGen() *gen {
	if g.base == nil && g.cfg.Base != nil {
		bg, err := newGen(g.cfg.Base.Set, Config{Package: g.cfg.Base.Package})
		if err == nil {
			g.base = bg
		}
	}
	return g.base
}

// invalidable reports whether a value of the struct type r can be built
// that its Validate rejects, by invalidating one field, its base or a
// nested struct.
func (g *gen) invalidable(r ref) bool {
	if r.fromBase {
		bg := g.baseGen()
		return bg != nil && bg.invalidable(ref{q: r.q})
	}
	switch g.memo[r.q] {
	case 1:
		return true
	case 2, 3: // not invalidable, or in progress (a cycle)
		return false
	}
	g.memo[r.q] = 3
	ok := g.invalidation(r.q) != nil
	g.memo[r.q] = 2
	if ok {
		g.memo[r.q] = 1
	}
	return ok
}

// invalidation is how Invalid<T> makes a T invalid: the field (or the
// embedded base) it sets and the expression it assigns.
type invalidation struct {
	field string
	expr  string
}

func (g *gen) invalidation(q xsd.QName) *invalidation {
	p, err := g.plan(q)
	if err != nil {
		return nil
	}
	if p.embed != "" {
		if er := g.embedRef(q); g.invalidable(er) {
			return &invalidation{field: p.embedName, expr: "*" + er.call(true, "")}
		}
	}
	for _, f := range p.fields {
		if f.any {
			if f.min > 0 {
				return &invalidation{field: f.goName, expr: "nil"}
			}
			continue
		}
		if expr := g.invalidField(f); expr != "" {
			return &invalidation{field: f.goName, expr: expr}
		}
	}
	return nil
}

// ptrFunc is the name of the helper that takes the address of a value:
// ptr in the examples package, generatedPtr in the generated tests, so as
// not to collide with a helper of hand-written tests.
func (g *gen) ptrFunc() string {
	if g.exQual != "" {
		return "generatedPtr"
	}
	return "ptr"
}

// isWrapper reports whether f is a polymorphic element (an Any<T> value).
func (g *gen) isWrapper(f field) bool {
	return g.wrappers[f.typ] && strings.TrimLeft(f.goType, "[]*") == wrapperName(f.typ)
}

// elemType returns the Go element type of f (without pointer or slice), as
// code outside its package writes it.
func (g *gen) elemType(f field) string {
	elem := strings.TrimLeft(f.goType, "[]*")
	if !strings.Contains(elem, ".") && !isGoBuiltin(elem) {
		elem = g.cfg.Package + "." + elem
	}
	return elem
}

// shape turns an expression of the element type of f into one of the type
// of f.
func (g *gen) shape(f field, elem, one string) string {
	switch f.kind {
	case kindValue:
		return one
	case kindPointer:
		if strings.HasPrefix(one, "*") {
			return strings.TrimPrefix(one, "*")
		}
		return g.ptrFunc() + "(" + one + ")"
	default:
		return "[]" + elem + "{" + one + "}"
	}
}

// invalidField returns an expression that makes the field f invalid, or ""
// when there is none.
func (g *gen) invalidField(f field) string {
	if f.kind == kindSlice && f.min > 0 {
		return "nil"
	}
	elem := g.elemType(f)
	switch {
	case g.isWrapper(f):
		if r := g.refOf(f.typ); g.invalidable(r) {
			return g.shape(f, elem, elem+"{Value: "+r.call(true, "")+"}")
		}
	case g.isStruct(f.typ):
		if r := g.refOf(f.typ); g.invalidable(r) {
			return g.shape(f, elem, "*"+r.call(true, ""))
		}
	default:
		if s, err := g.sample(f.typ); err == nil && len(s.invalid) > 0 {
			return g.shape(f, elem, s.invalid[0])
		}
	}
	if f.kind == kindSlice && f.max > 1 && f.max != xsd.Unbounded {
		return g.overfull(f)
	}
	return ""
}

// overfull returns a slice expression with one element more than f admits.
func (g *gen) overfull(f field) string {
	one, err := g.elemValue(f)
	if err != nil {
		return ""
	}
	parts := make([]string, f.max+1)
	for i := range parts {
		parts[i] = one
	}
	return "[]" + g.elemType(f) + "{" + strings.Join(parts, ", ") + "}"
}

// elemValue returns an expression for a valid value of the element type of
// f, with nested structs one level shallower than d.
func (g *gen) elemValue(f field) (string, error) {
	if g.isWrapper(f) {
		return g.elemType(f) + "{Value: " + g.refOf(f.typ).call(false, "d-1") + "}", nil
	}
	if g.isStruct(f.typ) {
		return "*" + g.refOf(f.typ).call(false, "d-1"), nil
	}
	s, err := g.sample(f.typ)
	if err != nil {
		return "", err
	}
	return s.valid, nil
}

// examples emits the example builders of the package.
func (g *gen) examples() (string, error) {
	var b strings.Builder
	for _, q := range g.ownedSorted(true) {
		p, err := g.plan(q)
		if err != nil {
			return "", err
		}
		name := GoName(q.Local)
		t := g.cfg.Package + "." + name
		fmt.Fprintf(&b, "// %s returns a %s whose every value is valid, with the optional\n// elements filled to depth d.\nfunc %s(d int) *%s {\n\tv := new(%s)\n", name, name, name, t, t)
		if p.embed != "" {
			fmt.Fprintf(&b, "\tv.%s = *%s\n", p.embedName, g.embedRef(q).call(false, "d"))
		}
		for _, f := range p.fields {
			if err := g.exampleField(&b, q, f); err != nil {
				return "", err
			}
		}
		b.WriteString("\treturn v\n}\n\n")
		if g.invalidable(ref{q: q}) {
			inv := g.invalidation(q)
			fmt.Fprintf(&b, "// Invalid%s returns a %s that Validate rejects.\nfunc Invalid%s() *%s {\n\tconst d = 0\n\tv := %s(d)\n\tv.%s = %s\n\treturn v\n}\n\n", name, name, name, t, name, inv.field, inv.expr)
		}
	}
	roots, err := g.roots()
	if err != nil {
		return "", err
	}
	b.WriteString("// Element returns an example of the document element with the local name\n// name, filled to depth d, or nil if this package has no such element.\nfunc Element(name string, d int) any {\n\tswitch name {\n")
	for _, el := range roots {
		fmt.Fprintf(&b, "\tcase %q:\n\t\treturn %s(d)\n", el.Name.Local, GoName(el.Type.Local))
	}
	b.WriteString("\t}\n\treturn nil\n}\n\n")
	b.WriteString("// All returns an example of every struct type of the package, filled to\n// depth d.\nfunc All(d int) []any {\n\treturn []any{\n")
	for _, q := range g.ownedSorted(true) {
		fmt.Fprintf(&b, "\t\t%s(d),\n", GoName(q.Local))
	}
	b.WriteString("\t}\n}\n\n")
	b.WriteString("func ptr[T any](v T) *T { return &v }\n")
	return b.String(), nil
}

func (g *gen) exampleField(b *strings.Builder, q xsd.QName, f field) error {
	if f.any {
		if f.min > 0 {
			fmt.Fprintf(b, "\tv.%s = []%sExtensionElement{{XMLName: xml.Name{Space: %q, Local: \"extension\"}, InnerXML: []byte(\"x\")}}\n", f.goName, g.basePkgPrefix(), q.Space)
		}
		return nil
	}
	one, err := g.elemValue(f)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", q, f.xmlName, err)
	}
	nested := g.isStruct(f.typ) || g.isWrapper(f)
	switch f.kind {
	case kindValue:
		fmt.Fprintf(b, "\tv.%s = %s\n", f.goName, one)
	case kindPointer:
		expr := g.shape(f, g.elemType(f), one)
		if g.isWrapper(f) {
			expr = "&" + one
		}
		if nested {
			fmt.Fprintf(b, "\tif d > 0 {\n\t\tv.%s = %s\n\t}\n", f.goName, expr)
		} else {
			fmt.Fprintf(b, "\tv.%s = %s\n", f.goName, expr)
		}
	default:
		n := f.min
		if n < 1 {
			n = 1
		}
		parts := make([]string, n)
		for i := range parts {
			parts[i] = one
		}
		lit := "[]" + g.elemType(f) + "{" + strings.Join(parts, ", ") + "}"
		if f.min > 0 {
			fmt.Fprintf(b, "\tv.%s = %s\n", f.goName, lit)
		} else {
			fmt.Fprintf(b, "\tif d > 0 {\n\t\tv.%s = %s\n\t}\n", f.goName, lit)
		}
	}
	return nil
}

func isGoBuiltin(t string) bool {
	for _, v := range goBuiltin {
		if v == t {
			return true
		}
	}
	return false
}

// basePkgPrefix is the qualifier of the package that defines
// ExtensionElement, as the examples package writes it.
func (g *gen) basePkgPrefix() string {
	if g.cfg.Base != nil {
		return g.cfg.Base.Package + "."
	}
	return g.cfg.Package + "."
}

// GenerateExamples produces the package of example builders of set.
func GenerateExamples(set *xsd.Set, cfg Config) (Files, error) {
	if cfg.Examples == nil {
		return nil, fmt.Errorf("no Examples in the configuration")
	}
	g, err := newGen(set, cfg)
	if err != nil {
		return nil, err
	}
	body, err := g.examples()
	if err != nil {
		return nil, err
	}
	var src strings.Builder
	src.WriteString(cfg.Header)
	fmt.Fprintf(&src, "\n// Package %s builds example values of every type of package %s,\n// for the tests generated with it.\npackage %s\n\nimport (\n", cfg.Examples.Package, cfg.Package, cfg.Examples.Package)
	imports := []string{}
	if strings.Contains(body, "xml.") {
		imports = append(imports, strconv.Quote("encoding/xml"))
	}
	imports = append(imports, cfg.Package+" "+strconv.Quote(cfg.ImportPath))
	if cfg.Base != nil {
		if strings.Contains(body, cfg.Base.Package+".") {
			imports = append(imports, cfg.Base.Package+" "+strconv.Quote(cfg.Base.ImportPath))
		}
		if strings.Contains(body, "baseexamples.") {
			if cfg.Base.Examples == nil {
				return nil, fmt.Errorf("the base package has no examples")
			}
			imports = append(imports, "baseexamples "+strconv.Quote(cfg.Base.Examples.ImportPath))
		}
	}
	sort.Strings(imports)
	src.WriteString(strings.Join(imports, "\n"))
	src.WriteString("\n)\n\n")
	src.WriteString(body)
	out, err := formatSource(src.String())
	if err != nil {
		return nil, err
	}
	return Files{"examples_gen.go": out}, nil
}
