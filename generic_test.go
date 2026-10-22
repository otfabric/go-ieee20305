// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
)

// Every link of every type points to a resource of the WADL.
func TestLinkResources(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range examples.All(3) {
		for _, l := range ieee20305.Links(v) {
			if l.Resource == "" || seen[l.Type] {
				continue
			}
			seen[l.Type] = true
			if _, ok := ieee20305.LookupResource(l.Resource); !ok {
				t.Errorf("%s (in %T) points to %s, which is no resource of the WADL", l.Type, v, l.Resource)
			}
		}
	}
	if len(seen) < 50 {
		t.Errorf("only %d link types seen", len(seen))
	}
	if ieee20305.Links(struct{}{}) != nil {
		t.Error("Links of a foreign value")
	}
}

func TestLinksOfDERControl(t *testing.T) {
	var dc ieee20305.DERControl
	dc.DERControlBase.OpModFreqWatt = &ieee20305.DERCurveLink{Link: ieee20305.Link{Href: "/derp/1/dc/1"}}
	links := ieee20305.Links(&dc)
	if len(links) != 1 || links[0].Resource != "DERCurve" || links[0].Element != "opModFreqWatt" || links[0].Href != "/derp/1/dc/1" {
		t.Errorf("Links = %+v", links)
	}
}

func TestDerives(t *testing.T) {
	for _, c := range []struct {
		elem, of string
		want     bool
	}{
		{"DERControlResponse", "Response", true},
		{"DrResponse", "Response", true},
		{"Response", "DERControlResponse", false},
		{"EndDevice", "EndDevice", true},
		{"EndDevice", "Response", false},
		{"NoSuchElement", "Response", false},
	} {
		if got := ieee20305.Derives(c.elem, c.of); got != c.want {
			t.Errorf("Derives(%q, %q) = %v", c.elem, c.of, got)
		}
	}
	if b, ok := ieee20305.TypeBase("DERControlResponse"); !ok || b != "Response" {
		t.Errorf("TypeBase(DERControlResponse) = %q, %v", b, ok)
	}
}

func TestCloneDoesNotAlias(t *testing.T) {
	ed := examples.EndDevice(2)
	c := ed.Clone()
	(*c.LFDI)[0] ^= 0xFF
	c.FunctionSetAssignmentsListLink.Href = "/changed"
	if (*ed.LFDI)[0] == (*c.LFDI)[0] || ed.FunctionSetAssignmentsListLink.Href == "/changed" {
		t.Error("the clone shares memory with the original")
	}
	if (*ieee20305.EndDevice)(nil).Clone() != nil {
		t.Error("Clone of nil")
	}
	ext := ieee20305.CloneExtensions([]ieee20305.ExtensionElement{{InnerXML: []byte("x"), Attrs: nil}})
	if string(ext[0].InnerXML) != "x" || ieee20305.CloneExtensions(nil) != nil {
		t.Error("CloneExtensions")
	}
}
