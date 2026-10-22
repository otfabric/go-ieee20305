// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func (e *Emitter) emitTypes() error {
	var b strings.Builder
	b.WriteString(e.header())
	fmt.Fprintf(&b, "package %s\n\n", e.pkgName)

	// The xml import is needed when types use xml.Name (e.g. top-level types
	// with XMLName fields). For now we import it conditionally to avoid
	// unused-import errors; go/format will catch real issues.
	needsXML := false
	for _, ct := range e.r.ComplexTypes {
		if ct.Name != "" && !ct.Abstract {
			needsXML = true
			break
		}
	}
	if needsXML {
		b.WriteString("import \"encoding/xml\"\n\n")
		// Ensure xml is used.
		b.WriteString("var _ xml.Name\n\n")
	}

	sorted := make([]*xsd.ComplexType, len(e.r.ComplexTypes))
	copy(sorted, e.r.ComplexTypes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	for _, ct := range sorted {
		if ct.Name == "" || ct.Abstract {
			continue
		}
		e.emitComplexType(&b, ct)
	}

	return e.writeFile("types_gen.go", b.String())
}

func (e *Emitter) emitComplexType(b *strings.Builder, ct *xsd.ComplexType) {
	typeName := GoTypeName(ct.Name)

	if ct.Doc != "" {
		fmt.Fprintf(b, "// %s is generated from xs:complexType %q.\n", typeName, ct.Name)
		fmt.Fprintf(b, "//\n// %s\n", ct.Doc)
	} else {
		fmt.Fprintf(b, "// %s is generated from xs:complexType %q.\n", typeName, ct.Name)
	}

	fmt.Fprintf(b, "type %s struct {\n", typeName)

	// Embed base type for inheritance.
	if ct.BaseType != "" && !xsd.IsBuiltinXSD(ct.BaseType) {
		if _, ok := e.r.ComplexTypeMap[ct.BaseType]; ok {
			baseCT := e.r.ComplexTypeMap[ct.BaseType]
			if !baseCT.Abstract {
				fmt.Fprintf(b, "\t%s\n", GoTypeName(ct.BaseType))
			} else {
				// Flatten abstract base: emit its fields directly.
				e.emitFlattenedFields(b, baseCT, make(map[string]bool))
			}
		}
	}

	// Own sequence elements.
	seen := make(map[string]bool)
	for _, el := range ct.Sequence {
		name := el.Name
		if el.Ref != "" {
			name = el.Ref
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		e.emitField(b, el)
	}

	// Own attributes.
	for _, attr := range ct.Attributes {
		if attr.Name != "" {
			e.emitAttrField(b, attr)
		}
	}

	fmt.Fprintf(b, "}\n\n")
}

func (e *Emitter) emitFlattenedFields(b *strings.Builder, ct *xsd.ComplexType, seen map[string]bool) {
	// Recursively flatten base chain for abstract types.
	if ct.BaseType != "" && !xsd.IsBuiltinXSD(ct.BaseType) {
		if base, ok := e.r.ComplexTypeMap[ct.BaseType]; ok {
			if base.Abstract {
				e.emitFlattenedFields(b, base, seen)
			} else {
				fmt.Fprintf(b, "\t%s\n", GoTypeName(ct.BaseType))
			}
		}
	}

	for _, el := range ct.Sequence {
		name := el.Name
		if el.Ref != "" {
			name = el.Ref
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		e.emitField(b, el)
	}

	for _, attr := range ct.Attributes {
		if attr.Name != "" && !seen[attr.Name] {
			seen[attr.Name] = true
			e.emitAttrField(b, attr)
		}
	}
}

func (e *Emitter) emitField(b *strings.Builder, el *xsd.Element) {
	fieldName := el.Name
	if el.Ref != "" {
		fieldName = el.Ref
	}
	goName := goFieldName(fieldName)
	typeName := e.resolveElementType(el)
	tag := fieldName

	isSlice := el.MaxOccurs == -1 || el.MaxOccurs > 1
	isOptional := el.MinOccurs == 0 && !isSlice

	var goType string
	switch {
	case isSlice:
		goType = "[]" + typeName
	case isOptional:
		goType = "*" + typeName
	default:
		goType = typeName
	}

	omit := ""
	if el.MinOccurs == 0 {
		omit = ",omitempty"
	}

	fmt.Fprintf(b, "\t%s %s `xml:\"%s%s\"`\n", goName, goType, tag, omit)
}

func (e *Emitter) emitAttrField(b *strings.Builder, attr *xsd.Attribute) {
	goName := goFieldName(attr.Name)
	goType := e.resolveAttrType(attr)
	omit := ""
	if attr.Use != "required" {
		omit = ",omitempty"
	}
	fmt.Fprintf(b, "\t%s %s `xml:\"%s,attr%s\"`\n", goName, goType, attr.Name, omit)
}

func (e *Emitter) resolveElementType(el *xsd.Element) string {
	typeName := el.Type
	if typeName == "" && el.Ref != "" {
		if refEl, ok := e.r.ElementMap[el.Ref]; ok {
			typeName = refEl.Type
		}
	}

	if typeName == "" {
		return "string"
	}
	if xsd.IsBuiltinXSD(typeName) {
		return XSDToGoType(typeName)
	}
	if _, ok := e.r.ComplexTypeMap[typeName]; ok {
		return GoTypeName(typeName)
	}
	if _, ok := e.r.SimpleTypeMap[typeName]; ok {
		return GoTypeName(typeName)
	}
	return "string"
}

func (e *Emitter) resolveAttrType(attr *xsd.Attribute) string {
	if attr.InlineSimple != nil {
		if attr.InlineSimple.IsEnum() {
			return "string"
		}
		if attr.InlineSimple.Base != "" {
			return XSDToGoType(attr.InlineSimple.Base)
		}
		return "string"
	}
	if attr.Type == "" {
		return "string"
	}
	if xsd.IsBuiltinXSD(attr.Type) {
		return XSDToGoType(attr.Type)
	}
	if _, ok := e.r.SimpleTypeMap[attr.Type]; ok {
		return GoTypeName(attr.Type)
	}
	return "string"
}
