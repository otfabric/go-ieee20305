// SPDX-License-Identifier: MIT

package xsd

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Options configure Load.
type Options struct {
	// Locations maps a schemaLocation as written in a schema document to
	// the file to load instead. Unmapped locations are resolved relative
	// to the referring document.
	Locations map[string]string
	// ElementRefs resolves references to global elements that the schema
	// set does not declare: the reference is read as a local element with
	// the given name and type.
	ElementRefs map[QName]LocalElement
}

// Load reads the schema document at path and every document it includes,
// imports or redefines, and resolves all references.
func Load(path string, opts Options) (*Set, error) {
	l := &loader{
		opts: opts,
		set: &Set{
			SimpleTypes:   map[QName]*SimpleType{},
			ComplexTypes:  map[QName]*ComplexType{},
			Elements:      map[QName]*Element{},
			Redefinitions: map[QName]*Redefinition{},
		},
		loaded: map[string]bool{},
	}
	if err := l.load(path); err != nil {
		return nil, err
	}
	if err := l.resolve(); err != nil {
		return nil, err
	}
	return l.set, nil
}

type loader struct {
	opts    Options
	set     *Set
	loaded  map[string]bool
	pending []pendingRef
}

// pendingRef is an element reference resolved after all documents load.
type pendingRef struct {
	particle *Particle
	ref      QName
	owner    QName
}

func (l *loader) load(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if l.loaded[abs] {
		return nil
	}
	l.loaded[abs] = true

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw rawSchema
	if err := xml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if raw.XMLName.Space != XSDNamespace || raw.XMLName.Local != "schema" {
		return fmt.Errorf("%s: root element is %s, not xs:schema", path, raw.XMLName.Local)
	}
	if err := unsupported(path, raw.Other); err != nil {
		return err
	}
	d := &doc{path: path, raw: &raw, ns: map[string]string{}}
	for _, a := range raw.Attrs {
		switch {
		case a.Name.Space == "xmlns":
			d.ns[a.Name.Local] = a.Value
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			d.ns[""] = a.Value
		}
	}
	d.qualified = raw.ElementFormDefault == "qualified"
	if raw.AttributeFormDefault == "qualified" {
		return fmt.Errorf("%s: attributeFormDefault=qualified is not supported", path)
	}
	l.set.Documents = append(l.set.Documents, &Document{Path: path, TargetNamespace: raw.TargetNamespace, Version: raw.Version})

	dir := filepath.Dir(path)
	locate := func(loc string) string {
		if p, ok := l.opts.Locations[loc]; ok {
			return p
		}
		return filepath.Join(dir, filepath.FromSlash(loc))
	}
	for _, inc := range raw.Includes {
		if err := l.load(locate(inc.SchemaLocation)); err != nil {
			return err
		}
	}
	for _, imp := range raw.Imports {
		if imp.SchemaLocation == "" {
			return fmt.Errorf("%s: xs:import of %s without schemaLocation", path, imp.Namespace)
		}
		if err := l.load(locate(imp.SchemaLocation)); err != nil {
			return err
		}
	}
	for _, red := range raw.Redefines {
		if err := l.load(locate(red.SchemaLocation)); err != nil {
			return err
		}
		if err := unsupported(path, red.Other); err != nil {
			return err
		}
		for i := range red.ComplexTypes {
			if err := d.redefine(l, &red.ComplexTypes[i]); err != nil {
				return err
			}
		}
	}

	for i := range raw.SimpleTypes {
		st, err := d.simpleType(&raw.SimpleTypes[i])
		if err != nil {
			return err
		}
		if _, dup := l.set.SimpleTypes[st.Name]; dup {
			return fmt.Errorf("%s: simple type %s declared twice", path, st.Name)
		}
		l.set.SimpleTypes[st.Name] = st
	}
	for i := range raw.ComplexTypes {
		ct, err := d.complexType(l, &raw.ComplexTypes[i], true)
		if err != nil {
			return err
		}
		if _, dup := l.set.ComplexTypes[ct.Name]; dup {
			return fmt.Errorf("%s: complex type %s declared twice", path, ct.Name)
		}
		l.set.ComplexTypes[ct.Name] = ct
	}
	for i := range raw.Elements {
		el, err := d.globalElement(l, &raw.Elements[i])
		if err != nil {
			return err
		}
		if _, dup := l.set.Elements[el.Name]; dup {
			return fmt.Errorf("%s: element %s declared twice", path, el.Name)
		}
		l.set.Elements[el.Name] = el
	}
	return nil
}

