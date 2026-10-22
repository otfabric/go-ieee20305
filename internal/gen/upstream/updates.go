// SPDX-License-Identifier: MIT

package upstream

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
)

// Finding is one change upstream relative to the manifest.
type Finding struct {
	Source string
	Kind   string // "changed", "new-release", "error"
	Detail string
}

// CheckUpdates compares upstream with the manifest: it downloads every
// source and compares its checksum, scans watched pages for release links
// the manifest does not know, and probes the URLs of expected future
// releases. A network failure is reported as a finding of kind "error".
func (c *Client) CheckUpdates(ctx context.Context, m *manifest.Manifest) []Finding {
	var out []Finding
	known := map[string]bool{}
	for _, s := range m.Sources {
		known[path.Base(s.URL)] = true
	}
	for _, s := range m.Sources {
		data, err := c.Download(ctx, s.URL)
		switch {
		case err != nil:
			out = append(out, Finding{s.ID, "error", err.Error()})
		case SHA256(data) != s.SHA256:
			out = append(out, Finding{s.ID, "changed", fmt.Sprintf("%s now has SHA-256 %s; the manifest pins %s", s.URL, SHA256(data), s.SHA256)})
		}
		if s.Watch == nil {
			continue
		}
		if s.Watch.Page != "" {
			links, err := c.scan(ctx, s.Watch.Page, s.Watch.Pattern)
			if err != nil {
				out = append(out, Finding{s.ID, "error", err.Error()})
			}
			ignored := map[string]bool{}
			for _, n := range s.Watch.Ignore {
				ignored[n] = true
			}
			for _, l := range links {
				if !known[path.Base(l)] && !ignored[path.Base(l)] {
					out = append(out, Finding{s.ID, "new-release", fmt.Sprintf("%s links %s, which the manifest does not pin", s.Watch.Page, l)})
				}
			}
		}
		for _, p := range s.Watch.Probe {
			status, err := c.status(ctx, p)
			switch {
			case err != nil:
				out = append(out, Finding{s.ID, "error", err.Error()})
			case status == http.StatusOK:
				out = append(out, Finding{s.ID, "new-release", fmt.Sprintf("%s exists", p)})
			}
		}
	}
	return out
}

func (c *Client) scan(ctx context.Context, page, pattern string) ([]string, error) {
	body, err := c.Download(ctx, page)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(page)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`href="([^"]*` + pattern + `)"`)
	seen := map[string]bool{}
	for _, m := range re.FindAllSubmatch(body, -1) {
		ref, err := url.Parse(string(m[1]))
		if err != nil {
			continue
		}
		seen[base.ResolveReference(ref).String()] = true
	}
	links := make([]string, 0, len(seen))
	for l := range seen {
		links = append(links, l)
	}
	sort.Strings(links)
	return links, nil
}

func (c *Client) status(ctx context.Context, u string) (int, error) {
	resp, err := c.get(ctx, u)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// Report renders findings as Markdown for a GitHub issue.
func Report(findings []Finding) string {
	var b strings.Builder
	b.WriteString("The scheduled upstream check found differences between the upstream schema sources and `schemas/manifest.json`.\n\n")
	b.WriteString("| Source | Kind | Detail |\n|---|---|---|\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", f.Source, f.Kind, strings.ReplaceAll(f.Detail, "|", "\\|"))
	}
	b.WriteString("\nTo adopt a change: download it, review the diff, update the URL and checksums in `schemas/manifest.json`, run `make schemas generate`, and commit the regenerated code.\n")
	return b.String()
}
