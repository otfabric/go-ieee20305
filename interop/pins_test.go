//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"os"
	"strings"
	"testing"
)

// The Interop workflow pins the same images as images.go.
func TestWorkflowPins(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/interop.yml")
	if err != nil {
		prerequisite(t, "no workflow: %v", err)
	}
	for _, img := range []string{EnvoyImage, GridAPPSDImage, Py20305Image, EPRIImage} {
		if !strings.Contains(string(data), "'"+img+"'") {
			t.Errorf("the Interop workflow does not pin %s, as interop/images.go does", img)
		}
	}
}
