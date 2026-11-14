// SPDX-License-Identifier: MIT

package xml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// Marshal encodes v as a document with the given root element, adding a
// schemaVer attribute when schemaVer is not empty. Redundant namespace
// declarations, which encoding/xml writes on every element, are removed.
func Marshal(name xml.Name, schemaVer string, v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	start := xml.StartElement{Name: name}
	if schemaVer != "" {
		start.Attr = []xml.Attr{{Name: xml.Name{Local: "schemaVer"}, Value: schemaVer}}
	}
	if err := enc.EncodeElement(v, start); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return compact(buf.Bytes())
}

// Unmarshal decodes a document whose root element must be name into v.
func Unmarshal(data []byte, name xml.Name, v any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	start, err := root(d)
	if err != nil {
		return err
	}
	if start.Name != name {
		return fmt.Errorf("document element is %s, want %s", show(start.Name), show(name))
	}
	return d.DecodeElement(v, &start)
}

// Decode decodes a document into the value newElement returns for its root
// element.
func Decode(data []byte, newElement func(xml.Name) any) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	start, err := root(d)
	if err != nil {
		return nil, err
	}
	v := newElement(start.Name)
	if v == nil {
		return nil, fmt.Errorf("unknown document element %s", show(start.Name))
	}
	if err := d.DecodeElement(v, &start); err != nil {
		return nil, err
	}
	return v, nil
}

func root(d *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return xml.StartElement{}, errors.New("no document element")
		}
		if err != nil {
			return xml.StartElement{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return xml.StartElement{}, errors.New("text before the document element")
			}
		}
	}
}

func show(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return "{" + n.Space + "}" + n.Local
}

const xsiNamespace = "http://www.w3.org/2001/XMLSchema-instance"

// compact rewrites an encoding/xml document without default namespace
// declarations that repeat the one already in scope, and with the
// conventional prefix xsi for the XML Schema instance namespace.
//
// A prefix bound to the XML Schema instance namespace is written as xsi only
// where that cannot change the meaning of another name: not on an element
// that declares xsi itself or has xsi: attributes, and not below an element
// that binds xsi to another namespace. Where a renamed scope meets such an
// element, the original prefix is declared again on it.
func compact(in []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(in))
	var out bytes.Buffer
	out.Grow(len(in))
	var stack []compactScope
	cur := compactScope{}
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, cur)
			keep := cur.enter(t.Attr)
			out.WriteByte('<')
			out.WriteString(rawName(cur.renamed(t.Name)))
			for _, a := range t.Attr {
				if a.Name.Space == "" && a.Name.Local == "xmlns" {
					if a.Value == cur.ns {
						continue
					}
					cur.ns = a.Value
				}
				if a.Name.Space == "xmlns" && cur.rename != "" && a.Name.Local == cur.rename {
					a.Name.Local = "xsi"
				} else {
					a.Name = cur.renamed(a.Name)
				}
				writeAttr(&out, a)
			}
			if keep != "" {
				writeAttr(&out, xml.Attr{Name: xml.Name{Space: "xmlns", Local: keep}, Value: xsiNamespace})
			}
			out.WriteByte('>')
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("unbalanced document")
			}
			out.WriteString("</")
			out.WriteString(rawName(cur.renamed(t.Name)))
			out.WriteByte('>')
			cur = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if err := xml.EscapeText(&out, t); err != nil {
				return nil, err
			}
		case xml.Comment:
			out.WriteString("<!--")
			out.Write(t)
			out.WriteString("-->")
		case xml.ProcInst:
			out.WriteString("<?")
			out.WriteString(t.Target)
			if len(t.Inst) > 0 {
				out.WriteByte(' ')
				out.Write(t.Inst)
			}
			out.WriteString("?>")
		case xml.Directive:
			out.WriteString("<!")
			out.Write(t)
			out.WriteByte('>')
		}
	}
	return out.Bytes(), nil
}

// compactScope is the namespace state of compact inside one element.
type compactScope struct {
	ns     string // default namespace
	rename string // prefix of the XML Schema instance namespace written as xsi
	xsi    string // namespace the input binds xsi to, "" if unbound
}

// enter updates s for an element with the given raw attributes. It returns
// a prefix that must be declared again for the XML Schema instance
// namespace on the element because a rename ends there.
func (s *compactScope) enter(attrs []xml.Attr) (keep string) {
	declXSI, usesXSI := false, false
	xsiValue := ""
	redeclared, redeclValue := false, ""
	for _, a := range attrs {
		switch {
		case a.Name.Space == "xmlns" && a.Name.Local == "xsi":
			declXSI, xsiValue = true, a.Value
		case a.Name.Space == "xmlns" && s.rename != "" && a.Name.Local == s.rename:
			redeclared, redeclValue = true, a.Value
		case a.Name.Space == "xsi":
			usesXSI = true
		}
	}
	if s.rename != "" && ((redeclared && redeclValue != xsiNamespace) || declXSI || usesXSI) {
		if !redeclared {
			keep = s.rename
		}
		s.rename = ""
	}
	if declXSI {
		s.xsi = xsiValue
	}
	if s.rename != "" || keep != "" || declXSI || usesXSI || (s.xsi != "" && s.xsi != xsiNamespace) {
		return keep
	}
	for _, a := range attrs {
		if a.Name.Space == "xmlns" && a.Value == xsiNamespace && a.Name.Local != "xsi" {
			s.rename = a.Name.Local
			break
		}
	}
	return keep
}

// renamed returns n with the renamed prefix replaced by xsi.
func (s *compactScope) renamed(n xml.Name) xml.Name {
	if s.rename != "" && n.Space == s.rename {
		n.Space = "xsi"
	}
	return n
}

func writeAttr(out *bytes.Buffer, a xml.Attr) {
	out.WriteByte(' ')
	out.WriteString(rawName(a.Name))
	out.WriteString(`="`)
	_ = xml.EscapeText(out, []byte(a.Value)) // a bytes.Buffer does not fail
	out.WriteByte('"')
}

func rawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}
