// SPDX-License-Identifier: MIT

package ieee20305

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
)

// ExtensionElement is an element in a place where the schema admits elements
// it does not declare (xs:any): extensions in other namespaces, such as
// CSIP-AUS elements read through this package, and elements of later
// revisions of IEEE 2030.5. Decoding keeps them, so that a document can be
// encoded again without losing them.
//
// The content is kept as XML that declares every namespace its element and
// attribute names use, so it means the same wherever it is written: names in
// the content keep their namespaces, including namespaces declared on the
// element itself or on an enclosing element of the document. Prefixes in
// the content may differ from the ones the document used. Namespace
// declarations made inside the content are kept as written; prefixes that
// appear only in attribute values or text (such as QName values) are bound
// only when the content declares them itself.
type ExtensionElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	InnerXML []byte     `xml:",innerxml"`
}

// Namespaces with conventional prefixes.
const (
	xmlNamespace = "http://www.w3.org/XML/1998/namespace"
	xsiNamespace = "http://www.w3.org/2001/XMLSchema-instance"
)

// UnmarshalXML decodes the element. Namespace declarations are dropped
// from its attributes: the encoder declares the namespace of the element and
// its attributes itself, and the content is rewritten to declare the
// namespaces it uses. An element with two attributes of the same name in the
// same namespace is refused.
func (e *ExtensionElement) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	if err := uniqueAttrs(start.Attr); err != nil {
		return err
	}
	e.XMLName = start.Name
	var attrs []xml.Attr
	for _, a := range start.Attr {
		if isNamespaceDecl(a.Name) {
			continue
		}
		attrs = append(attrs, a)
	}
	e.Attrs = attrs
	w := nsWriter{def: start.Name.Space, defKnown: start.Name.Space != ""}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := w.start(t); err != nil {
				return err
			}
		case xml.EndElement:
			if len(w.scopes) == 0 {
				e.InnerXML = append([]byte{}, w.buf.Bytes()...)
				return nil
			}
			w.end()
		case xml.CharData:
			_ = xml.EscapeText(&w.buf, t) // a bytes.Buffer does not fail
		case xml.Comment:
			w.buf.WriteString("<!--")
			w.buf.Write(t)
			w.buf.WriteString("-->")
		case xml.ProcInst:
			w.buf.WriteString("<?")
			w.buf.WriteString(t.Target)
			if len(t.Inst) > 0 {
				w.buf.WriteByte(' ')
				w.buf.Write(t.Inst)
			}
			w.buf.WriteString("?>")
		}
	}
}

func isNamespaceDecl(n xml.Name) bool {
	return n.Space == "xmlns" || (n.Space == "" && n.Local == "xmlns")
}

func uniqueAttrs(attrs []xml.Attr) error {
	if len(attrs) < 2 {
		return nil
	}
	seen := make(map[xml.Name]struct{}, len(attrs))
	for _, a := range attrs {
		if _, dup := seen[a.Name]; dup {
			return fmt.Errorf("duplicate attribute {%s}%s", a.Name.Space, a.Name.Local)
		}
		seen[a.Name] = struct{}{}
	}
	return nil
}

// nsWriter writes decoded (namespace-resolved) tokens as XML whose names
// are all bound by declarations it writes itself.
type nsWriter struct {
	buf      bytes.Buffer
	def      string // default namespace in scope of the output
	defKnown bool   // whether def is known; false outside any declaration
	prefixes map[string]string
	byURL    map[string]string
	undo     []nsUndo
	scopes   []nsScope
	seq      int
}

type nsScope struct {
	def      string
	defKnown bool
	undo     int
	name     string
}

type nsUndo struct {
	byURL bool
	key   string
	old   string
	had   bool
}

func (w *nsWriter) bind(prefix, url string) {
	if w.prefixes == nil {
		w.prefixes = make(map[string]string)
		w.byURL = make(map[string]string)
	}
	old, had := w.prefixes[prefix]
	w.undo = append(w.undo, nsUndo{key: prefix, old: old, had: had})
	w.prefixes[prefix] = url
	old, had = w.byURL[url]
	w.undo = append(w.undo, nsUndo{byURL: true, key: url, old: old, had: had})
	w.byURL[url] = prefix
}

// lookup returns a prefix bound to url in the output, or "".
func (w *nsWriter) lookup(url string) string {
	if url == xmlNamespace {
		return "xml"
	}
	if p, ok := w.byURL[url]; ok && w.prefixes[p] == url {
		return p
	}
	return ""
}

// declare binds a new prefix to url and writes its declaration.
func (w *nsWriter) declare(url string) string {
	p := "xsi"
	if url != xsiNamespace || w.prefixes[p] != "" {
		for {
			w.seq++
			p = "ns" + strconv.Itoa(w.seq)
			if w.prefixes[p] == "" {
				break
			}
		}
	}
	w.bind(p, url)
	w.attr("xmlns:"+p, url)
	return p
}

func (w *nsWriter) attr(name, value string) {
	w.buf.WriteByte(' ')
	w.buf.WriteString(name)
	w.buf.WriteString(`="`)
	_ = xml.EscapeText(&w.buf, []byte(value)) // a bytes.Buffer does not fail
	w.buf.WriteByte('"')
}

func (w *nsWriter) start(t xml.StartElement) error {
	if err := uniqueAttrs(t.Attr); err != nil {
		return err
	}
	w.scopes = append(w.scopes, nsScope{def: w.def, defKnown: w.defKnown, undo: len(w.undo)})
	// Declarations made on the element apply to its own name.
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" && a.Name.Local != "xml" && a.Name.Local != "xmlns" && a.Value != "" {
			w.bind(a.Name.Local, a.Value)
		}
	}
	name, setDefault := t.Name.Local, false
	switch {
	case w.defKnown && t.Name.Space == w.def:
	case t.Name.Space == "":
		setDefault = true
	default:
		if p := w.lookup(t.Name.Space); p != "" {
			name = p + ":" + name
		} else {
			setDefault = true
		}
	}
	w.scopes[len(w.scopes)-1].name = name
	w.buf.WriteByte('<')
	w.buf.WriteString(name)
	if setDefault {
		w.def, w.defKnown = t.Name.Space, true
		w.attr("xmlns", t.Name.Space)
	}
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" {
			if a.Name.Local != "xml" && a.Name.Local != "xmlns" && a.Value != "" {
				w.attr("xmlns:"+a.Name.Local, a.Value)
			}
			continue
		}
		if a.Name.Space == "" {
			if a.Name.Local != "xmlns" {
				w.attr(a.Name.Local, a.Value)
			}
			continue
		}
		p := w.lookup(a.Name.Space)
		if p == "" {
			p = w.declare(a.Name.Space)
		}
		w.attr(p+":"+a.Name.Local, a.Value)
	}
	w.buf.WriteByte('>')
	return nil
}

func (w *nsWriter) end() {
	s := w.scopes[len(w.scopes)-1]
	w.scopes = w.scopes[:len(w.scopes)-1]
	w.buf.WriteString("</")
	w.buf.WriteString(s.name)
	w.buf.WriteByte('>')
	for i := len(w.undo) - 1; i >= s.undo; i-- {
		u := w.undo[i]
		m := w.prefixes
		if u.byURL {
			m = w.byURL
		}
		if u.had {
			m[u.key] = u.old
		} else {
			delete(m, u.key)
		}
	}
	w.undo = w.undo[:s.undo]
	w.def, w.defKnown = s.def, s.defKnown
}
