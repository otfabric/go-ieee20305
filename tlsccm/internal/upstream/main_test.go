// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every recorded patch is present in the copy, so -diff can undo it.
func TestPatchesApply(t *testing.T) {
	t.Chdir("../..")
	for _, name := range shared {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		norm, err := normalize(name, src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(norm), "package gotls") || strings.Contains(string(norm), "tlsccm/internal/gotls/stubs") {
			t.Errorf("%s: not normalized", name)
		}
	}
	if _, err := normalize("auth.go", []byte("\npackage gotls\n")); err == nil {
		t.Error("a file without its recorded patch normalized")
	}
}

func TestDay(t *testing.T) {
	for in, want := range map[string]string{
		"2025-10-29T21:52:24Z": "2025-10-29",
		"2025-10-29":           "2025-10-29",
		"2025":                 "2025",
		"":                     "",
	} {
		if got := day(in); got != want {
			t.Errorf("day(%q) = %q, want %q", in, got, want)
		}
	}
}
