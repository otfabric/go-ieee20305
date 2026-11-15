// SPDX-License-Identifier: MIT

package dnsmsg

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// nameOfWire returns a name whose wire form is n bytes (n >= 2), of labels
// of at most 63 bytes.
func nameOfWire(n int) string {
	var labels []string
	left := n - 1 // the root label
	for left > 0 {
		l := min(63, left-1)
		if left-1-l == 1 {
			l-- // no label of length 0 left over
		}
		labels = append(labels, strings.Repeat("a", l))
		left -= 1 + l
	}
	return strings.Join(labels, ".")
}

// Labels of 63 bytes and names of 255 are the longest the encoder and the
// decoder take.
func TestNameLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
		{nameOfWire(255), true},
		{nameOfWire(256), false},
		{strings.Repeat(`\.`, 63), true},
		{strings.Repeat(`\.`, 64), false},
	} {
		m := &Message{Questions: []Question{{Name: c.name, Type: TypeA, Class: ClassINET}}}
		b, err := m.Encode()
		if c.ok != (err == nil) {
			t.Errorf("Encode of a name of %d bytes: %v", len(c.name), err)
			continue
		}
		if err != nil {
			continue
		}
		got, err := Parse(b)
		if err != nil || got.Questions[0].Name != c.name {
			t.Errorf("round trip of %d bytes: %v", len(c.name), err)
		}
	}
}

// A name that pointers make longer than 255 bytes is refused, though each
// of its parts is short: question k is a label of 63 bytes and a pointer
// to the name of question k-1, so the fourth expands to 4*64+1 = 257 bytes.
func TestCompressedNameTooLong(t *testing.T) {
	for _, n := range []int{3, 4} {
		b := []byte{0, 0, 0x84, 0, 0, byte(n), 0, 0, 0, 0, 0, 0}
		prev := -1
		for range n {
			at := len(b)
			b = append(b, 63)
			b = append(b, bytes.Repeat([]byte{'x'}, 63)...)
			if prev < 0 {
				b = append(b, 0)
			} else {
				b = append(b, 0xc0|byte(prev>>8), byte(prev))
			}
			b = append(b, 0, 1, 0, 1)
			prev = at
		}
		_, err := Parse(b)
		if n == 3 && err != nil {
			t.Errorf("Parse of a 193-byte compressed name: %v", err)
		}
		if n == 4 && !errors.Is(err, ErrFormat) {
			t.Errorf("Parse of a 257-byte compressed name: %v", err)
		}
	}
}

// A chain of pointers, each to the name of the question before, resolves
// up to maxHops pointers per name; a longer chain is refused, as is a
// pointer that does not point before the previous one.
func TestPointerChain(t *testing.T) {
	chain := func(n int) []byte {
		b := []byte{0, 0, 0x84, 0, byte(n >> 8), byte(n & 0xff), 0, 0, 0, 0, 0, 0}
		prev := len(b)
		b = append(b, 0, 0, 1, 0, 1)
		for range n - 1 {
			at := len(b)
			b = append(b, 0xc0|byte(prev>>8), byte(prev), 0, 1, 0, 1)
			prev = at
		}
		return b
	}
	n := maxHops + 1
	m, err := Parse(chain(n))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Questions) != n || m.Questions[n-1].Name != "" {
		t.Errorf("%d questions", len(m.Questions))
	}
	if _, err := Parse(chain(n + 1)); !errors.Is(err, ErrFormat) {
		t.Errorf("Parse of a chain of %d pointers: %v", n, err)
	}
	// A pointer to itself, or to the pointer after it, is refused.
	for _, loop := range [][]byte{{0xc0, 12}, {0xc0, 14, 0xc0, 12}} {
		b := append([]byte{0, 0, 0x84, 0, 0, 1, 0, 0, 0, 0, 0, 0}, loop...)
		if _, err := Parse(append(b, 0, 1, 0, 1)); !errors.Is(err, ErrFormat) {
			t.Errorf("Parse of a pointer loop % x: %v", loop, err)
		}
	}
}

