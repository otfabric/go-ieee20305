// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"strings"
)

func (e *Emitter) emitXML() error {
	var hexTypes []hexBinaryInfo
	var intTypes []intTextInfo

	for _, st := range e.r.SimpleTypes {
		if st.Name == "" {
			continue
		}
		if st.Base == "xs:hexBinary" && st.MinLength > 0 && st.MinLength == st.MaxLength {
			hexTypes = append(hexTypes, hexBinaryInfo{
				GoName: GoTypeName(st.Name),
				Len:    st.MinLength,
			})
		}
		if isIntBase(st.Base) && !st.IsEnum() {
			intTypes = append(intTypes, intTextInfo{
				GoName: GoTypeName(st.Name),
				GoBase: resolveSimpleBase(st),
			})
		}
	}

	if len(hexTypes) == 0 && len(intTypes) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(e.header())
	fmt.Fprintf(&b, "package %s\n\n", e.pkgName)
	b.WriteString("import (\n")
	b.WriteString("\t\"encoding/hex\"\n")
	b.WriteString("\t\"encoding/xml\"\n")
	b.WriteString("\t\"fmt\"\n")
	if len(intTypes) > 0 {
		b.WriteString("\t\"strconv\"\n")
	}
	b.WriteString("\t\"strings\"\n")
	b.WriteString(")\n\n")

	for _, h := range hexTypes {
		emitHexBinaryMethods(&b, h)
	}

	for _, it := range intTypes {
		emitIntTextMethods(&b, it)
	}

	return e.writeFile("xml_gen.go", b.String())
}

type hexBinaryInfo struct {
	GoName string
	Len    int
}

type intTextInfo struct {
	GoName string
	GoBase string
}

