// SPDX-License-Identifier: MIT

package emit_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

var update = flag.Bool("update", false, "update golden files")

func loadTestSchema(t *testing.T) *xsd.Resolved {
	t.Helper()
	path := filepath.Join("..", "xsd", "testdata", "minimal.xsd")
	s, err := xsd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	r, err := xsd.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func TestEmitGolden(t *testing.T) {
	r := loadTestSchema(t)

	outDir := t.TempDir()
	e := emit.New(r, outDir, "ieee20305", "minimal.xsd", "abc123test")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	goldenFiles := []string{"types_gen.go", "primitives_gen.go"}
	for _, name := range goldenFiles {
		t.Run(name, func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(outDir, name))
			if err != nil {
				t.Fatalf("read generated: %v", err)
			}

			goldenPath := filepath.Join("testdata", name+".golden")

			if *update {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
				t.Logf("updated %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", goldenPath, err)
			}

			if string(got) != string(want) {
				t.Errorf("output differs from golden file %s\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
			}
		})
	}
}

func TestEmitCompiles(t *testing.T) {
	r := loadTestSchema(t)

	outDir := t.TempDir()
	e := emit.New(r, outDir, "ieee20305", "test.xsd", "deadbeef")
	if err := e.Emit(); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Verify the output files exist and are non-empty.
	for _, name := range []string{"types_gen.go", "primitives_gen.go"} {
		info, err := os.Stat(filepath.Join(outDir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}