// doc is the context of one schema document during loading.
type doc struct {
	path      string
	raw       *rawSchema
	ns        map[string]string
	qualified bool
}

func (d *doc) errorf(format string, args ...any) error {
	return fmt.Errorf("%s: "+format, append([]any{d.path}, args...)...)
}

// qname resolves a QName-valued attribute against the document's namespace
// declarations. IEEE 2030.5 schemas declare namespaces on xs:schema only.
func (d *doc) qname(s string) (QName, error) {
	prefix, local := "", s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		prefix, local = s[:i], s[i+1:]
	}
	space, ok := d.ns[prefix]
	if !ok {
		if prefix != "" {
			return QName{}, d.errorf("undeclared namespace prefix in %q", s)
		}
		space = ""
	}
	return QName{Space: space, Local: local}, nil
}

func (d *doc) name(local string) QName {
	return QName{Space: d.raw.TargetNamespace, Local: local}
}

func (d *doc) simpleType(raw *rawSimpleType) (*SimpleType, error) {
	if raw.Name == "" {
		return nil, d.errorf("anonymous xs:simpleType is not supported")
	}
	if err := unsupported(d.path, raw.Other); err != nil {
		return nil, err
	}
	st := &SimpleType{Name: d.name(raw.Name), Doc: raw.Annotation.text()}
	r := raw.Restriction
	if r == nil {
		return nil, d.errorf("simple type %s: only xs:restriction is supported", raw.Name)
	}
	if err := unsupported(d.path, r.Other); err != nil {
		return nil, err
	}
	base, err := d.qname(r.Base)
	if err != nil {
		return nil, err
	}
	st.Base = base
	if st.Facets, err = facets(r); err != nil {
		return nil, d.errorf("simple type %s: %v", raw.Name, err)
	}
	for _, e := range r.Enumerations {
		st.Enumerations = append(st.Enumerations, e.Value)
	}
	return st, nil
}

func facets(r *rawRestriction) (Facets, error) {
	var f Facets
	atoi := func(v *rawFacet) (*int, error) {
		if v == nil {
			return nil, nil
		}
		n, err := strconv.Atoi(v.Value)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad length facet %q", v.Value)
		}
		return &n, nil
	}
	var err error
	if f.Length, err = atoi(r.Length); err != nil {
		return f, err
	}
	if f.MinLength, err = atoi(r.MinLength); err != nil {
		return f, err
	}
	if f.MaxLength, err = atoi(r.MaxLength); err != nil {
		return f, err
	}
	if r.MinInclusive != nil {
		f.MinInclusive = &r.MinInclusive.Value
	}
	if r.MaxInclusive != nil {
		f.MaxInclusive = &r.MaxInclusive.Value
	}
	if len(r.Patterns) > 1 {
		return f, fmt.Errorf("more than one pattern facet")
	}
	if len(r.Patterns) == 1 {
		f.Pattern = r.Patterns[0].Value
	}
	return f, nil
}

func (d *doc) complexType(l *loader, raw *rawComplexType, named bool) (*ComplexType, error) {
	if named && raw.Name == "" {
		return nil, d.errorf("global xs:complexType without a name")
	}
	if raw.Mixed || raw.Abstract {
		return nil, d.errorf("complex type %s: mixed and abstract are not supported", raw.Name)
	}
	if err := unsupported(d.path, raw.Other); err != nil {
		return nil, err
	}
	ct := &ComplexType{Doc: raw.Annotation.text()}
	if named {
		ct.Name = d.name(raw.Name)
	}
	var seq *rawSequence
	var attrs []rawAttribute
	var anyAttr *rawAnyAttribute
	switch {
	case raw.ComplexContent != nil:
		cc := raw.ComplexContent
		if err := unsupported(d.path, cc.Other); err != nil {
			return nil, err
		}
		if cc.Extension == nil {
			return nil, d.errorf("complex type %s: only complexContent/extension is supported", raw.Name)
		}
		ext := cc.Extension
		if err := unsupported(d.path, ext.Other); err != nil {
			return nil, err
		}
		base, err := d.qname(ext.Base)
		if err != nil {
			return nil, err
		}
		ct.Content, ct.Base = ContentComplexExtension, base
		seq, attrs, anyAttr = ext.Sequence, ext.Attributes, ext.AnyAttribute
	case raw.SimpleContent != nil:
		sc := raw.SimpleContent
		if err := unsupported(d.path, sc.Other); err != nil {
			return nil, err
		}
		if sc.Extension == nil {
			return nil, d.errorf("complex type %s: only simpleContent/extension is supported", raw.Name)
		}
		ext := sc.Extension
		if err := unsupported(d.path, ext.Other); err != nil {
			return nil, err
		}
		if ext.Sequence != nil {
			return nil, d.errorf("complex type %s: simpleContent with a sequence", raw.Name)
		}
		base, err := d.qname(ext.Base)
		if err != nil {
			return nil, err
		}
		ct.Content, ct.Base = ContentSimpleExtension, base
		attrs, anyAttr = ext.Attributes, ext.AnyAttribute
	default:
		ct.Content = ContentSequence
		seq, attrs, anyAttr = raw.Sequence, raw.Attributes, raw.AnyAttribute
	}
	ct.AnyAttribute = anyAttr != nil
	if seq != nil {
		ps, err := d.particles(l, seq, ct.Name)
		if err != nil {
			return nil, err
		}
		ct.Particles = ps
	}
	as, err := d.attributes(attrs, ct.Name)
	if err != nil {
		return nil, err
	}
	ct.Attributes = as
	return ct, nil
}