// A message of many questions that each point at one name of 127 labels
// decodes with a bounded number of allocations per question: decoding a
// label does not allocate.
func TestManyPointersToLongName(t *testing.T) {
	const n = 1454
	b := []byte{0, 0, 0, 0, byte(n >> 8), byte(n & 0xff), 0, 0, 0, 0, 0, 0}
	for range 127 {
		b = append(b, 1, 'a')
	}
	b = append(b, 0, 0, 1, 0, 1)
	for range n - 1 {
		b = append(b, 0xc0, 12, 0, 1, 0, 1)
	}
	if len(b) > 9000 {
		t.Fatalf("message of %d bytes", len(b))
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Questions) != n || len(m.Questions[n-1].Name) != 253 {
		t.Fatalf("%d questions", len(m.Questions))
	}
	allocs := testing.AllocsPerRun(3, func() { _, _ = Parse(b) })
	if allocs > 8*n {
		t.Errorf("%v allocations for %d questions", allocs, n)
	}
}

// Labels with dots and backslashes are escaped in decoded names, as
// JoinLabel escapes them.
func TestDecodeEscapes(t *testing.T) {
	for _, l := range []string{"plain", "a.b", `a\b`, `.\.`} {
		b, err := (&Message{Questions: []Question{{Name: JoinLabel(l, "local"), Type: TypePTR, Class: ClassINET}}}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := m.Questions[0].Name, JoinLabel(l, "local"); got != want {
			t.Errorf("label %q decoded as %q, want %q", l, got, want)
		}
		labels, err := SplitName(m.Questions[0].Name)
		if err != nil || len(labels) != 2 || labels[0] != l {
			t.Errorf("labels of %q: %q %v", m.Questions[0].Name, labels, err)
		}
	}
}

// Many records pointing at one long name expand within bounds, and a
// message whose offsets pass 0x3fff compresses only against names before.
func TestLargeMessages(t *testing.T) {
	long := nameOfWire(255)
	var answers []Resource
	for i := range 2000 {
		answers = append(answers, Resource{Name: long, Class: ClassINET, TTL: 1,
			Data: &TXT{Strings: []string{strings.Repeat("t", 255), fmt.Sprint(i)}}})
	}
	m := &Message{Answers: answers}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 0x4000 {
		t.Fatalf("message of %d bytes", len(b))
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Answers) != 2000 || got.Answers[1999].Name != long {
		t.Errorf("%d answers", len(got.Answers))
	}
	// New names past 0x3fff are written in full, and still decode.
	m.Answers = append(m.Answers, Resource{Name: "late.example", Class: ClassINET, Data: &PTR{Target: "late.example"}},
		Resource{Name: "late.example", Class: ClassINET, Data: &PTR{Target: "x.late.example"}})
	b, err = m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err = Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	last := got.Answers[len(got.Answers)-1]
	if last.Name != "late.example" || last.Data.(*PTR).Target != "x.late.example" {
		t.Errorf("last answer %+v", last)
	}
	// Sections of more than 65535 entries are refused.
	for i, msg := range []*Message{{Answers: make([]Resource, 0x10000)}, {Authorities: make([]Resource, 0x10000)}, {Additionals: make([]Resource, 0x10000)}} {
		if _, err := msg.Encode(); !errors.Is(err, ErrFormat) {
			t.Errorf("section %d of 65536 entries: %v", i, err)
		}
	}
	// TXT strings of 255 bytes are the longest.
	ok := &Message{Answers: []Resource{{Name: "a", Data: &TXT{Strings: []string{strings.Repeat("x", 255)}}}}}
	if _, err := ok.Encode(); err != nil {
		t.Errorf("TXT string of 255 bytes: %v", err)
	}
	// Record data of exactly 65535 bytes is the longest.
	ok = &Message{Answers: []Resource{{Name: "a", Data: &Unknown{T: 99, Data: make([]byte, 0xffff)}}}}
	b, err = ok.Encode()
	if err != nil {
		t.Fatalf("record data of 65535 bytes: %v", err)
	}
	if got, err := Parse(b); err != nil || len(got.Answers[0].Data.(*Unknown).Data) != 0xffff {
		t.Errorf("Parse of record data of 65535 bytes: %v", err)
	}
}

// A header that claims 65535 entries in every section of a short message
// fails fast, without allocating for the claim.
func TestHugeCounts(t *testing.T) {
	b := []byte{0, 0, 0x84, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 1, 0, 1}
	allocs := testing.AllocsPerRun(10, func() { _, _ = Parse(b) })
	if _, err := Parse(b); !errors.Is(err, ErrFormat) {
		t.Errorf("Parse: %v", err)
	}
	if allocs > 20 {
		t.Errorf("%v allocations for a message of %d bytes", allocs, len(b))
	}
}
