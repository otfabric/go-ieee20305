// SPDX-License-Identifier: MIT

//go:build ignore

// Command features checks the matrix of docs/FEATURES.md against the tests:
// every e2e test a row names exists, and every row that marks a stack as
// tested names an interop operation (or step) that the suite of interop/
// runs. Run it from the root of the repository:
//
//	go run scripts/features.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ticked    = regexp.MustCompile("`([^`]+)`")
	testName  = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)
	funcDecls = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
)

func main() {
	problems, err := check("docs/FEATURES.md", "e2e", "interop")
	if err != nil {
		fmt.Fprintln(os.Stderr, "features:", err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "docs/FEATURES.md:", p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Println("docs/FEATURES.md matches the tests")
}

// check returns the problems of the matrix of doc.
func check(doc, e2eDir, interopDir string) ([]string, error) {
	data, err := os.ReadFile(doc)
	if err != nil {
		return nil, err
	}
	tests, err := testFuncs(e2eDir)
	if err != nil {
		return nil, err
	}
	suite, err := concat(interopDir)
	if err != nil {
		return nil, err
	}
	var problems []string
	var header []string
	rows := 0
	inMatrix := false
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		switch {
		case strings.HasPrefix(line, "## "):
			inMatrix = strings.TrimSpace(line) == "## Matrix"
			continue
		case !inMatrix || !strings.HasPrefix(line, "|"):
			continue
		}
		cells := split(line)
		if header == nil {
			header = cells
			continue
		}
		if strings.HasPrefix(cells[0], "---") || strings.HasPrefix(cells[0], "**") {
			continue
		}
		if len(cells) != len(header) {
			problems = append(problems, fmt.Sprintf("line %d: %d cells, the header has %d", n, len(cells), len(header)))
			continue
		}
		rows++
		row := map[string]string{}
		for j, h := range header {
			row[h] = cells[j]
		}
		for _, m := range ticked.FindAllStringSubmatch(row["e2e"], -1) {
			if testName.MatchString(m[1]) && !tests[m[1]] {
				problems = append(problems, fmt.Sprintf("line %d: no e2e test %s", n, m[1]))
			}
		}
		ops := ticked.FindAllStringSubmatch(row["Interop op"], -1)
		for _, m := range ops {
			if !strings.Contains(suite, `"`+m[1]+`"`) {
				problems = append(problems, fmt.Sprintf("line %d: the interop suite runs no %q", n, m[1]))
			}
		}
		for _, h := range header[4 : len(header)-1] {
			if strings.Contains(row[h], "tested") && len(ops) == 0 {
				problems = append(problems, fmt.Sprintf("line %d: %s is marked tested, but the row names no interop op", n, h))
			}
		}
	}
	if header == nil || rows == 0 {
		problems = append(problems, "no matrix under \"## Matrix\"")
	}
	return problems, nil
}

func split(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(strings.TrimSuffix(line, "|"), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// testFuncs returns the test functions of the test files of dir.
func testFuncs(dir string) (map[string]bool, error) {
	src, err := concat(dir)
	if err != nil {
		return nil, err
	}
	tests := map[string]bool{}
	for _, m := range funcDecls.FindAllStringSubmatch(src, -1) {
		tests[m[1]] = true
	}
	return tests, nil
}

// concat returns the test files of dir, one after the other.
func concat(dir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no test files in %s", dir)
	}
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
