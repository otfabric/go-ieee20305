// SPDX-License-Identifier: MIT

package xsd

import (
	"fmt"
	"sort"
)

// builtins lists the XML Schema built-in types the generator maps to Go.
var builtins = map[string]bool{
	"string": true, "anyURI": true, "boolean": true, "hexBinary": true,
	"byte": true, "short": true, "int": true, "long": true,
	"unsignedByte": true, "unsignedShort": true, "unsignedInt": true, "unsignedLong": true,
}

func (l *loader) resolve() error {
	s := l.set
	for _, p := range l.pending {
		if g, ok := s.Elements[p.ref]; ok {
			p.particle.Element.Name = g.Name
			p.particle.Element.Type = g.Type
			if p.particle.Element.Doc == "" {
				p.particle.Element.Doc = g.Doc
			}
			continue
		}
		if o, ok := l.opts.ElementRefs[p.ref]; ok {
			p.particle.Element.Name = o.Name
			p.particle.Element.Type = o.Type
			p.particle.Element.Ref = false
			continue
		}
		return fmt.Errorf("%s: element ref %s does not resolve to a global element", p.owner, p.ref)
	}

	// A complexContent extension of a simple-content type that adds only
	// attributes is simple content with attributes.
	for _, n := range s.ComplexTypeNames() {
		ct := s.ComplexTypes[n]
		if ct.Content != ContentComplexExtension {
			continue
		}
		if base, ok := s.ComplexTypes[ct.Base]; ok && base.Content == ContentSimpleExtension {
			if len(ct.Particles) > 0 {
				return fmt.Errorf("%s: elements added to simple content %s", n, ct.Base)
			}
			ct.Content = ContentSimpleExtension
		}
	}

	for _, n := range s.SimpleTypeNames() {
		st := s.SimpleTypes[n]
		if err := s.checkSimpleRef(st.Base, "base of "+n.String()); err != nil {
			return err
		}
		if _, err := s.Builtin(n); err != nil {
			return err
		}
	}
	for _, n := range s.ComplexTypeNames() {
		ct := s.ComplexTypes[n]
		if err := s.checkComplexType(ct); err != nil {
			return err
		}
	}
	for n, red := range s.Redefinitions {
		ct, ok := s.ComplexTypes[n]
		if !ok {
			return fmt.Errorf("redefinition of undeclared type %s", n)
		}
		if ct.Content == ContentSimpleExtension {
			return fmt.Errorf("redefinition of simple-content type %s is not supported", n)
		}
		probe := &ComplexType{Name: n, Content: ContentSequence, Particles: red.Particles, Attributes: red.Attributes}
		if err := s.checkComplexType(probe); err != nil {
			return err
		}
	}
	for _, el := range s.Elements {
		if _, ok := s.ComplexTypes[el.Type]; !ok {
			if err := s.checkSimpleRef(el.Type, "element "+el.Name.String()); err != nil {
				return err
			}
		}
		for _, a := range el.ExtraAttributes {
			if err := s.checkSimpleRef(a.Type, "attribute "+a.Name+" of element "+el.Name.String()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Set) checkComplexType(ct *ComplexType) error {
	switch ct.Content {
	case ContentSequence:
	case ContentComplexExtension:
		seen := map[QName]bool{ct.Name: true}
		for cur := ct; cur.Content == ContentComplexExtension; {
			base, ok := s.ComplexTypes[cur.Base]
			if !ok {
				return fmt.Errorf("%s: base %s is not a complex type", cur.Name, cur.Base)
			}
			if base.Content == ContentSimpleExtension {
				return fmt.Errorf("%s: complex content extending simple content %s", cur.Name, base.Name)
			}
			if seen[base.Name] {
				return fmt.Errorf("%s: circular base chain", ct.Name)
			}
			seen[base.Name] = true
			cur = base
		}
	case ContentSimpleExtension:
		if _, ok := s.ComplexTypes[ct.Base]; ok {
			base := s.ComplexTypes[ct.Base]
			if base.Content != ContentSimpleExtension {
				return fmt.Errorf("%s: simple content extending complex content %s", ct.Name, ct.Base)
			}
			if len(base.Attributes) > 0 {
				return fmt.Errorf("%s: extending simple content with attributes (%s) is not supported", ct.Name, ct.Base)
			}
		} else if err := s.checkSimpleRef(ct.Base, "base of "+ct.Name.String()); err != nil {
			return err
		}
	}
	for _, p := range ct.Particles {
		if p.Element == nil {
			continue
		}
		el := p.Element
		if _, ok := s.ComplexTypes[el.Type]; ok {
			continue
		}
		if err := s.checkSimpleRef(el.Type, "element "+el.Name.Local+" of "+ct.Name.String()); err != nil {
			return err
		}
	}
	for _, a := range ct.Attributes {
		if err := s.checkSimpleRef(a.Type, "attribute "+a.Name+" of "+ct.Name.String()); err != nil {
			return err
		}
		if !s.IsScalar(a.Type) {
			return fmt.Errorf("attribute %s of %s has type %s, which is not a scalar", a.Name, ct.Name, a.Type)
		}
	}
	return nil
}

// checkSimpleRef reports whether q names a supported built-in, a simple
// type, or a simple-content complex type.
func (s *Set) checkSimpleRef(q QName, context string) error {
	if q.IsBuiltin() {
		if !builtins[q.Local] {
			return fmt.Errorf("%s: built-in type xs:%s is not supported", context, q.Local)
		}
		return nil
	}
	if _, ok := s.SimpleTypes[q]; ok {
		return nil
	}
	if ct, ok := s.ComplexTypes[q]; ok {
		if ct.Content == ContentSimpleExtension {
			return nil
		}
		return fmt.Errorf("%s: type %s has element content, not a scalar", context, q)
	}
	return fmt.Errorf("%s: type %s is not declared", context, q)
}

// IsScalar reports whether q denotes a value without structure: a built-in,
// a simple type, or a simple-content complex type without attributes.
func (s *Set) IsScalar(q QName) bool {
	if q.IsBuiltin() {
		return true
	}
	if _, ok := s.SimpleTypes[q]; ok {
		return true
	}
	ct, ok := s.ComplexTypes[q]
	return ok && ct.Content == ContentSimpleExtension && len(ct.Attributes) == 0
}

// Builtin returns the XML Schema built-in type a simple value ultimately
// derives from.
func (s *Set) Builtin(q QName) (QName, error) {
	seen := map[QName]bool{}
	for !q.IsBuiltin() {
		if seen[q] {
			return QName{}, fmt.Errorf("%s: circular simple type derivation", q)
		}
		seen[q] = true
		if st, ok := s.SimpleTypes[q]; ok {
			q = st.Base
			continue
		}
		if ct, ok := s.ComplexTypes[q]; ok && ct.Content == ContentSimpleExtension {
			q = ct.Base
			continue
		}
		return QName{}, fmt.Errorf("%s is not a simple type", q)
	}
	return q, nil
}

// SimpleBase returns the type a simple value derives from directly, or the
// zero QName for a built-in.
func (s *Set) SimpleBase(q QName) QName {
	if st, ok := s.SimpleTypes[q]; ok {
		return st.Base
	}
	if ct, ok := s.ComplexTypes[q]; ok && ct.Content == ContentSimpleExtension {
		return ct.Base
	}
	return QName{}
}

// EffectiveFacets merges the facets along the derivation of a simple value;
// a facet declared closer to q wins.
func (s *Set) EffectiveFacets(q QName) Facets {
	var chain []Facets
	for cur := q; !cur.IsZero() && !cur.IsBuiltin(); cur = s.SimpleBase(cur) {
		if st, ok := s.SimpleTypes[cur]; ok {
			chain = append(chain, st.Facets)
		}
	}
	var f Facets
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		if c.Length != nil {
			f.Length = c.Length
		}
		if c.MinLength != nil {
			f.MinLength = c.MinLength
		}
		if c.MaxLength != nil {
			f.MaxLength = c.MaxLength
		}
		if c.MinInclusive != nil {
			f.MinInclusive = c.MinInclusive
		}
		if c.MaxInclusive != nil {
			f.MaxInclusive = c.MaxInclusive
		}
		if c.Pattern != "" {
			f.Pattern = c.Pattern
		}
	}
	return f
}

// Enumerations returns the enumeration values of the nearest simple type in
// the derivation of q that declares any.
func (s *Set) Enumerations(q QName) []string {
	for cur := q; !cur.IsZero() && !cur.IsBuiltin(); cur = s.SimpleBase(cur) {
		if st, ok := s.SimpleTypes[cur]; ok && len(st.Enumerations) > 0 {
			return st.Enumerations
		}
	}
	return nil
}

// SimpleTypeNames returns the simple type names in sorted order.
func (s *Set) SimpleTypeNames() []QName {
	return sortedKeys(s.SimpleTypes)
}

// ComplexTypeNames returns the complex type names in sorted order.
func (s *Set) ComplexTypeNames() []QName {
	return sortedKeys(s.ComplexTypes)
}

// ElementNames returns the global element names in sorted order.
func (s *Set) ElementNames() []QName {
	return sortedKeys(s.Elements)
}

func sortedKeys[V any](m map[QName]V) []QName {
	keys := make([]QName, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Space != keys[j].Space {
			return keys[i].Space < keys[j].Space
		}
		return keys[i].Local < keys[j].Local
	})
	return keys
}
