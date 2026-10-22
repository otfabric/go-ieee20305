// SPDX-License-Identifier: MIT

package csipaus_test

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-ieee20305/csipaus"
	examples "github.com/otfabric/go-ieee20305/internal/examples/csipaus"
)

// FuzzDecode decodes arbitrary CSIP-AUS documents: Decode never panics, and
// what it decodes marshals to a document that decodes again to the same
// encoding.
func FuzzDecode(f *testing.F) {
	for _, v := range examples.All(1) {
		if out, err := csipaus.Marshal(v); err == nil {
			f.Add(out)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := csipaus.Decode(data)
		if err != nil {
			return
		}
		out, err := csipaus.Marshal(v)
		if err != nil {
			return
		}
		again, err := csipaus.Decode(out)
		if err != nil {
			t.Fatalf("a marshalled document does not decode: %v\n%s", err, out)
		}
		out2, err := csipaus.Marshal(again)
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("the round trip changed the document (%v):\n%s\n%s", err, out, out2)
		}
		_, _ = csipaus.LimitsOf(v)
	})
}