func (d *doc) particles(l *loader, seq *rawSequence, owner QName) ([]*Particle, error) {
	if seq.MinOccurs != "" || seq.MaxOccurs != "" {
		return nil, d.errorf("%s: occurrence constraints on xs:sequence are not supported", owner)
	}
	var out []*Particle
	for _, item := range seq.Items {
		p := &Particle{}
		var err error
		if p.MinOccurs, err = occurs(item.MinOccurs, 1); err != nil {
			return nil, d.errorf("%s: %v", owner, err)
		}
		if p.MaxOccurs, err = occurs(item.MaxOccurs, 1); err != nil {
			return nil, d.errorf("%s: %v", owner, err)
		}
		switch item.XMLName.Local {
		case "element":
			if item.ComplexType != nil || item.SimpleType != nil {
				return nil, d.errorf("%s: element %s has an anonymous type, which is not supported", owner, item.Name)
			}
			el := &LocalElement{Doc: item.Annotation.text()}
			if item.Default != "" || item.HasDefault {
				v := item.Default
				el.Default = &v
			}
			if item.Fixed != "" || item.Nillable != "" {
				return nil, d.errorf("%s: fixed and nillable elements are not supported", owner)
			}
			if item.Ref != "" {
				if item.Name != "" || item.Type != "" {
					return nil, d.errorf("%s: element ref with name or type", owner)
				}
				ref, err := d.qname(item.Ref)
				if err != nil {
					return nil, err
				}
				el.Ref = true
				p.Element = el
				l.pending = append(l.pending, pendingRef{particle: p, ref: ref, owner: owner})
			} else {
				if item.Name == "" || item.Type == "" {
					return nil, d.errorf("%s: element needs a name and a type", owner)
				}
				typ, err := d.qname(item.Type)
				if err != nil {
					return nil, err
				}
				el.Name = QName{Local: item.Name}
				if d.qualified {
					el.Name.Space = d.raw.TargetNamespace
				}
				el.Type = typ
				p.Element = el
			}
		case "any":
			ns := item.Namespace
			if ns == "" {
				ns = "##any"
			}
			if ns != "##other" && ns != "##any" && ns != "##targetNamespace" {
				return nil, d.errorf("%s: xs:any namespace %q is not supported", owner, ns)
			}
			pc := item.ProcessContents
			if pc == "" {
				pc = "strict"
			}
			p.Any = &Wildcard{Namespace: ns, ProcessContents: pc}
		default:
			return nil, d.errorf("%s: xs:%s in a sequence is not supported", owner, item.XMLName.Local)
		}
		out = append(out, p)
	}
	return out, nil
}

func (d *doc) attributes(raws []rawAttribute, owner QName) ([]*Attribute, error) {
	var out []*Attribute
	for _, ra := range raws {
		if ra.Ref != "" || ra.Name == "" || ra.Type == "" || ra.SimpleType != nil || ra.Fixed != "" || ra.Form != "" {
			return nil, d.errorf("%s: attribute %q: only named attributes with a named type are supported", owner, ra.Name)
		}
		typ, err := d.qname(ra.Type)
		if err != nil {
			return nil, err
		}
		a := &Attribute{Name: ra.Name, Type: typ, Doc: ra.Annotation.text()}
		switch ra.Use {
		case "", "optional":
		case "required":
			a.Required = true
		default:
			return nil, d.errorf("%s: attribute %s: use=%q is not supported", owner, ra.Name, ra.Use)
		}
		if ra.HasDefault {
			v := ra.Default
			a.Default = &v
		}
		out = append(out, a)
	}
	return out, nil
}

