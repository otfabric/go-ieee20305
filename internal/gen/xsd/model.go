// SPDX-License-Identifier: MIT

package xsd

// Schema is the typed intermediate representation of an IEEE 2030.5 XSD.
type Schema struct {
	Namespace    string
	Version      string
	SimpleTypes  []*SimpleType
	ComplexTypes []*ComplexType
	TopElements  []*Element
}

// SimpleType represents an xs:simpleType.
type SimpleType struct {
	Name         string
	Doc          string
	Base         string // xs:restriction base
	Enumerations []string
	MinLength    int
	MaxLength    int
	Pattern      string
	MinInclusive string
	MaxInclusive string
}

// IsEnum reports whether the simple type defines an enumeration.
func (st *SimpleType) IsEnum() bool {
	return len(st.Enumerations) > 0
}

// ContentKind classifies how a complex type's body is defined.
type ContentKind int

const (
	ContentSequence      ContentKind = iota // own xs:sequence only
	ContentComplexExtend                    // xs:complexContent/xs:extension
)

// ComplexType represents an xs:complexType.
type ComplexType struct {
	Name        string
	Doc         string
	Abstract    bool
	BaseType    string      // non-empty when ContentKind is ContentComplexExtend
	ContentKind ContentKind
	Sequence    []*Element
	Attributes  []*Attribute
}

// Element represents an xs:element inside a sequence or at schema top level.
type Element struct {
	Name          string
	Type          string
	Ref           string
	MinOccurs     int
	MaxOccurs     int // -1 means unbounded
	Doc           string
	InlineComplex *ComplexType
	InlineSimple  *SimpleType
}

// Attribute represents an xs:attribute on a complex type.
type Attribute struct {
	Name         string
	Type         string
	Use          string // "required" or "optional" (default)
	Default      string
	Doc          string
	InlineSimple *SimpleType
}
