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

// The results of the benchmarks are kept here, so that the work that makes
// them is not removed as unused.
var (
	sinkBytes []byte
	sinkDoc   any
)

// BenchmarkMarshal encodes every example document once per iteration.
func BenchmarkMarshal(b *testing.B) {
	docs := documents()
	if len(docs) == 0 {
		b.Fatal("no documents")
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, v := range docs {
			out, err := ieee20305.Marshal(v)
			if err != nil {
				b.Fatal(err)
			}
			sinkBytes = out
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
	if len(encoded) == 0 {
		b.Fatal("no documents")
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, data := range encoded {
			v, err := ieee20305.Decode(data)
			if err != nil {
				b.Fatal(err)
			}
			sinkDoc = v
		}
	}
}
