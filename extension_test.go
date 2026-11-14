// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// An element the schema admits without declaring is kept: its name, its
// attributes without the namespace declarations, and its content.
func TestExtensionElement(t *testing.T) {
	doc := `<EndDevice xmlns="urn:ieee:std:2030.5:ns" href="/edev/1">` +
		`<x:Site xmlns:x="urn:example:site" x:id="7" kind="pv"><x:nmi>4407123456</x:nmi></x:Site>` +
		`<Bare xmlns="urn:example:bare"/>` +
		`<sFDI>123456789018</sFDI><changedTime>0</changedTime></EndDevice>`
	var ed ieee20305.EndDevice
	if err := ieee20305.Unmarshal([]byte(doc), &ed); err != nil {
		t.Fatal(err)
	}
	if len(ed.Extensions) != 2 {
		t.Fatalf("extensions %+v", ed.Extensions)
	}
	site, bare := ed.Extensions[0], ed.Extensions[1]
	if site.XMLName != (xml.Name{Space: "urn:example:site", Local: "Site"}) {
		t.Errorf("name %v", site.XMLName)
	}
	// xmlns:x is dropped; x:id and kind are kept.
	if len(site.Attrs) != 2 || site.Attrs[0].Name.Local != "id" || site.Attrs[0].Value != "7" ||
		site.Attrs[1].Name.Local != "kind" || site.Attrs[1].Value != "pv" {
		t.Errorf("attributes %+v", site.Attrs)
	}
	if !strings.Contains(string(site.InnerXML), "4407123456") {
		t.Errorf("content %q", site.InnerXML)
	}
	// The default namespace declaration is dropped too: no attributes left.
	if bare.Attrs != nil || bare.XMLName.Space != "urn:example:bare" {
		t.Errorf("bare %+v", bare)
	}

	// Encoded again, the extensions come back with their namespaces.
	out, err := ieee20305.Marshal(&ed)
	if err != nil {
		t.Fatal(err)
	}
	var again ieee20305.EndDevice
	if err := ieee20305.Unmarshal(out, &again); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(again.Extensions) != 2 || again.Extensions[0].XMLName != site.XMLName ||
		!strings.Contains(string(again.Extensions[0].InnerXML), "4407123456") {
		t.Errorf("round trip: %+v\n%s", again.Extensions, out)
	}
}

// A malformed extension fails the document.
func TestExtensionElementMalformed(t *testing.T) {
	var e ieee20305.ExtensionElement
	d := xml.NewDecoder(strings.NewReader(`<x:Site xmlns:x="urn:x"><x:nmi></x:Site>`))
	tok, err := d.Token()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.UnmarshalXML(d, tok.(xml.StartElement)); err == nil {
		t.Error("a malformed extension decoded")
	}
}

// nsCheck reports whether doc is namespace-well-formed: every prefix of an
// element or attribute name is declared, and no element has two attributes
// with the same raw or expanded name.
func nsCheck(doc []byte) error {
	d := xml.NewDecoder(strings.NewReader(string(doc)))
	scopes := []map[string]string{{"xml": "http://www.w3.org/XML/1998/namespace"}}
	resolve := func(p string) (string, bool) {
		for i := len(scopes) - 1; i >= 0; i-- {
			if u, ok := scopes[i][p]; ok {
				return u, u != ""
			}
		}
		return "", false
	}
	for {
		tok, err := d.RawToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s := map[string]string{}
			raw := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if raw[a.Name] {
					return fmt.Errorf("<%s>: attribute %s:%s twice", t.Name.Local, a.Name.Space, a.Name.Local)
				}
				raw[a.Name] = true
				if a.Name.Space == "xmlns" {
					s[a.Name.Local] = a.Value
				}
			}
			scopes = append(scopes, s)
			if t.Name.Space != "" {
				if _, ok := resolve(t.Name.Space); !ok {
					return fmt.Errorf("<%s:%s>: prefix not declared", t.Name.Space, t.Name.Local)
				}
			}
			expanded := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if a.Name.Space == "" || a.Name.Space == "xmlns" {
					continue
				}
				u, ok := resolve(a.Name.Space)
				if !ok {
					return fmt.Errorf("<%s>: attribute prefix %s not declared", t.Name.Local, a.Name.Space)
				}
				n := xml.Name{Space: u, Local: a.Name.Local}
				if expanded[n] {
					return fmt.Errorf("<%s>: attribute {%s}%s twice", t.Name.Local, u, a.Name.Local)
				}
				expanded[n] = true
			}
		case xml.EndElement:
			scopes = scopes[:len(scopes)-1]
		}
	}
}

// names returns the expanded names of the elements and attributes of doc,
// as {space}local, in document order.
func names(t *testing.T, doc []byte) []string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(string(doc)))
	var out []string
	for {
		tok, err := d.Token()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			return out
		}
		if s, ok := tok.(xml.StartElement); ok {
			out = append(out, "{"+s.Name.Space+"}"+s.Name.Local)
			for _, a := range s.Attr {
				if a.Name.Space != "xmlns" && (a.Name.Space != "" || a.Name.Local != "xmlns") {
					out = append(out, "@{"+a.Name.Space+"}"+a.Name.Local+"="+a.Value)
				}
			}
		}
	}
}

