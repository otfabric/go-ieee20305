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

// ParseFile parses a single XSD file into a Schema.
func ParseFile(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// ParseBundle parses a primary XSD and any xs:include files from the same
// directory. IEEE 2030.5 uses a single sep.xsd, but this supports the
// CSIP-AUS extension XSDs that include the base schema.
func ParseBundle(dir, primary string) (*Schema, error) {
	path := filepath.Join(dir, primary)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw xsdSchema
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	// Process xs:include directives.
	for _, inc := range raw.Includes {
		incPath := filepath.Join(dir, inc.SchemaLocation)
		incData, err := os.ReadFile(incPath)
		if err != nil {
			return nil, fmt.Errorf("include %s: %w", inc.SchemaLocation, err)
		}
		var incRaw xsdSchema
		if err := xml.Unmarshal(incData, &incRaw); err != nil {
			return nil, fmt.Errorf("unmarshal include %s: %w", inc.SchemaLocation, err)
		}
		raw.SimpleTypes = append(raw.SimpleTypes, incRaw.SimpleTypes...)
		raw.ComplexTypes = append(raw.ComplexTypes, incRaw.ComplexTypes...)
		raw.Elements = append(raw.Elements, incRaw.Elements...)
	}

	return convert(&raw), nil
}

// Parse parses XSD bytes into a Schema.
func Parse(data []byte) (*Schema, error) {
	var raw xsdSchema
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal xsd: %w", err)
	}
	return convert(&raw), nil
}

// ExtensionBundle holds a parsed extension schema alongside its base schema.
// The extension's types may reference base types; the Base schema is needed
// for resolution but the extension emitter only generates Go code for the
// extension's own types.
type ExtensionBundle struct {
	Base      *Schema
	Extension *Schema
}

// ParseExtension parses an extension XSD (like CSIP-AUS) that imports a
// base schema. The extension file must be at extPath; the base schema at
// basePath. Types from the base are available for reference resolution
// but are not part of the extension output.
func ParseExtension(basePath, extPath string) (*ExtensionBundle, error) {
	base, err := ParseFile(basePath)
	if err != nil {
		return nil, fmt.Errorf("parse base: %w", err)
	}

	extData, err := os.ReadFile(extPath)
	if err != nil {
		return nil, fmt.Errorf("read extension %s: %w", extPath, err)
	}
	var extRaw xsdSchema
	if err := xml.Unmarshal(extData, &extRaw); err != nil {
		return nil, fmt.Errorf("unmarshal extension %s: %w", extPath, err)
	}

	// Process xs:include within the extension.
	extDir := filepath.Dir(extPath)
	for _, inc := range extRaw.Includes {
		incPath := filepath.Join(extDir, inc.SchemaLocation)
		incData, err := os.ReadFile(incPath)
		if err != nil {
			return nil, fmt.Errorf("extension include %s: %w", inc.SchemaLocation, err)
		}
		var incRaw xsdSchema
		if err := xml.Unmarshal(incData, &incRaw); err != nil {
			return nil, fmt.Errorf("unmarshal extension include %s: %w", inc.SchemaLocation, err)
		}
		extRaw.SimpleTypes = append(extRaw.SimpleTypes, incRaw.SimpleTypes...)
		extRaw.ComplexTypes = append(extRaw.ComplexTypes, incRaw.ComplexTypes...)
		extRaw.Elements = append(extRaw.Elements, incRaw.Elements...)
	}

	ext := convert(&extRaw)

	return &ExtensionBundle{Base: base, Extension: ext}, nil
}

// --- Raw XSD types for encoding/xml unmarshal ---

type xsdSchema struct {
	XMLName         xml.Name         `xml:"schema"`
	TargetNamespace string           `xml:"targetNamespace,attr"`
	Version         string           `xml:"version,attr"`
	Includes        []xsdInclude     `xml:"include"`
	Imports         []xsdImport      `xml:"import"`
	SimpleTypes     []xsdSimpleType  `xml:"simpleType"`
	ComplexTypes    []xsdComplexType `xml:"complexType"`
	Elements        []xsdElement     `xml:"element"`
}

