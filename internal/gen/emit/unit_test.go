// SPDX-License-Identifier: MIT

package emit

import (
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func TestLiteral(t *testing.T) {
	for _, c := range []struct{ builtin, in, want string }{
		{"string", " a ", `"a"`},
		{"boolean", "1", "true"},
		{"boolean", "0", "false"},
		{"hexBinary", "0a0B", "T{0x0A, 0x0B}"},
		{"byte", "-5", "-5"},
		{"unsignedShort", "7", "7"},
	} {
		if got, err := literal(c.builtin, c.in, "T"); err != nil || got != c.want {
			t.Errorf("literal(%s, %q) = %q, %v; want %q", c.builtin, c.in, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"boolean", "maybe"}, {"hexBinary", "zz"}, {"byte", "300"}, {"unsignedByte", "-1"}} {
		if _, err := literal(c[0], c[1], "T"); err == nil {
			t.Errorf("literal(%s, %q) succeeded", c[0], c[1])
		}
	}
}

func ip(n int) *int { return &n }

func TestHexSample(t *testing.T) {
	q := xsd.QName{Local: "H"}
	s, err := hexSample(q, xsd.Facets{}, []string{"0A"}, "H")
	if err != nil || s.valid != "H{0x0A}" || len(s.invalid) != 1 {
		t.Errorf("enumeration: %+v, %v", s, err)
	}
	if _, err := hexSample(q, xsd.Facets{}, []string{"zz"}, "H"); err == nil {
		t.Error("a malformed enumeration was accepted")
	}
	s, err = hexSample(q, xsd.Facets{Length: ip(2)}, nil, "H")
	if err != nil || s.valid != "H{0x01, 0x01}" || len(s.invalid) != 1 {
		t.Errorf("length: %+v, %v", s, err)
	}
	s, err = hexSample(q, xsd.Facets{MinLength: ip(3), MaxLength: ip(2)}, nil, "H")
	if err != nil || s.valid != "H{0x01, 0x01}" || len(s.invalid) != 2 {
		t.Errorf("min and max length: %+v, %v", s, err)
	}
}

func TestIntSample(t *testing.T) {
	q := xsd.QName{Local: "I"}
	id := func(s string) string { return s }
	if _, err := intSample(q, "decimal", xsd.Facets{}, nil, id); err == nil {
		t.Error("xs:decimal has example values")
	}
	bad := "1.5"
	if _, err := intSample(q, "int", xsd.Facets{MinInclusive: &bad}, nil, id); err == nil {
		t.Error("a decimal minInclusive was accepted")
	}
	if _, err := intSample(q, "int", xsd.Facets{MaxInclusive: &bad}, nil, id); err == nil {
		t.Error("a decimal maxInclusive was accepted")
	}
	lo, hi := "0", "1"
	s, err := intSample(q, "unsignedByte", xsd.Facets{MinInclusive: &lo, MaxInclusive: &hi}, []string{"0", " 1 "}, id)
	if err != nil || s.valid != "0" || len(s.invalid) != 0 {
		t.Errorf("enumeration of the whole range: %+v, %v", s, err)
	}
	lo = "5"
	s, err = intSample(q, "int", xsd.Facets{MinInclusive: &lo}, nil, id)
	if err != nil || s.valid != "5" || strings.Join(s.invalid, ",") != "4" {
		t.Errorf("minInclusive above 1: %+v, %v", s, err)
	}
	hi = "-5"
	s, err = intSample(q, "int", xsd.Facets{MaxInclusive: &hi}, nil, id)
	if err != nil || s.valid != "-5" || strings.Join(s.invalid, ",") != "-4" {
		t.Errorf("maxInclusive below 1: %+v, %v", s, err)
	}
}
