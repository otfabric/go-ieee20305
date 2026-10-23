// SPDX-License-Identifier: MIT

// Command upstream checks internal/gotls, the copy of crypto/tls with
// CCM_8, against Go, from the root of the tlsccm module:
//
//	go run ./internal/upstream -diff    # the copy against crypto/tls of its base
//	go run ./internal/upstream -vulns   # Go's crypto/tls vulnerabilities, reviewed or not
//
// -diff fetches the files of crypto/tls at the base tag, undoes in the
// copy what makes it importable (the package name, the stand-ins of the
// standard library's internal packages) and the recorded patches, and
// requires the rest to be identical. -vulns lists the vulnerabilities of
// crypto/tls in Go's vulnerability database (vuln.go.dev) published after
// the base, and requires each to be assessed in internal/gotls/SECURITY.md.
//
// Exit status: 0 clean; 3 findings (differences, unreviewed
// vulnerabilities), written to stdout and to -report; 2 the check could
// not run (network, files).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// base is the release of Go the copy was taken from, and the day it was
// tagged: vulnerabilities published since may be missing from it.
const (
	base     = "go1.22.0"
	baseDate = "2024-02-06"
	dir      = "internal/gotls"
)

// shared are the files of crypto/tls the copy carries.
var shared = []string{
	"alert.go", "auth.go", "boring.go", "cache.go", "cipher_suites.go", "common.go",
	"common_string.go", "conn.go", "handshake_client.go", "handshake_client_tls13.go",
	"handshake_messages.go", "handshake_server.go", "handshake_server_tls13.go",
	"key_agreement.go", "key_schedule.go", "notboring.go", "prf.go", "quic.go", "ticket.go", "tls.go",
}

// own are the files of the copy that crypto/tls does not have.
var own = map[string]string{
	"cipher_suites_ccm.go":            "the registration of CCM_8",
	"ccm_check_test.go":               "test: CCM_8 is registered",
	"ccm_raw_test.go":                 "test: a handshake with CCM_8",
	"cipher_suites_ccm_order_test.go": "test: CCM_8 ranks above GCM",
	"stubs/boring/boring.go":          "stand-in for crypto/internal/boring",
	"stubs/cpu/cpu.go":                "stand-in for internal/cpu",
	"stubs/fipstls/fipstls.go":        "stand-in for crypto/internal/boring/fipstls",
	"stubs/godebug/godebug.go":        "stand-in for internal/godebug",
	"LICENSE":                         "the license of Go",
	"LICENSE-BATTELLE":                "the license of the CCM_8 registration and the stand-ins",
	"NOTICE-BATTELLE":                 "the notice of the CCM_8 registration and the stand-ins",
	"UPSTREAM.md":                     "provenance",
	"SECURITY.md":                     "the review of crypto/tls vulnerabilities",
}

// patch is a change of a shared file: ours replaces upstream, for a reason
// recorded in UPSTREAM.md.
type patch struct {
	file, ours, upstream string
}

var patches = []patch{
	// GO-2025-4008: the ALPN error quotes what the client sent.
	{"handshake_server.go",
		`fmt.Errorf("tls: client requested unsupported application protocols (%q)", clientProtos)`,
		`fmt.Errorf("tls: client requested unsupported application protocols (%s)", clientProtos)`},
}

// imports are the import paths of the copy and of crypto/tls.
var imports = [][2]string{
	{`"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls/stubs/fipstls"`, `"crypto/internal/boring/fipstls"`},
	{`"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls/stubs/boring"`, `"crypto/internal/boring"`},
	{`"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls/stubs/cpu"`, `"internal/cpu"`},
	{`"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls/stubs/godebug"`, `"internal/godebug"`},
}

var client = &http.Client{Timeout: time.Minute}

