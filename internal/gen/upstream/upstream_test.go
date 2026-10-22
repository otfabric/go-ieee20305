// SPDX-License-Identifier: MIT

package upstream_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
	"github.com/otfabric/go-ieee20305/internal/gen/upstream"
)

type server struct {
	*httptest.Server
	files map[string][]byte
	hits  map[string]int
	ua    string
}

func newServer(t *testing.T) *server {
	s := &server{files: map[string][]byte{}, hits: map[string]int{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		s.ua = r.Header.Get("User-Agent")
		data, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setup(t *testing.T) (*server, *manifest.Manifest, *upstream.Client) {
	s := newServer(t)
	archive := zipOf(t, map[string]string{
		"pkg/sep.xsd":           "<schema/>",
		"pkg/Examples/a.xml":    "<a/>",
		"pkg/Examples/b.xml":    "<b/>",
		"__MACOSX/pkg/._x":      "junk",
		"pkg/Documentation/x.d": "doc",
	})
	s.files["/ieee/2030.5-2023_downloads.zip"] = archive
	s.files["/single.xsd"] = []byte("<single/>")
	s.files["/downloads/"] = []byte(`<a href="/ieee/2030.5-2023_downloads.zip">x</a> <a href="/ieee/2030.5-2013_downloads.zip">old</a> <a href="/ieee/2030.5-2027_downloads.zip">new</a>`)
	s.files["/ns/v9-9"] = []byte("future")
	m := &manifest.Manifest{UserAgent: "ieee20305-gen-test", Sources: []manifest.Source{
		{
			ID: "zip", URL: s.URL + "/ieee/2030.5-2023_downloads.zip", SHA256: upstream.SHA256(archive), Archive: "zip",
			Files: []manifest.File{
				{From: "pkg/sep.xsd", To: "z/sep.xsd", SHA256: upstream.SHA256([]byte("<schema/>"))},
				{From: "pkg/Examples/", To: "z/Examples/"},
			},
			Watch: &manifest.Watch{Page: s.URL + "/downloads/", Pattern: `2030\.5-[0-9]{4}_downloads\.zip`, Ignore: []string{"2030.5-2013_downloads.zip"}},
		},
		{
			ID: "single", URL: s.URL + "/single.xsd", SHA256: upstream.SHA256([]byte("<single/>")),
			Files: []manifest.File{{To: "s/single.xsd", SHA256: upstream.SHA256([]byte("<single/>"))}},
			Watch: &manifest.Watch{Probe: []string{s.URL + "/ns/v9-9", s.URL + "/ns/v9-8"}},
		},
	}}
	c := &upstream.Client{HTTP: s.Client(), UserAgent: m.UserAgent}
	return s, m, c
}

func TestFetch(t *testing.T) {
	s, m, c := setup(t)
	dir := t.TempDir()
	var log bytes.Buffer
	if err := c.Fetch(context.Background(), m, dir, &log); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"z/sep.xsd", "z/Examples/a.xml", "z/Examples/b.xml", "s/single.xsd"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
	for _, f := range []string{"z/Examples/._x", "z/Documentation/x.d"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s should not be extracted", f)
		}
	}
	if s.ua != "ieee20305-gen-test" {
		t.Errorf("User-Agent = %q", s.ua)
	}
	if err := upstream.Verify(m, dir); err != nil {
		t.Errorf("Verify: %v", err)
	}

	before := s.hits["/single.xsd"]
	log.Reset()
	if err := c.Fetch(context.Background(), m, dir, &log); err != nil {
		t.Fatal(err)
	}
	if s.hits["/single.xsd"] != before || !strings.Contains(log.String(), "up to date") {
		t.Errorf("a complete source must not be downloaded again: %s", log.String())
	}

	if err := os.WriteFile(filepath.Join(dir, "s/single.xsd"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := upstream.Verify(m, dir); err == nil {
		t.Error("Verify accepted a modified file")
	}
	if err := c.Fetch(context.Background(), m, dir, io.Discard); err != nil {
		t.Errorf("Fetch must restore a modified file: %v", err)
	}
}

func TestFetchRejectsChangedUpstream(t *testing.T) {
	s, m, c := setup(t)
	s.files["/single.xsd"] = []byte("<changed/>")
	err := c.Fetch(context.Background(), m, t.TempDir(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "upstream changed") {
		t.Errorf("err = %v, want a checksum mismatch", err)
	}
	delete(s.files, "/single.xsd")
	if err := c.Fetch(context.Background(), m, t.TempDir(), io.Discard); err == nil {
		t.Error("Fetch succeeded on a 404")
	}
}

func TestCheckUpdates(t *testing.T) {
	s, m, c := setup(t)
	if got := c.CheckUpdates(context.Background(), m); len(got) != 2 {
		t.Fatalf("findings = %+v, want the new 2027 release and the v9-9 probe", got)
	}
	s.files["/single.xsd"] = []byte("<changed/>")
	findings := c.CheckUpdates(context.Background(), m)
	kinds := map[string]int{}
	for _, f := range findings {
		kinds[f.Kind]++
	}
	if kinds["changed"] != 1 || kinds["new-release"] != 2 {
		t.Errorf("findings = %+v", findings)
	}
	report := upstream.Report(findings)
	for _, want := range []string{"2030.5-2027_downloads.zip", "/ns/v9-9", "single", "| changed |"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "2013") {
		t.Error("ignored release reported")
	}
}
