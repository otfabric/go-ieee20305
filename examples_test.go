// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

const upstream = "schemas/upstream/ieee-2030.5-2023"

// needSchemas skips a test that needs the downloaded schemas or xmllint, or
// fails it when IEEE20305_REQUIRE_SCHEMAS is set: the Generated code job sets
// it, as it installs both.
func needSchemas(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("IEEE20305_REQUIRE_SCHEMAS") != "" {
		t.Fatal(why)
	}
	t.Skip(why + " (IEEE20305_REQUIRE_SCHEMAS=1 makes this a failure)")
}

// TestOfficialExamples round-trips the examples of the IEEE 2030.5-2023
// supplemental material, and checks with xmllint that what Marshal writes
// is valid against the official sep.xsd whenever the example is.
func TestOfficialExamples(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(upstream, "Examples", "*.xml"))
	if len(files) == 0 {
		needSchemas(t, "upstream schemas not downloaded; run make download")
	}
	xmllint, _ := exec.LookPath("xmllint")
	if xmllint == "" {
		t.Log("xmllint not found: skipping schema validation of encoded documents")
	}
	schema := filepath.Join(upstream, "sep.xsd")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			v, err := ieee20305.Decode(in)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			inputValid := xmllint == "" || exec.Command(xmllint, "--noout", "--schema", schema, f).Run() == nil
			if inputValid {
				if err := v.(interface{ Validate() error }).Validate(); err != nil {
					t.Errorf("Validate: %v", err)
				}
			}
			out, err := ieee20305.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			back, err := ieee20305.Decode(out)
			if err != nil {
				t.Fatalf("Decode of Marshal output: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(v, back) {
				t.Errorf("round trip changed the value\nin:  %s\nout: %s", bytes.TrimSpace(in), out)
			}
			if xmllint != "" && inputValid {
				cmd := exec.Command(xmllint, "--noout", "--schema", schema, "-")
				cmd.Stdin = bytes.NewReader(out)
				if msg, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("encoded document is not valid against sep.xsd: %s\n%s", msg, out)
				}
			}
		})
	}
}
