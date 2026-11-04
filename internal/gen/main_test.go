// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
)

// repoRoot is the module root, two directories above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(filepath.Join(repoRoot(t), "schemas", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// write replaces what the generator wrote earlier, removes what it no
// longer produces, and leaves files it did not write.
func TestWrite(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "old_gen.go")
	own := filepath.Join(dir, "own.go")
	if err := os.WriteFile(stale, []byte(marker+"\n\npackage x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := write(dir, emit.Files{"new_gen.go": []byte(marker + "\n\npackage x\n")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale generated file kept: %v", err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("hand-written file removed: %v", err)
	}
	got, err := generatedFiles(dir)
	if err != nil || !slices.Equal(got, []string{"new_gen.go"}) {
		t.Errorf("generatedFiles = %v, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, "new_gen.go")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("new file mode %v, %v; want 0600", fi.Mode().Perm(), err)
		}
	}
	// A directory that cannot be created, under a file.
	if err := write(filepath.Join(own, "x"), emit.Files{}); err == nil {
		t.Error("write under a file succeeded")
	}
	if _, err := generatedFiles(filepath.Join(dir, "missing")); err == nil {
		t.Error("generatedFiles of a missing directory succeeded")
	}
}

// The committed generated files record the sources the manifest pins.
func TestVerifyCommitted(t *testing.T) {
	if err := verify(loadManifest(t), repoRoot(t)); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x_gen.go")
	src := marker + "\n//\ta.xsd sha256:0a\n//\tb.wadl sha256:0b\n//\tno sum\npackage x\n//\tc.xsd sha256:0c\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := recordedSources(path)
	if err != nil || len(got) != 2 || got["a.xsd"] != "0a" || got["b.wadl"] != "0b" {
		t.Errorf("recordedSources = %v, %v", got, err)
	}
	if _, err := recordedSources(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Error("recordedSources of a missing file succeeded")
	}
}

// Without the downloaded upstream files, generate fails and says how to
// get them.
func TestGenerateWithoutUpstream(t *testing.T) {
	err := generate(loadManifest(t), t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "make download") {
		t.Errorf("generate without upstream files: %v", err)
	}
}

func TestWriteReport(t *testing.T) {
	if err := writeReport("", "ignored"); err != nil {
		t.Errorf("no path: %v", err)
	}
	path := filepath.Join(t.TempDir(), "report.md")
	if err := writeReport(path, "# changed\n"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "# changed\n" {
		t.Errorf("report %q, %v", data, err)
	}
	if err := writeReport(filepath.Join(path, "x"), "x"); err == nil {
		t.Error("report under a file succeeded")
	}
}