type xsdInclude struct {
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdImport struct {
	Namespace      string `xml:"namespace,attr"`
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdSimpleType struct {
	Name        string          `xml:"name,attr"`
	Annotation  *xsdAnnotation  `xml:"annotation"`
	Restriction *xsdRestriction `xml:"restriction"`
	Union       *xsdUnion       `xml:"union"`
	List        *xsdList        `xml:"list"`
}

type xsdUnion struct {
	MemberTypes string `xml:"memberTypes,attr"`
}

type xsdList struct {
	ItemType string `xml:"itemType,attr"`
}

type xsdRestriction struct {
	Base         string           `xml:"base,attr"`
	Enumerations []xsdEnumeration `xml:"enumeration"`
	MinLength    *xsdFacet        `xml:"minLength"`
	MaxLength    *xsdFacet        `xml:"maxLength"`
	Length       *xsdFacet        `xml:"length"`
	Pattern      *xsdPattern      `xml:"pattern"`
	MinInclusive *xsdFacet        `xml:"minInclusive"`
	MaxInclusive *xsdFacet        `xml:"maxInclusive"`
}

type xsdEnumeration struct {
	Value string `xml:"value,attr"`
}

type xsdFacet struct {
	Value string `xml:"value,attr"`
}

type xsdPattern struct {
	Value string `xml:"value,attr"`
}

type xsdComplexType struct {
	Name           string             `xml:"name,attr"`
	Abstract       bool               `xml:"abstract,attr"`
	Annotation     *xsdAnnotation     `xml:"annotation"`
	Sequence       *xsdSequence       `xml:"sequence"`
	ComplexContent *xsdComplexContent `xml:"complexContent"`
	Attributes     []xsdAttribute     `xml:"attribute"`
}

type xsdComplexContent struct {
	Extension *xsdExtension `xml:"extension"`
}

type xsdExtension struct {
	Base       string         `xml:"base,attr"`
	Sequence   *xsdSequence   `xml:"sequence"`
	Attributes []xsdAttribute `xml:"attribute"`
}

type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	Ref         string          `xml:"ref,attr"`
	MinOccurs   string          `xml:"minOccurs,attr"`
	MaxOccurs   string          `xml:"maxOccurs,attr"`
	Annotation  *xsdAnnotation  `xml:"annotation"`
	ComplexType *xsdComplexType `xml:"complexType"`
	SimpleType  *xsdSimpleType  `xml:"simpleType"`
}

type xsdAttribute struct {
	Name       string         `xml:"name,attr"`
	Type       string         `xml:"type,attr"`
	Use        string         `xml:"use,attr"`
	Default    string         `xml:"default,attr"`
	Annotation *xsdAnnotation `xml:"annotation"`
	SimpleType *xsdSimpleType `xml:"simpleType"`
}

type xsdAnnotation struct {
	Documentation string `xml:"documentation"`
}

// --- Conversion from raw to typed IR ---

func convert(raw *xsdSchema) *Schema {
	s := &Schema{
		Namespace: raw.TargetNamespace,
		Version:   raw.Version,
	}

	for i := range raw.SimpleTypes {
		s.SimpleTypes = append(s.SimpleTypes, convertSimpleType(&raw.SimpleTypes[i]))
	}
	for i := range raw.ComplexTypes {
		s.ComplexTypes = append(s.ComplexTypes, convertComplexType(&raw.ComplexTypes[i]))
	}
	for i := range raw.Elements {
		s.TopElements = append(s.TopElements, convertElement(&raw.Elements[i]))
	}

	return s
}

func convertSimpleType(raw *xsdSimpleType) *SimpleType {
	st := &SimpleType{
		Name: raw.Name,
		Doc:  docString(raw.Annotation),
	}
	if raw.Restriction != nil {
		st.Base = stripNS(raw.Restriction.Base)
		for _, e := range raw.Restriction.Enumerations {
			st.Enumerations = append(st.Enumerations, e.Value)
		}
		if raw.Restriction.MinLength != nil {
			st.MinLength = atoi(raw.Restriction.MinLength.Value)
		}
		if raw.Restriction.MaxLength != nil {
			st.MaxLength = atoi(raw.Restriction.MaxLength.Value)
		}
		if raw.Restriction.Length != nil {
			n := atoi(raw.Restriction.Length.Value)
			st.MinLength = n
			st.MaxLength = n
		}
		if raw.Restriction.Pattern != nil {
			st.Pattern = raw.Restriction.Pattern.Value
		}
		if raw.Restriction.MinInclusive != nil {
			st.MinInclusive = raw.Restriction.MinInclusive.Value
		}
		if raw.Restriction.MaxInclusive != nil {
			st.MaxInclusive = raw.Restriction.MaxInclusive.Value
		}
	}
	if raw.Union != nil {
		st.Base = "xs:string"
	}
	if raw.List != nil {
		st.Base = stripNS(raw.List.ItemType)
	}
	return st
}

func convertComplexType(raw *xsdComplexType) *ComplexType {
	ct := &ComplexType{
		Name:     raw.Name,
		Doc:      docString(raw.Annotation),
		Abstract: raw.Abstract,
	}

	if raw.ComplexContent != nil && raw.ComplexContent.Extension != nil {
		ext := raw.ComplexContent.Extension
		ct.ContentKind = ContentComplexExtend
		ct.BaseType = stripNS(ext.Base)
		if ext.Sequence != nil {
			for i := range ext.Sequence.Elements {
				ct.Sequence = append(ct.Sequence, convertElement(&ext.Sequence.Elements[i]))
			}
		}
		for i := range ext.Attributes {
			ct.Attributes = append(ct.Attributes, convertAttribute(&ext.Attributes[i]))
		}
	} else {
		ct.ContentKind = ContentSequence
		if raw.Sequence != nil {
			for i := range raw.Sequence.Elements {
				ct.Sequence = append(ct.Sequence, convertElement(&raw.Sequence.Elements[i]))
			}
		}
	}

	for i := range raw.Attributes {
		ct.Attributes = append(ct.Attributes, convertAttribute(&raw.Attributes[i]))
	}

	return ct
}

func convertElement(raw *xsdElement) *Element {
	el := &Element{
		Name:      raw.Name,
		Type:      stripNS(raw.Type),
		Ref:       stripNS(raw.Ref),
		MinOccurs: parseMinOccurs(raw.MinOccurs),
		MaxOccurs: parseMaxOccurs(raw.MaxOccurs),
		Doc:       docString(raw.Annotation),
	}
	if raw.ComplexType != nil {
		el.InlineComplex = convertComplexType(raw.ComplexType)
	}
	if raw.SimpleType != nil {
		el.InlineSimple = convertSimpleType(raw.SimpleType)
	}
	return el
}

func convertAttribute(raw *xsdAttribute) *Attribute {
	attr := &Attribute{
		Name:    raw.Name,
		Type:    stripNS(raw.Type),
		Use:     raw.Use,
		Default: raw.Default,
		Doc:     docString(raw.Annotation),
	}
	if attr.Use == "" {
		attr.Use = "optional"
	}
	if raw.SimpleType != nil {
		attr.InlineSimple = convertSimpleType(raw.SimpleType)
	}
	return attr
}

// --- Helpers ---

func docString(ann *xsdAnnotation) string {
	if ann == nil {
		return ""
	}
	return strings.TrimSpace(ann.Documentation)
}

// stripNS removes a namespace prefix (e.g. "sep:" or "csipaus:") but
// preserves the "xs:" prefix for XSD builtins.
func stripNS(name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "xs:") || strings.HasPrefix(name, "xsd:") {
		return name
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func parseMinOccurs(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func parseMaxOccurs(s string) int {
	if s == "" {
		return 1
	}
	if s == "unbounded" {
		return -1
	}
	return atoi(s)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