func (d *doc) globalElement(l *loader, raw *rawElement) (*Element, error) {
	if raw.Name == "" || raw.Ref != "" {
		return nil, d.errorf("global element without a name")
	}
	el := &Element{Name: d.name(raw.Name), Doc: raw.Annotation.text()}
	switch {
	case raw.Type != "" && raw.ComplexType == nil && raw.SimpleType == nil:
		typ, err := d.qname(raw.Type)
		if err != nil {
			return nil, err
		}
		el.Type = typ
	case raw.Type == "" && raw.ComplexType != nil:
		ct, err := d.complexType(l, raw.ComplexType, false)
		if err != nil {
			return nil, err
		}
		if ct.Content != ContentComplexExtension || len(ct.Particles) > 0 {
			return nil, d.errorf("element %s: an anonymous type may only extend a named type with attributes", raw.Name)
		}
		el.Type = ct.Base
		el.ExtraAttributes = ct.Attributes
	default:
		return nil, d.errorf("element %s: needs exactly one of a type attribute and an anonymous complex type", raw.Name)
	}
	return el, nil
}

// redefine records an xs:redefine of a complex type that extends its
// original definition.
func (d *doc) redefine(l *loader, raw *rawComplexType) error {
	ct, err := d.complexType(l, raw, true)
	if err != nil {
		return err
	}
	if ct.Content != ContentComplexExtension || ct.Base != ct.Name {
		return d.errorf("redefinition of %s must extend the original type", ct.Name)
	}
	if _, dup := l.set.Redefinitions[ct.Name]; dup {
		return d.errorf("%s redefined twice", ct.Name)
	}
	l.set.Redefinitions[ct.Name] = &Redefinition{Particles: ct.Particles, Attributes: ct.Attributes}
	return nil
}

func occurs(s string, def int) (int, error) {
	switch s {
	case "":
		return def, nil
	case "unbounded":
		return Unbounded, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad occurrence %q", s)
	}
	return n, nil
}

func unsupported(path string, other []rawOther) error {
	if len(other) == 0 {
		return nil
	}
	return fmt.Errorf("%s: unsupported XSD construct xs:%s", path, other[0].XMLName.Local)
}

// --- raw documents, decoded with encoding/xml ---

type rawOther struct {
	XMLName xml.Name
}

type rawAnnotation struct {
	Documentation []string `xml:"documentation"`
}

func (a *rawAnnotation) text() string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join(a.Documentation, "\n\n"))
}

type rawSchema struct {
	XMLName              xml.Name
	Attrs                []xml.Attr       `xml:",any,attr"`
	TargetNamespace      string           `xml:"targetNamespace,attr"`
	Version              string           `xml:"version,attr"`
	ElementFormDefault   string           `xml:"elementFormDefault,attr"`
	AttributeFormDefault string           `xml:"attributeFormDefault,attr"`
	Annotation           *rawAnnotation   `xml:"annotation"`
	Includes             []rawInclude     `xml:"include"`
	Imports              []rawImport      `xml:"import"`
	Redefines            []rawRedefine    `xml:"redefine"`
	SimpleTypes          []rawSimpleType  `xml:"simpleType"`
	ComplexTypes         []rawComplexType `xml:"complexType"`
	Elements             []rawElement     `xml:"element"`
	Other                []rawOther       `xml:",any"`
}

