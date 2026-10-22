// SPDX-License-Identifier: MIT

package dnsmsg

import (
	"bytes"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func sample() *Message {
	inst := JoinLabel("Meter 1.5", "_smartenergy._tcp.local")
	return &Message{
		Header: Header{ID: 7, Response: true, Opcode: 2, Authoritative: true, Truncated: true,
			RecursionDesired: true, RecursionAvailable: true, RCode: 3},
		Questions: []Question{
			{Name: "_smartenergy._tcp.local", Type: TypePTR, Class: ClassINET, Unicast: true},
			{Name: "_derp._sub._smartenergy._tcp.local", Type: TypePTR, Class: ClassINET},
		},
		Answers: []Resource{
			{Name: "_smartenergy._tcp.local", Class: ClassINET, TTL: 4500, Data: &PTR{Target: inst}},
			{Name: inst, Class: ClassINET, CacheFlush: true, TTL: 120,
				Data: &SRV{Priority: 1, Weight: 2, Port: 443, Target: "meter-1.local"}},
			{Name: inst, Class: ClassINET, CacheFlush: true, TTL: 4500,
				Data: &TXT{Strings: []string{"txtvers=1", "dcap=/dcap", ""}}},
		},
		Authorities: []Resource{
			{Name: "meter-1.local", Class: ClassINET, TTL: 120, Data: &Unknown{T: TypeNSEC, Data: []byte{1, 2, 3}}},
		},
		Additionals: []Resource{
			{Name: "meter-1.local", Class: ClassINET, CacheFlush: true, TTL: 120,
				Data: &A{Addr: netip.MustParseAddr("192.0.2.1")}},
			{Name: "meter-1.local", Class: ClassINET, CacheFlush: true, TTL: 120,
				Data: &AAAA{Addr: netip.MustParseAddr("fe80::1")}},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	m := sample()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, m)
	}
	b2, err := got.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, b2) {
		t.Error("re-encoding changed the message")
	}
}

func TestCompression(t *testing.T) {
	m := sample()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// "_smartenergy" is written once; every other occurrence is a pointer.
	if n := bytes.Count(b, []byte("_smartenergy")); n != 1 {
		t.Errorf("_smartenergy written %d times", n)
	}
	if n := bytes.Count(b, []byte("meter-1")); n != 1 {
		t.Errorf("meter-1 written %d times", n)
	}
	// The second question is _derp, then a pointer to the first name.
	q2 := 12 + len("\x0c_smartenergy\x04_tcp\x05local\x00") + 4
	want := append([]byte("\x05_derp\x04_sub"), 0xc0, 12)
	if !bytes.Equal(b[q2:q2+len(want)], want) {
		t.Errorf("second question % x, want % x", b[q2:q2+len(want)], want)
	}
}

func TestParseCompressed(t *testing.T) {
	// A hand-written response: the answer's name points to the question's,
	// and the PTR target is "x" followed by a pointer.
	b := []byte{0, 1, 0x84, 0, 0, 1, 0, 1, 0, 0, 0, 0,
		3, 'f', 'o', 'o', 5, 'l', 'o', 'c', 'a', 'l', 0, 0, 12, 0, 1,
		0xc0, 12, 0, 12, 0x80, 1, 0, 0, 0, 10, 0, 4, 1, 'x', 0xc0, 12,
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Answers[0]
	if r.Name != "foo.local" || !r.CacheFlush || r.TTL != 10 || r.Data.(*PTR).Target != "x.foo.local" {
		t.Errorf("answer %+v %+v", r, r.Data)
	}
	if !m.Response || !m.Authoritative || m.Questions[0].Name != "foo.local" {
		t.Errorf("message %+v", m)
	}
}

func TestParseErrors(t *testing.T) {
	hdr := func(qd, an byte) []byte { return []byte{0, 0, 0, 0, 0, qd, 0, an, 0, 0, 0, 0} }
	cat := func(bs ...[]byte) []byte { return bytes.Join(bs, nil) }
	rr := func(t byte, rdata ...byte) []byte {
		return cat(hdr(0, 1), []byte{0, 0, t, 0, 1, 0, 0, 0, 1, 0, byte(len(rdata))}, rdata)
	}
	long := make([]byte, 0, 300)
	for i := 0; i < 5; i++ {
		long = append(long, 63)
		long = append(long, bytes.Repeat([]byte{'a'}, 63)...)
	}
	tests := map[string][]byte{
		"short header":         {0, 0, 0},
		"truncated name":       cat(hdr(1, 0), []byte{3, 'a'}),
		"unterminated name":    cat(hdr(1, 0), []byte{1, 'a'}),
		"truncated question":   cat(hdr(1, 0), []byte{0, 0, 1}),
		"forward pointer":      cat(hdr(1, 0), []byte{0xc0, 12}),
		"truncated pointer":    cat(hdr(1, 0), []byte{0xc0}),
		"pointer loop":         cat(hdr(1, 0), []byte{1, 'a', 0xc0, 12}),
		"extended label":       cat(hdr(1, 0), []byte{0x40}),
		"long name":            cat(hdr(1, 0), long, []byte{0, 0, 1, 0, 1}),
		"truncated record":     cat(hdr(0, 1), []byte{0, 0, 1}),
		"truncated rdata":      cat(hdr(0, 1), []byte{0, 0, 1, 0, 1, 0, 0, 0, 1, 0, 9, 1}),
		"short A":              rr(1, 1, 2, 3),
		"short AAAA":           rr(28, 1, 2, 3),
		"short SRV":            rr(33, 0, 0, 0, 0, 0, 0),
		"SRV name past rdata":  rr(33, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0),
		"bad SRV name":         rr(33, 0, 0, 0, 0, 0, 0, 0x40),
		"PTR name short":       rr(12, 1, 'a', 0, 0),
		"bad PTR name":         rr(12, 0x80),
		"truncated TXT string": rr(16, 5, 'a'),
		"record bad name":      cat(hdr(0, 1), []byte{0x40}),
		"question bad name":    cat(hdr(1, 0), []byte{0x80}),
	}
	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(b); !errors.Is(err, ErrFormat) {
				t.Errorf("Parse = %v, want ErrFormat", err)
			}
		})
	}
}

