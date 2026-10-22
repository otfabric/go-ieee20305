// SPDX-License-Identifier: MIT

// Package xsd loads a set of XML Schema documents into a typed,
// namespace-aware model. It supports the subset of XSD that IEEE 2030.5 and
// its profiles use and rejects everything else, so that the generator never
// silently misreads a schema.
package xsd

import (
	"fmt"
	"strings"
)

// XSDNamespace is the namespace of XML Schema itself.
const XSDNamespace = "http://www.w3.org/2001/XMLSchema"

// QName is a namespace-qualified name.
type QName struct {
	Space string
	Local string
}

// String renders q in Clark notation: {space}local.
func (q QName) String() string {
	if q.Space == "" {
		return q.Local
	}
	return "{" + q.Space + "}" + q.Local
}

// IsBuiltin reports whether q names an XML Schema built-in type.
func (q QName) IsBuiltin() bool { return q.Space == XSDNamespace }

// IsZero reports whether q is empty.
func (q QName) IsZero() bool { return q.Local == "" }

// ParseQName parses Clark notation ({space}local or local).
func ParseQName(s string) (QName, error) {
	if !strings.HasPrefix(s, "{") {
		if s == "" {
			return QName{}, fmt.Errorf("empty name")
		}
		return QName{Local: s}, nil
	}
	i := strings.IndexByte(s, '}')
	if i < 0 || i == len(s)-1 {
		return QName{}, fmt.Errorf("malformed name %q", s)
	}
	return QName{Space: s[1:i], Local: s[i+1:]}, nil
}

// Set is a resolved collection of schema documents: every component of every
// document reachable from the root through include, import and redefine.
type Set struct {
	SimpleTypes  map[QName]*SimpleType
	ComplexTypes map[QName]*ComplexType
	Elements     map[QName]*Element
	// Redefinitions holds, for each complex type redefined by an
	// xs:redefine, what the redefinition appends. ComplexTypes keeps the
	// original definition.
	Redefinitions map[QName]*Redefinition
	// Documents lists the loaded documents in load order.
	Documents []*Document
}

// Document is one loaded schema document.
type Document struct {
	Path            string
	TargetNamespace string
	Version         string
}

// SimpleType is a named xs:simpleType defined by restriction.
type SimpleType struct {
	Name         QName
	Doc          string
	Base         QName
	Facets       Facets
	Enumerations []string
}

// Facets are the constraining facets of a restriction. Nil means absent.
type Facets struct {
	Length       *int
	MinLength    *int
	MaxLength    *int
	MinInclusive *string
	MaxInclusive *string
	Pattern      string
}

// Content classifies the body of a complex type.
type Content int

// Kinds of complex type content.
const (
	// ContentSequence is an xs:sequence (possibly empty) with attributes.
	ContentSequence Content = iota
	// ContentComplexExtension is xs:complexContent/xs:extension of Base.
	ContentComplexExtension
	// ContentSimpleExtension is xs:simpleContent/xs:extension of Base:
	// text content of a simple type.
	ContentSimpleExtension
)

// ComplexType is a named or anonymous xs:complexType.
type ComplexType struct {
	Name         QName
	Doc          string
	Content      Content
	Base         QName
	Particles    []*Particle
	Attributes   []*Attribute
	AnyAttribute bool
}

// Particle is one member of a sequence: an element or a wildcard.
type Particle struct {
	Element   *LocalElement
	Any       *Wildcard
	MinOccurs int
	MaxOccurs int // Unbounded for maxOccurs="unbounded"
}

// Unbounded is the MaxOccurs of maxOccurs="unbounded".
const Unbounded = -1

// LocalElement is an element in a content model, declared locally or by
// reference to a global element.
type LocalElement struct {
	Name    QName
	Type    QName
	Default *string
	Doc     string
	// Ref is set when the element is a reference to a global element.
	Ref bool
}

// Wildcard is an xs:any.
type Wildcard struct {
	Namespace       string
	ProcessContents string
}

// Attribute is a local attribute. IEEE 2030.5 attributes are unqualified.
type Attribute struct {
	Name     string
	Type     QName
	Required bool
	Default  *string
	Doc      string
}

// Element is a global element declaration.
type Element struct {
	Name QName
	Doc  string
	// Type is the named type of the element. For an element with an
	// anonymous type that extends a named type with attributes only,
	// Type is that named type and ExtraAttributes lists the attributes.
	Type            QName
	ExtraAttributes []*Attribute
}

// Redefinition is what an xs:redefine appends to a complex type.
type Redefinition struct {
	Particles  []*Particle
	Attributes []*Attribute
}

// BaseChain returns the complex base types of ct, nearest first.
func (s *Set) BaseChain(ct *ComplexType) []*ComplexType {
	var chain []*ComplexType
	for cur := ct; cur.Content == ContentComplexExtension; {
		base := s.ComplexTypes[cur.Base]
		chain = append(chain, base)
		cur = base
	}
	return chain
}