func get(url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// normalize turns a file of the copy back into the crypto/tls it was.
func normalize(name string, src []byte) ([]byte, error) {
	s := strings.Replace(string(src), "\npackage gotls\n", "\npackage tls\n", 1)
	for _, im := range imports {
		s = strings.ReplaceAll(s, im[0], im[1])
	}
	for _, p := range patches {
		if p.file != name {
			continue
		}
		if !strings.Contains(s, p.ours) {
			return nil, fmt.Errorf("%s: the recorded patch is missing: %s", name, p.ours)
		}
		s = strings.Replace(s, p.ours, p.upstream, 1)
	}
	return format.Source([]byte(s))
}

func diff() ([]string, error) {
	var findings []string
	for _, name := range shared {
		ours, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		norm, err := normalize(name, ours)
		if err != nil {
			findings = append(findings, err.Error())
			continue
		}
		up, err := get("https://raw.githubusercontent.com/golang/go/" + base + "/src/crypto/tls/" + name)
		if err != nil {
			return nil, err
		}
		if up, err = format.Source(up); err != nil {
			return nil, fmt.Errorf("%s of %s: %w", name, base, err)
		}
		if !bytes.Equal(norm, up) {
			findings = append(findings, fmt.Sprintf("%s differs from crypto/tls of %s beyond the recorded patches", name, base))
		}
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if _, ok := own[rel]; !ok && !contains(shared, rel) {
			findings = append(findings, rel+" is neither a file of crypto/tls nor recorded as one of the copy's own")
		}
		return nil
	})
	return findings, err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// vuln is an entry of vuln.go.dev.
type vuln struct {
	ID        string   `json:"id"`
	Published string   `json:"published"`
	Aliases   []string `json:"aliases"`
	Summary   string   `json:"summary"`
	Affected  []struct {
		Ranges []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
		Specific struct {
			Imports []struct {
				Path string `json:"path"`
			} `json:"imports"`
		} `json:"ecosystem_specific"`
	} `json:"affected"`
}

func vulns() ([]string, error) {
	data, err := get("https://vuln.go.dev/index/modules.json")
	if err != nil {
		return nil, err
	}
	var modules []struct {
		Path  string `json:"path"`
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	}
	if err := json.Unmarshal(data, &modules); err != nil {
		return nil, err
	}
	reviewed, err := os.ReadFile(filepath.Join(dir, "SECURITY.md"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range regexp.MustCompile(`GO-\d{4}-\d+`).FindAllString(string(reviewed), -1) {
		seen[id] = true
	}
	var findings []string
	for _, m := range modules {
		if m.Path != "stdlib" {
			continue
		}
		for _, v := range m.Vulns {
			data, err := get("https://vuln.go.dev/ID/" + v.ID + ".json")
			if err != nil {
				return nil, err
			}
			var e vuln
			if err := json.Unmarshal(data, &e); err != nil {
				return nil, err
			}
			if e.Published < baseDate || seen[e.ID] || !tls(e) {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s (%s, published %s): %s", e.ID, strings.Join(e.Aliases, ", "), e.Published[:10], e.Summary))
		}
	}
	sort.Strings(findings)
	return findings, nil
}

func tls(e vuln) bool {
	for _, a := range e.Affected {
		for _, im := range a.Specific.Imports {
			if im.Path == "crypto/tls" {
				return true
			}
		}
	}
	return false
}

func main() {
	doDiff := flag.Bool("diff", false, "compare the copy with crypto/tls of "+base)
	doVulns := flag.Bool("vulns", false, "list the crypto/tls vulnerabilities SECURITY.md does not review")
	report := flag.String("report", "", "also write the findings to this file, as Markdown")
	flag.Parse()
	if !*doDiff && !*doVulns {
		*doDiff, *doVulns = true, true
	}
	var out []string
	if *doDiff {
		f, err := diff()
		if err != nil {
			fmt.Fprintln(os.Stderr, "upstream:", err)
			os.Exit(2)
		}
		for _, x := range f {
			out = append(out, "- copy: "+x)
		}
	}
	if *doVulns {
		f, err := vulns()
		if err != nil {
			fmt.Fprintln(os.Stderr, "upstream:", err)
			os.Exit(2)
		}
		for _, x := range f {
			out = append(out, "- not reviewed in tlsccm/internal/gotls/SECURITY.md: "+x)
		}
	}
	if len(out) == 0 {
		fmt.Println("tlsccm/internal/gotls: no findings")
		return
	}
	text := "tlsccm/internal/gotls needs attention:\n\n" + strings.Join(out, "\n") + "\n"
	fmt.Print(text)
	if *report != "" {
		if err := os.WriteFile(*report, []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "upstream:", err)
			os.Exit(2)
		}
	}
	os.Exit(3)
}