func TestEncodeErrors(t *testing.T) {
	long := strings.Repeat("a", 64)
	longName := strings.Repeat("abcdefghi.", 26)
	rr := func(name string, d RData) *Message { return &Message{Answers: []Resource{{Name: name, Data: d}}} }
	tests := map[string]*Message{
		"long label":       {Questions: []Question{{Name: long + ".local"}}},
		"long name":        {Questions: []Question{{Name: longName}}},
		"empty label":      {Questions: []Question{{Name: "a..local"}}},
		"trailing escape":  {Questions: []Question{{Name: `a\`}}},
		"no data":          rr("a", nil),
		"bad record name":  rr("a..b", &PTR{Target: "x"}),
		"bad PTR target":   rr("a", &PTR{Target: ".x"}),
		"bad SRV target":   rr("a", &SRV{Target: long}),
		"long TXT":         rr("a", &TXT{Strings: []string{strings.Repeat("x", 256)}}),
		"A with IPv6":      rr("a", &A{Addr: netip.MustParseAddr("::2")}),
		"A without addr":   rr("a", &A{}),
		"AAAA without":     rr("a", &AAAA{}),
		"long rdata":       rr("a", &Unknown{T: 99, Data: make([]byte, 0x10000)}),
		"foreign data":     rr("a", foreign{}),
		"many questions":   {Questions: make([]Question, 0x10000)},
		"bad second label": {Questions: []Question{{Name: "a"}, {Name: "b..c"}}},
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := m.Encode(); !errors.Is(err, ErrFormat) {
				t.Errorf("Encode = %v, want ErrFormat", err)
			}
		})
	}
}

type foreign struct{}

func (foreign) Type() Type { return 99 }

func TestEmptyTXT(t *testing.T) {
	b, err := (&Message{Answers: []Resource{{Name: "a", Data: &TXT{}}}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Answers[0].Data.(*TXT).Strings; len(got) != 1 || got[0] != "" {
		t.Errorf("empty TXT decoded as %q", got)
	}
}

func TestAMapped(t *testing.T) {
	b, err := (&Message{Answers: []Resource{{Name: "a", Data: &A{Addr: netip.MustParseAddr("::ffff:192.0.2.9")}}}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Answers[0].Data.(*A).Addr; got != netip.MustParseAddr("192.0.2.9") {
		t.Errorf("A = %v", got)
	}
}

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"", nil},
		{".", nil},
		{"a.b.", []string{"a", "b"}},
		{`My\.Meter\\x.local`, []string{`My.Meter\x`, "local"}},
		{`a\.`, []string{"a."}},
	} {
		got, err := SplitName(tc.name)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitName(%q) = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if got := JoinLabel(`a.b\c`, "local"); got != `a\.b\\c.local` {
		t.Errorf("JoinLabel = %q", got)
	}
	if got := JoinLabel("a", ""); got != "a" {
		t.Errorf("JoinLabel = %q", got)
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Foo.LOCAL.", "foo.local", true},
		{`a\.`, "a", false},
		{`a\\.`, `a\\`, true},
		{"a", "b", false},
	} {
		if got := EqualNames(tc.a, tc.b); got != tc.want {
			t.Errorf("EqualNames(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestTypeString(t *testing.T) {
	for typ, want := range map[Type]string{TypeA: "A", TypePTR: "PTR", TypeTXT: "TXT", TypeAAAA: "AAAA",
		TypeSRV: "SRV", TypeNSEC: "NSEC", TypeANY: "ANY", 99: "TYPE99"} {
		if got := typ.String(); got != want {
			t.Errorf("%d.String() = %q", typ, got)
		}
	}
	if (Resource{}).Type() != 0 {
		t.Error("Type of a record without data")
	}
	if (Resource{Data: &Unknown{T: 99}}).Type() != 99 {
		t.Error("Type of Unknown")
	}
}

func FuzzParse(f *testing.F) {
	b, err := sample().Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte{0, 1, 0x84, 0, 0, 1, 0, 1, 0, 0, 0, 0,
		3, 'f', 'o', 'o', 5, 'l', 'o', 'c', 'a', 'l', 0, 0, 12, 0, 1,
		0xc0, 12, 0, 12, 0x80, 1, 0, 0, 0, 10, 0, 4, 1, 'x', 0xc0, 12})
	f.Add(make([]byte, 12))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			return
		}
		b1, err := m.Encode()
		if err != nil {
			t.Fatalf("Encode of a parsed message: %v", err)
		}
		m2, err := Parse(b1)
		if err != nil {
			t.Fatalf("Parse of an encoded message: %v", err)
		}
		b2, err := m2.Encode()
		if err != nil {
			t.Fatalf("second Encode: %v", err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("re-encoding is not stable:\n% x\n% x", b1, b2)
		}
	})
}
