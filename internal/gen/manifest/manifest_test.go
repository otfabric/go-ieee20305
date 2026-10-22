// SPDX-License-Identifier: MIT

package manifest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
)

// TestRepositoryManifest validates the manifest the module generates from.
func TestRepositoryManifest(t *testing.T) {
	m, err := manifest.Load(filepath.Join("..", "..", "..", "schemas", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	base, ok := m.Target("ieee20305")
	if !ok || base.Dir != "." || m.FileSHA256(base.XSD) == "" {
		t.Errorf("base target = %+v", base)
	}
	if _, ok := m.Target("csipaus"); !ok {
		t.Error("csipaus target missing")
	}
	for _, s := range m.Sources {
		if s.Redistributable {
			t.Errorf("%s: no upstream source may be marked redistributable without a recorded licence", s.ID)
		}
	}
}

const sha = "0000000000000000000000000000000000000000000000000000000000000000"

func TestParseRejects(t *testing.T) {
	src := func(extra string) string {
		return `{"sources":[{"id":"a","url":"https://x/a.zip","sha256":"` + sha + `","archive":"zip","license":"l",` + extra + `}]}`
	}
	tests := map[string]string{
		"unknown field":    `{"nope":1}`,
		"http":             `{"sources":[{"id":"a","url":"http://x","sha256":"` + sha + `","files":[{"to":"a","sha256":"` + sha + `"}]}]}`,
		"short sha":        `{"sources":[{"id":"a","url":"https://x","sha256":"abc","files":[{"to":"a","sha256":"` + sha + `"}]}]}`,
		"no files":         src(`"files":[]`),
		"escaping path":    src(`"files":[{"from":"x","to":"../x","sha256":"` + sha + `"}]`),
		"absolute path":    src(`"files":[{"from":"x","to":"/x","sha256":"` + sha + `"}]`),
		"file without sha": src(`"files":[{"from":"x","to":"x"}]`),
		"dir with sha":     src(`"files":[{"from":"x/","to":"x/","sha256":"` + sha + `"}]`),
		"dir to file":      src(`"files":[{"from":"x/","to":"x","sha256":"` + sha + `"}]`),
		"bad pattern":      src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}],"watch":{"page":"https://x","pattern":"("}`),
		"unpinned input":   src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"y"}]}`,
		"unknown base":     src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"x","base":"z"}]}`,
		"bad override":     src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"x","overrides":[{"kind":"other","name":"n","type":"t","reason":"r"}]}]}`,
	}
	for name, doc := range tests {
		if _, err := manifest.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, doc)
		}
	}
	ok := src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)
	if _, err := manifest.Parse([]byte(ok)); err != nil || !strings.Contains(ok, "zip") {
		t.Errorf("valid manifest rejected: %v", err)
	}
}
