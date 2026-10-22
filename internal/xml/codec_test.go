// SPDX-License-Identifier: MIT

package xml

import (
	"encoding/xml"
	"strings"
	"testing"
)

type power struct {
	Value int `xml:"urn:a value"`
}

type doc struct {
	Href  string `xml:"href,attr,omitempty"`
	Power power  `xml:"urn:a power"`
	Ext   *power `xml:"urn:b ext,omitempty"`
	Note  string `xml:"urn:a note,omitempty"`
}

var docName = xml.Name{Space: "urn:a", Local: "Doc"}

func TestMarshalCompacts(t *testing.T) {
	out, err := Marshal(docName, "2.2", doc{Href: "/d", Power: power{1}, Ext: &power{2}, Note: "a<b & \"c\""})
	if err != nil {
		t.Fatal(err)
	}
	want := `<Doc xmlns="urn:a" schemaVer="2.2" href="/d"><power><value>1</value></power>` +
		`<ext xmlns="urn:b"><value xmlns="urn:a">2</value></ext><note>a&lt;b &amp; &#34;c&#34;</note></Doc>`
	if string(out) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", out, want)
	}
	var back doc
	if err := Unmarshal(out, docName, &back); err != nil || back.Ext.Value != 2 || back.Note != `a<b & "c"` {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
}

func TestMarshalWithoutSchemaVer(t *testing.T) {
	out, err := Marshal(docName, "", doc{})
	if err != nil || strings.Contains(string(out), "schemaVer") {
		t.Errorf("Marshal = %s, %v", out, err)
	}
}

type typed struct {
	Type string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
}

func TestCompactNamesXSI(t *testing.T) {
	out, err := Marshal(docName, "", struct {
		R typed `xml:"urn:a R"`
	}{typed{"Reading"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<R xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Reading">`) {
		t.Errorf("xsi prefix not used: %s", out)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	var d doc
	for name, in := range map[string]string{
		"wrong root":  `<Other xmlns="urn:a"/>`,
		"wrong space": `<Doc xmlns="urn:x"/>`,
		"empty":       ``,
		"text first":  `junk<Doc xmlns="urn:a"/>`,
		"malformed":   `<Doc xmlns="urn:a">`,
	} {
		if err := Unmarshal([]byte(in), docName, &d); err == nil {
			t.Errorf("%s: Unmarshal succeeded", name)
		}
	}
	if err := Unmarshal([]byte(`<?xml version="1.0"?>`+"\n"+`<Doc xmlns="urn:a"><power><value>3</value></power></Doc>`), docName, &d); err != nil || d.Power.Value != 3 {
		t.Errorf("declaration before the root: %v", err)
	}
}

func TestDecode(t *testing.T) {
	newElement := func(n xml.Name) any {
		if n == docName {
			return new(doc)
		}
		return nil
	}
	v, err := Decode([]byte(`<Doc xmlns="urn:a" href="/x"/>`), newElement)
	if err != nil || v.(*doc).Href != "/x" {
		t.Errorf("Decode = %#v, %v", v, err)
	}
	if _, err := Decode([]byte(`<Nope xmlns="urn:a"/>`), newElement); err == nil || !strings.Contains(err.Error(), "{urn:a}Nope") {
		t.Errorf("unknown root: %v", err)
	}
}

func TestMarshalUnsupported(t *testing.T) {
	if _, err := Marshal(docName, "", make(chan int)); err == nil {
		t.Error("Marshal of a channel succeeded")
	}
}

func TestCompactKeepsMarkup(t *testing.T) {
	in := `<?xml version="1.0"?><!DOCTYPE Doc><?pi?><Doc xmlns="urn:a"><!-- c --><a xmlns="urn:a">x</a></Doc>`
	out, err := compact([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0"?><!DOCTYPE Doc><?pi?><Doc xmlns="urn:a"><!-- c --><a>x</a></Doc>`
	if string(out) != want {
		t.Errorf("compact =\n%s\nwant\n%s", out, want)
	}
}

func TestCompactErrors(t *testing.T) {
	for name, in := range map[string]string{
		"unbalanced": `</Doc>`,
		"malformed":  `<Doc a=>`,
	} {
		if _, err := compact([]byte(in)); err == nil {
			t.Errorf("%s: compact succeeded", name)
		}
	}
}
