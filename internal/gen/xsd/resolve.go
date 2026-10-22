// SPDX-License-Identifier: MIT

package xsd

import "fmt"

// Resolved wraps a Schema with lookup maps and validates all references.
type Resolved struct {
	*Schema
	SimpleTypeMap  map[string]*SimpleType
	ComplexTypeMap map[string]*ComplexType
	ElementMap     map[string]*Element
}

// Resolve builds lookup maps and validates that every type reference in the
// schema resolves to a known type.
func Resolve(s *Schema) (*Resolved, error) {
	r := &Resolved{
		Schema:         s,
		SimpleTypeMap:  make(map[string]*SimpleType, len(s.SimpleTypes)),
		ComplexTypeMap: make(map[string]*ComplexType, len(s.ComplexTypes)),
		ElementMap:     make(map[string]*Element, len(s.TopElements)),
	}

	for _, st := range s.SimpleTypes {
		r.SimpleTypeMap[st.Name] = st
	}
	for _, ct := range s.ComplexTypes {
		r.ComplexTypeMap[ct.Name] = ct
	}
	for _, el := range s.TopElements {
		r.ElementMap[el.Name] = el
	}

	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Resolved) validate() error {
	for _, ct := range r.ComplexTypes {
		if ct.BaseType != "" && !IsBuiltinXSD(ct.BaseType) {
			if _, ok := r.ComplexTypeMap[ct.BaseType]; !ok {
				if _, ok := r.SimpleTypeMap[ct.BaseType]; !ok {
					return fmt.Errorf("complex type %q: base type %q not found", ct.Name, ct.BaseType)
				}
			}
		}
		for _, el := range ct.Sequence {
			if err := r.validateElementRef(ct.Name, el); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Resolved) validateElementRef(context string, el *Element) error {
	if el.Ref != "" {
		if _, ok := r.ElementMap[el.Ref]; !ok {
			return fmt.Errorf("type %q: element ref %q not found", context, el.Ref)
		}
	}
	if el.Type != "" && !IsBuiltinXSD(el.Type) {
		if _, ok := r.ComplexTypeMap[el.Type]; !ok {
			if _, ok := r.SimpleTypeMap[el.Type]; !ok {
				return fmt.Errorf("type %q: element %q type %q not found", context, el.Name, el.Type)
			}
		}
	}
	return nil
}

// BaseChain returns the chain of base types for ct, nearest first.
func (r *Resolved) BaseChain(ct *ComplexType) []*ComplexType {
	var chain []*ComplexType
	seen := make(map[string]bool)
	current := ct
	for current.BaseType != "" && !IsBuiltinXSD(current.BaseType) {
		if seen[current.BaseType] {
			break
		}
		seen[current.BaseType] = true
		base, ok := r.ComplexTypeMap[current.BaseType]
		if !ok {
			break
		}
		chain = append(chain, base)
		current = base
	}
	return chain
}

// AllSequenceElements returns all sequence elements for ct, including
// inherited elements from base types (base-first order).
func (r *Resolved) AllSequenceElements(ct *ComplexType) []*Element {
	chain := r.BaseChain(ct)

	var all []*Element
	// Walk from root base to ct.
	for i := len(chain) - 1; i >= 0; i-- {
		all = append(all, chain[i].Sequence...)
	}
	all = append(all, ct.Sequence...)
	return all
}

// AllAttributes returns all attributes for ct, including inherited
// attributes from base types (base-first order).
func (r *Resolved) AllAttributes(ct *ComplexType) []*Attribute {
	chain := r.BaseChain(ct)

	var all []*Attribute
	for i := len(chain) - 1; i >= 0; i-- {
		all = append(all, chain[i].Attributes...)
	}
	all = append(all, ct.Attributes...)
	return all
}

// IsBuiltinXSD reports whether name is an XSD builtin type.
func IsBuiltinXSD(name string) bool {
	return len(name) > 3 && (name[:3] == "xs:" || (len(name) > 4 && name[:4] == "xsd:"))
}
