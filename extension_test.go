// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"encoding/xml"
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
