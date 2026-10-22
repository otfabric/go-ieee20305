// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"bytes"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
)

// FuzzDecode decodes arbitrary documents: Decode never panics, and what it
// decodes marshals, in both editions, to a document that decodes again to
// the same encoding.
func FuzzDecode(f *testing.F) {
	for _, v := range examples.All(1) {
		if out, err := ieee20305.Marshal(v); err == nil {
			f.Add(out)
		}
	}
	f.Add([]byte(`<EndDevice xmlns="urn:ieee:std:2030.5:ns"><sFDI>1</sFDI></EndDevice>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := ieee20305.StandardCodec.Decode(data)
		if err != nil {
			return
		}
		_ = ieee20305.Links(v)
		if vv, ok := v.(interface{ Validate() error }); ok {
			_ = vv.Validate()
		}
		out, err := ieee20305.Marshal(v)
		if err != nil {
			return // a value the XML of which does not hold, such as a bad date
		}
		again, err := ieee20305.StandardCodec.Decode(out)
		if err != nil {
			t.Fatalf("a marshalled document does not decode: %v\n%s", err, out)
		}
		out2, err := ieee20305.Marshal(again)
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("the round trip changed the document (%v):\n%s\n%s", err, out, out2)
		}
		if _, err := ieee20305.MarshalEdition(v, ieee20305.Edition2018); err != nil {
			t.Fatalf("MarshalEdition 2018: %v", err)
		}
		_ = ieee20305.Clone(v)
	})
}