type rawInclude struct {
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type rawImport struct {
	Namespace      string `xml:"namespace,attr"`
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type rawRedefine struct {
	SchemaLocation string           `xml:"schemaLocation,attr"`
	Annotation     *rawAnnotation   `xml:"annotation"`
	ComplexTypes   []rawComplexType `xml:"complexType"`
	Other          []rawOther       `xml:",any"`
}

type rawSimpleType struct {
	Name        string          `xml:"name,attr"`
	Annotation  *rawAnnotation  `xml:"annotation"`
	Restriction *rawRestriction `xml:"restriction"`
	Other       []rawOther      `xml:",any"`
}

type rawRestriction struct {
	Base         string         `xml:"base,attr"`
	Annotation   *rawAnnotation `xml:"annotation"`
	Enumerations []rawFacet     `xml:"enumeration"`
	Length       *rawFacet      `xml:"length"`
	MinLength    *rawFacet      `xml:"minLength"`
	MaxLength    *rawFacet      `xml:"maxLength"`
	MinInclusive *rawFacet      `xml:"minInclusive"`
	MaxInclusive *rawFacet      `xml:"maxInclusive"`
	Patterns     []rawFacet     `xml:"pattern"`
	Other        []rawOther     `xml:",any"`
}

type rawFacet struct {
	Value      string         `xml:"value,attr"`
	Annotation *rawAnnotation `xml:"annotation"`
}

type rawComplexType struct {
	Name           string           `xml:"name,attr"`
	Mixed          bool             `xml:"mixed,attr"`
	Abstract       bool             `xml:"abstract,attr"`
	Annotation     *rawAnnotation   `xml:"annotation"`
	Sequence       *rawSequence     `xml:"sequence"`
	ComplexContent *rawContent      `xml:"complexContent"`
	SimpleContent  *rawContent      `xml:"simpleContent"`
	Attributes     []rawAttribute   `xml:"attribute"`
	AnyAttribute   *rawAnyAttribute `xml:"anyAttribute"`
	Other          []rawOther       `xml:",any"`
}

type rawContent struct {
	Annotation *rawAnnotation `xml:"annotation"`
	Extension  *rawExtension  `xml:"extension"`
	Other      []rawOther     `xml:",any"`
}

type rawExtension struct {
	Base         string           `xml:"base,attr"`
	Annotation   *rawAnnotation   `xml:"annotation"`
	Sequence     *rawSequence     `xml:"sequence"`
	Attributes   []rawAttribute   `xml:"attribute"`
	AnyAttribute *rawAnyAttribute `xml:"anyAttribute"`
	Other        []rawOther       `xml:",any"`
}

type rawAnyAttribute struct {
	ProcessContents string `xml:"processContents,attr"`
}

type rawSequence struct {
	MinOccurs  string         `xml:"minOccurs,attr"`
	MaxOccurs  string         `xml:"maxOccurs,attr"`
	Annotation *rawAnnotation `xml:"annotation"`
	Items      []rawItem      `xml:",any"`
}

// rawItem is an xs:element or xs:any inside a sequence; the sequence keeps
// them in document order.
type rawItem struct {
	XMLName         xml.Name
	Name            string          `xml:"name,attr"`
	Type            string          `xml:"type,attr"`
	Ref             string          `xml:"ref,attr"`
	MinOccurs       string          `xml:"minOccurs,attr"`
	MaxOccurs       string          `xml:"maxOccurs,attr"`
	Default         string          `xml:"default,attr"`
	HasDefault      bool            `xml:"-"`
	Fixed           string          `xml:"fixed,attr"`
	Nillable        string          `xml:"nillable,attr"`
	Namespace       string          `xml:"namespace,attr"`
	ProcessContents string          `xml:"processContents,attr"`
	Annotation      *rawAnnotation  `xml:"annotation"`
	ComplexType     *rawComplexType `xml:"complexType"`
	SimpleType      *rawSimpleType  `xml:"simpleType"`
}

// UnmarshalXML records whether a default attribute is present, since an
// empty default is meaningful.
func (it *rawItem) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain rawItem
	if err := d.DecodeElement((*plain)(it), &start); err != nil {
		return err
	}
	for _, a := range start.Attr {
		if a.Name.Space == "" && a.Name.Local == "default" {
			it.HasDefault = true
		}
	}
	return nil
}

type rawElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	Ref         string          `xml:"ref,attr"`
	Annotation  *rawAnnotation  `xml:"annotation"`
	ComplexType *rawComplexType `xml:"complexType"`
	SimpleType  *rawSimpleType  `xml:"simpleType"`
}

type rawAttribute struct {
	Name       string         `xml:"name,attr"`
	Type       string         `xml:"type,attr"`
	Ref        string         `xml:"ref,attr"`
	Use        string         `xml:"use,attr"`
	Default    string         `xml:"default,attr"`
	HasDefault bool           `xml:"-"`
	Fixed      string         `xml:"fixed,attr"`
	Form       string         `xml:"form,attr"`
	Annotation *rawAnnotation `xml:"annotation"`
	SimpleType *rawSimpleType `xml:"simpleType"`
}

// UnmarshalXML records whether a default attribute is present.
func (a *rawAttribute) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain rawAttribute
	if err := d.DecodeElement((*plain)(a), &start); err != nil {
		return err
	}
	for _, at := range start.Attr {
		if at.Name.Space == "" && at.Name.Local == "default" {
			a.HasDefault = true
		}
	}
	return nil
}
