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
		"unknown field":                          `{"nope":1}`,
		"http":                                   `{"sources":[{"id":"a","url":"http://x","sha256":"` + sha + `","files":[{"to":"a","sha256":"` + sha + `"}]}]}`,
		"short sha":                              `{"sources":[{"id":"a","url":"https://x","sha256":"abc","files":[{"to":"a","sha256":"` + sha + `"}]}]}`,
		"no files":                               src(`"files":[]`),
		"escaping path":                          src(`"files":[{"from":"x","to":"../x","sha256":"` + sha + `"}]`),
		"absolute path":                          src(`"files":[{"from":"x","to":"/x","sha256":"` + sha + `"}]`),
		"file without sha":                       src(`"files":[{"from":"x","to":"x"}]`),
		"dir with sha":                           src(`"files":[{"from":"x/","to":"x/","sha256":"` + sha + `"}]`),
		"dir to file":                            src(`"files":[{"from":"x/","to":"x","sha256":"` + sha + `"}]`),
		"bad pattern":                            src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}],"watch":{"page":"https://x","pattern":"("}`),
		"unpinned input":                         src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"y"}]}`,
		"unknown base":                           src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"x","base":"z"}]}`,
		"bad override":                           src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"x","overrides":[{"kind":"other","name":"n","type":"t","reason":"r"}]}]}`,
		"examples without a package":             src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"t","dir":".","package":"p","importPath":"p","xsd":"x","examples":{"dir":"e","importPath":"p/e"}}]}`,
		"profile examples without base examples": src(`"files":[{"from":"x","to":"x","sha256":"` + sha + `"}]`)[:len(src(`"files":[{"from":"x","to":"x","sha256":"`+sha+`"}]`))-1] + `,"targets":[{"name":"b","dir":".","package":"b","importPath":"b","xsd":"x"},{"name":"t","dir":"t","package":"p","importPath":"b/t","xsd":"x","base":"b","examples":{"dir":"e","package":"e","importPath":"b/e"}}]}`,
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

func TestLoadMissing(t *testing.T) {
	if _, err := manifest.Load(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Error("Load of a missing file succeeded")
	}
}

// withTargets returns a manifest with one pinned single-file source x and the
// given targets.
func withTargets(targets string) string {
	return `{"sources":[{"id":"a","url":"https://x/a","sha256":"` + sha + `","license":"l","files":[{"to":"x","sha256":"` + sha + `"}]}],"targets":[` + targets + `]}`
}

func TestParseRejectsMore(t *testing.T) {
	file := `"files":[{"to":"x","sha256":"` + sha + `"}]`
	one := func(id, extra string) string {
		return `{"id":"` + id + `","url":"https://x/a","sha256":"` + sha + `",` + extra + `}`
	}
	tests := map[string]string{
		"duplicate id":      `{"sources":[` + one("a", file) + `,` + one("a", `"files":[{"to":"y","sha256":"`+sha+`"}]`) + `]}`,
		"missing id":        `{"sources":[` + one("", file) + `]}`,
		"bad archive":       `{"sources":[` + one("a", `"archive":"tar",`+file) + `]}`,
		"duplicate output":  `{"sources":[` + one("a", file) + `,` + one("b", file) + `]}`,
		"single with from":  `{"sources":[` + one("a", `"files":[{"from":"f","to":"x","sha256":"`+sha+`"}]`) + `]}`,
		"watch half":        `{"sources":[` + one("a", file+`,"watch":{"page":"https://x"}`) + `]}`,
		"duplicate target":  withTargets(`{"name":"t","dir":".","package":"p","importPath":"p"},{"name":"t","dir":".","package":"p","importPath":"p"}`),
		"no package":        withTargets(`{"name":"t","dir":".","importPath":"p"}`),
		"edition2018":       withTargets(`{"name":"t","dir":".","package":"p","importPath":"p","edition2018":{"xsd":"x"}}`),
		"resource override": withTargets(`{"name":"t","dir":".","package":"p","importPath":"p","overrides":[{"kind":"resource","name":"n","reason":"r"}]}`),
		"override no type":  withTargets(`{"name":"t","dir":".","package":"p","importPath":"p","overrides":[{"kind":"elementRef","name":"n","reason":"r"}]}`),
		"trailing garbage":  `[`,
	}
	for name, doc := range tests {
		if _, err := manifest.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, doc)
		}
	}
}

func TestParseAccepts(t *testing.T) {
	doc := withTargets(`{"name":"b","dir":".","package":"b","importPath":"b","xsd":"x","wadl":"x",` +
		`"edition2018":{"xsd":"x","wadl":"x"},"schemaLocations":{"u":"x"},"examples":{"dir":"e","package":"e","importPath":"b/e"},` +
		`"overrides":[{"kind":"elementRef","name":"n","type":"t","ref":"r","reason":"r"},` +
		`{"kind":"polymorphicElement","name":"n","type":"t","reason":"r"},` +
		`{"kind":"resource","name":"n","like":"l","path":"/p","reason":"r"}]},` +
		`{"name":"t","dir":"t","package":"t","importPath":"b/t","base":"b","examples":{"dir":"te","package":"te","importPath":"b/te"}}`)
	m, err := manifest.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Target("nope"); ok {
		t.Error("Target found an unknown name")
	}
	if m.FileSHA256("nope") != "" {
		t.Error("FileSHA256 pinned an unknown file")
	}
}
