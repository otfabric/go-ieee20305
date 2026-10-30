// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
)

// documents returns the example documents that are document types of their
// own: examples.All also holds abstract types, which Marshal rejects.
func documents() []any {
	var docs []any
	for _, v := range examples.All(1) {
		if _, ok := ieee20305.ElementName(v); ok {
			docs = append(docs, v)
		}
	}
	return docs
}

// BenchmarkMarshal encodes every example document once per iteration.
func BenchmarkMarshal(b *testing.B) {
	docs := documents()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, v := range docs {
			if _, err := ieee20305.Marshal(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkDecode decodes every example document once per iteration.
func BenchmarkDecode(b *testing.B) {
	var encoded [][]byte
	for _, v := range documents() {
		out, err := ieee20305.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		encoded = append(encoded, out)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, data := range encoded {
			if _, err := ieee20305.Decode(data); err != nil {
				b.Fatal(err)
			}
		}
	}
}