// Content of an extension keeps the namespaces of its names when it is
// encoded again, wherever the document declared them.
func TestExtensionElementNamespaces(t *testing.T) {
	const xsi = "http://www.w3.org/2001/XMLSchema-instance"
	for name, ext := range map[string]string{
		"prefix declared on the element": `<p:z xmlns:p="urn:p"><p:w/></p:z>`,
		"prefix declared on the root":    `<c:x c:k="1"><value>1</value><c:y c:a="2" xml:lang="en"/></c:x>`,
		"declared inside":                `<c:x><q:t xmlns:q="urn:q" xmlns:xsi="` + xsi + `" xsi:type="q:T"/></c:x>`,
		"xsi from the root":              `<c:x><c:y xsi:nil="true"/><value i:type="c:T" xmlns:i="` + xsi + `"/></c:x>`,
		"other default on the element":   `<c:x xmlns="urn:d"><a><b xmlns="">t</b></a><c:y/></c:x>`,
		"generated prefix taken":         `<c:x><ns1:a xmlns:ns1="urn:n" c:k="1"><xsi:b xmlns:xsi="urn:o" c:k="2" i:t="3" xmlns:i="` + xsi + `"/></ns1:a></c:x>`,
		"shadowed prefix":                `<c:x><c:a xmlns:c="urn:other" xmlns:d="urn:c"><d:b d:k="1"/></c:a></c:x>`,
		"no namespace":                   `<c:x><plain xmlns="" a="1"><more/></plain></c:x>`,
		"markup":                         `<c:x><!-- c --><?pi data?><![CDATA[a<b]]>&amp;</c:x>`,
		"default equals element":         `<c:x><c:y><c:z/></c:y></c:x>`,
	} {
		doc := `<EndDevice xmlns="urn:ieee:std:2030.5:ns" xmlns:c="urn:c" xmlns:xsi="` + xsi + `">` +
			ext + `<sFDI>1</sFDI></EndDevice>`
		var ed ieee20305.EndDevice
		if err := ieee20305.Unmarshal([]byte(doc), &ed); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := names(t, []byte(doc))
		for _, e := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
			out, err := ieee20305.MarshalEdition(&ed, e)
			if err != nil {
				t.Fatalf("%s %v: %v", name, e, err)
			}
			if err := nsCheck(out); err != nil {
				t.Errorf("%s %v: %v\n%s", name, e, err, out)
			}
			got := names(t, out)
			if g, w := strings.Join(extension(got), " "), strings.Join(extension(want), " "); g != w {
				t.Errorf("%s %v: names\n%s\nwant\n%s\n%s", name, e, g, w, out)
			}
		}
	}
}

// extension returns the names of the extension, which the encoder writes
// first: those after the root element and its attributes, up to sFDI.
func extension(ns []string) []string {
	i := 1
	for i < len(ns) && strings.HasPrefix(ns[i], "@") {
		i++
	}
	for j := i; j < len(ns); j++ {
		if strings.HasSuffix(ns[j], "}sFDI") {
			return ns[i:j]
		}
	}
	return nil
}

// QName values and text keep the prefixes the content declares.
func TestExtensionElementKeepsContentDeclarations(t *testing.T) {
	doc := `<EndDevice xmlns="urn:ieee:std:2030.5:ns"><sFDI>1</sFDI>` +
		`<c:x xmlns:c="urn:c"><q:t xmlns:q="urn:q" kind="q:T">q:v</q:t></c:x></EndDevice>`
	var ed ieee20305.EndDevice
	if err := ieee20305.Unmarshal([]byte(doc), &ed); err != nil {
		t.Fatal(err)
	}
	inner := string(ed.Extensions[0].InnerXML)
	if !strings.Contains(inner, `xmlns:q="urn:q"`) || !strings.Contains(inner, `kind="q:T"`) || !strings.Contains(inner, `>q:v<`) {
		t.Errorf("content %s", inner)
	}
}

// An element with the same attribute twice, by raw or expanded name, is
// refused, at the extension and inside it.
func TestExtensionElementDuplicateAttributes(t *testing.T) {
	for _, ext := range []string{
		`<c:x xmlns:a="urn:u" xmlns:b="urn:u" a:k="1" b:k="2"/>`,
		`<c:x><c:y xmlns:a="urn:u" xmlns:b="urn:u" a:k="1" b:k="2"/></c:x>`,
	} {
		doc := `<EndDevice xmlns="urn:ieee:std:2030.5:ns" xmlns:c="urn:c"><sFDI>1</sFDI>` + ext + `</EndDevice>`
		var ed ieee20305.EndDevice
		if err := ieee20305.Unmarshal([]byte(doc), &ed); err == nil {
			t.Errorf("%s decoded: %+v", ext, ed.Extensions)
		}
	}
}

// A malformed document inside an extension fails the document.
func TestExtensionElementMalformedContent(t *testing.T) {
	var e ieee20305.ExtensionElement
	d := xml.NewDecoder(strings.NewReader(`<x:Site xmlns:x="urn:x"><x:a></x:b></x:Site>`))
	tok, err := d.Token()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.UnmarshalXML(d, tok.(xml.StartElement)); err == nil {
		t.Error("a malformed extension decoded")
	}
}