func emitHexBinaryMethods(b *strings.Builder, h hexBinaryInfo) {
	// MarshalXML
	fmt.Fprintf(b, "// MarshalXML encodes %s as uppercase hex.\n", h.GoName)
	fmt.Fprintf(b, "func (v %s) MarshalXML(e *xml.Encoder, start xml.StartElement) error {\n", h.GoName)
	fmt.Fprintf(b, "\treturn e.EncodeElement(strings.ToUpper(hex.EncodeToString(v[:])), start)\n")
	fmt.Fprintf(b, "}\n\n")

	// UnmarshalXML
	fmt.Fprintf(b, "// UnmarshalXML decodes %s from hex text.\n", h.GoName)
	fmt.Fprintf(b, "func (v *%s) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {\n", h.GoName)
	fmt.Fprintf(b, "\tvar s string\n")
	fmt.Fprintf(b, "\tif err := d.DecodeElement(&s, &start); err != nil {\n")
	fmt.Fprintf(b, "\t\treturn err\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\ts = strings.TrimSpace(s)\n")
	fmt.Fprintf(b, "\tb, err := hex.DecodeString(s)\n")
	fmt.Fprintf(b, "\tif err != nil {\n")
	fmt.Fprintf(b, "\t\treturn fmt.Errorf(\"decode %s: %%w\", err)\n", h.GoName)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tif len(b) != %d {\n", h.Len)
	fmt.Fprintf(b, "\t\treturn fmt.Errorf(\"%s: got %%d bytes, want %d\", len(b))\n", h.GoName, h.Len)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tcopy(v[:], b)\n")
	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")

	// MarshalXMLAttr
	fmt.Fprintf(b, "// MarshalXMLAttr encodes %s as an XML attribute.\n", h.GoName)
	fmt.Fprintf(b, "func (v %s) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {\n", h.GoName)
	fmt.Fprintf(b, "\treturn xml.Attr{Name: name, Value: strings.ToUpper(hex.EncodeToString(v[:]))}, nil\n")
	fmt.Fprintf(b, "}\n\n")

	// UnmarshalXMLAttr
	fmt.Fprintf(b, "// UnmarshalXMLAttr decodes %s from an XML attribute.\n", h.GoName)
	fmt.Fprintf(b, "func (v *%s) UnmarshalXMLAttr(attr xml.Attr) error {\n", h.GoName)
	fmt.Fprintf(b, "\ts := strings.TrimSpace(attr.Value)\n")
	fmt.Fprintf(b, "\tb, err := hex.DecodeString(s)\n")
	fmt.Fprintf(b, "\tif err != nil {\n")
	fmt.Fprintf(b, "\t\treturn fmt.Errorf(\"decode %s attr: %%w\", err)\n", h.GoName)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tif len(b) != %d {\n", h.Len)
	fmt.Fprintf(b, "\t\treturn fmt.Errorf(\"%s attr: got %%d bytes, want %d\", len(b))\n", h.GoName, h.Len)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tcopy(v[:], b)\n")
	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")

	// String
	fmt.Fprintf(b, "// String returns %s as uppercase hex.\n", h.GoName)
	fmt.Fprintf(b, "func (v %s) String() string {\n", h.GoName)
	fmt.Fprintf(b, "\treturn strings.ToUpper(hex.EncodeToString(v[:]))\n")
	fmt.Fprintf(b, "}\n\n")
}

func emitIntTextMethods(b *strings.Builder, it intTextInfo) {
	// Determine the strconv function based on the Go base type.
	var parseFunc, formatExpr string
	switch it.GoBase {
	case "int64":
		parseFunc = "strconv.ParseInt(s, 10, 64)"
		formatExpr = "strconv.FormatInt(int64(v), 10)"
	case "uint64":
		parseFunc = "strconv.ParseUint(s, 10, 64)"
		formatExpr = "strconv.FormatUint(uint64(v), 10)"
	case "int32":
		parseFunc = "strconv.ParseInt(s, 10, 32)"
		formatExpr = "strconv.FormatInt(int64(v), 10)"
	case "uint32":
		parseFunc = "strconv.ParseUint(s, 10, 32)"
		formatExpr = "strconv.FormatUint(uint64(v), 10)"
	case "int16":
		parseFunc = "strconv.ParseInt(s, 10, 16)"
		formatExpr = "strconv.FormatInt(int64(v), 10)"
	case "uint16":
		parseFunc = "strconv.ParseUint(s, 10, 16)"
		formatExpr = "strconv.FormatUint(uint64(v), 10)"
	case "uint8":
		parseFunc = "strconv.ParseUint(s, 10, 8)"
		formatExpr = "strconv.FormatUint(uint64(v), 10)"
	default:
		return
	}

	// MarshalXML
	fmt.Fprintf(b, "// MarshalXML encodes %s as decimal text.\n", it.GoName)
	fmt.Fprintf(b, "func (v %s) MarshalXML(e *xml.Encoder, start xml.StartElement) error {\n", it.GoName)
	fmt.Fprintf(b, "\treturn e.EncodeElement(%s, start)\n", formatExpr)
	fmt.Fprintf(b, "}\n\n")

	// UnmarshalXML
	fmt.Fprintf(b, "// UnmarshalXML decodes %s from decimal text.\n", it.GoName)
	fmt.Fprintf(b, "func (v *%s) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {\n", it.GoName)
	fmt.Fprintf(b, "\tvar s string\n")
	fmt.Fprintf(b, "\tif err := d.DecodeElement(&s, &start); err != nil {\n")
	fmt.Fprintf(b, "\t\treturn err\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\ts = strings.TrimSpace(s)\n")
	fmt.Fprintf(b, "\tn, err := %s\n", parseFunc)
	fmt.Fprintf(b, "\tif err != nil {\n")
	fmt.Fprintf(b, "\t\treturn fmt.Errorf(\"decode %s: %%w\", err)\n", it.GoName)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\t*v = %s(n)\n", it.GoName)
	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")
}

func isIntBase(base string) bool {
	switch base {
	case "xs:long", "xs:unsignedLong",
		"xs:int", "xs:integer", "xs:unsignedInt",
		"xs:short", "xs:unsignedShort",
		"xs:unsignedByte":
		return true
	}
	return false
}
