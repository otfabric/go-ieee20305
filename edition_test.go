// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
)

// Every document example, encoded for 2018 and for 2023, is valid against
// the official schema of that edition.
func TestEditionsValidate(t *testing.T) {
	xmllint, _ := exec.LookPath("xmllint")
	schemas := map[ieee20305.Edition]string{
		ieee20305.Edition2018: "schemas/upstream/ieee-2030.5-2018/sep.xsd",
		ieee20305.Edition2023: "schemas/upstream/ieee-2030.5-2023/sep.xsd",
	}
	for _, s := range schemas {
		if _, err := os.Stat(s); err != nil || xmllint == "" {
			needSchemas(t, "needs xmllint and the upstream schemas (make download)")
		}
	}
	xsd2018, err := os.ReadFile(schemas[ieee20305.Edition2018])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[ieee20305.Edition][]string{}
	for _, r := range ieee20305.Resources {
		for _, m := range r.Methods {
			for _, name := range []string{m.Request, m.Response} {
				v := examples.Element(name, 2)
				if v == nil {
					continue
				}
				for e := range schemas {
					if e == ieee20305.Edition2018 && !bytes.Contains(xsd2018, []byte(fmt.Sprintf(`<xs:element name="%s" type=`, name))) {
						continue // a document element of 2023 only
					}
					out, err := ieee20305.MarshalEdition(v, e)
					if err != nil {
						t.Fatalf("%s %v: %v", name, e, err)
					}
					if e == ieee20305.Edition2018 && bytes.Contains(out, []byte("schemaVer")) {
						t.Errorf("%s: a 2018 document with schemaVer", name)
					}
					path := filepath.Join(dir, fmt.Sprintf("%v-%s.xml", e, name))
					if err := os.WriteFile(path, out, 0o644); err != nil {
						t.Fatal(err)
					}
					files[e] = append(files[e], path)
				}
			}
		}
	}
	for e, list := range files {
		if len(list) < 50 {
			t.Errorf("%v: only %d documents", e, len(list))
		}
		args := append([]string{"--noout", "--schema", schemas[e]}, unique(list)...)
		if out, err := exec.Command(xmllint, args...).CombinedOutput(); err != nil {
			var bad []string
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.HasSuffix(line, " validates") && line != "" {
					bad = append(bad, line)
				}
			}
			t.Errorf("%v: documents invalid against %s:\n%s", e, schemas[e], strings.Join(bad, "\n"))
		}
	}
}

func unique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func TestEditions(t *testing.T) {
	for _, s := range []string{"2018", "2023"} {
		e, err := ieee20305.ParseEdition(s)
		if err != nil || e.String() != s {
			t.Errorf("ParseEdition(%q) = %v, %v", s, e, err)
		}
	}
	if _, err := ieee20305.ParseEdition("2013"); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("ParseEdition(2013) = %v", err)
	}
	if got := ieee20305.Edition(9).String(); got != "Edition(9)" {
		t.Errorf("String of an unknown edition = %q", got)
	}
	if !ieee20305.InEdition("EndDeviceList", ieee20305.Edition2018) || ieee20305.InEdition("CurrentDERControls", ieee20305.Edition2018) {
		t.Error("InEdition 2018")
	}
	if !ieee20305.InEdition("CurrentDERControls", ieee20305.Edition2023) || ieee20305.InEdition("Nope", ieee20305.Edition2023) {
		t.Error("InEdition 2023")
	}
	ed := examples.EndDevice(2)
	if _, err := ieee20305.MarshalEdition(*ed, ieee20305.Edition2018); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("MarshalEdition of a value = %v", err)
	}
	if _, err := ieee20305.MarshalEdition(struct{}{}, ieee20305.Edition2018); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("MarshalEdition of a foreign type = %v", err)
	}
	if _, err := ieee20305.MarshalEdition(ed, ieee20305.Edition(9)); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Errorf("MarshalEdition of an unknown edition = %v", err)
	}
	out, err := ieee20305.Edition2018.Codec().Marshal(ed)
	if err != nil || bytes.Contains(out, []byte("_r2_3")) || bytes.Contains(out, []byte("AggregatedDeviceListLink")) {
		t.Errorf("2018 codec: %v\n%s", err, out)
	}
	if ed.AggregatedDeviceListLink == nil {
		t.Error("MarshalEdition stripped the original")
	}
	var back ieee20305.EndDevice
	c := ieee20305.Edition2018.Codec()
	if err := c.Unmarshal(out, &back); err != nil || back.SFDI != ed.SFDI {
		t.Errorf("2018 codec Unmarshal: %v", err)
	}
	if v, err := c.Decode(out); err != nil {
		t.Errorf("2018 codec Decode: %v", err)
	} else if _, ok := v.(*ieee20305.EndDevice); !ok {
		t.Errorf("2018 codec Decode = %T", v)
	}
}
