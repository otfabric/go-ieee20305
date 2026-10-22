// SPDX-License-Identifier: MIT

// Package upstream downloads the pinned schema sources of the manifest,
// verifies them, and checks whether upstream has published something newer.
package upstream

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/otfabric/go-ieee20305/internal/gen/manifest"
)

// maxDownload bounds a single download; the largest source is ~6 MB.
const maxDownload = 64 << 20

// Client fetches upstream sources.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// NewClient returns a Client with a timeout and the manifest's User-Agent.
func NewClient(m *manifest.Manifest) *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, UserAgent: m.UserAgent}
}

func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return c.HTTP.Do(req)
}

// Download returns the body of url, which must answer 200.
func (c *Client) Download(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if len(data) > maxDownload {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", url, maxDownload)
	}
	return data, nil
}

// SHA256 returns the lowercase hex SHA-256 of data.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Verify reports whether every pinned file of the manifest is present in
// dir with its pinned checksum.
func Verify(m *manifest.Manifest, dir string) error {
	for _, s := range m.Sources {
		for _, f := range s.Files {
			if f.IsDir() {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.To))); err != nil {
					return fmt.Errorf("%s: %w", f.To, err)
				}
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.To)))
			if err != nil {
				return fmt.Errorf("%s: %w (run make schemas)", f.To, err)
			}
			if got := SHA256(data); got != f.SHA256 {
				return fmt.Errorf("%s: SHA-256 %s, manifest pins %s", f.To, got, f.SHA256)
			}
		}
	}
	return nil
}

// Fetch downloads every source whose files are missing or stale in dir,
// verifies it against the manifest and writes its files. It logs one line
// per source to log.
func (c *Client) Fetch(ctx context.Context, m *manifest.Manifest, dir string, log io.Writer) error {
	for _, s := range m.Sources {
		if sourceComplete(s, dir) {
			_, _ = fmt.Fprintf(log, "%-20s up to date\n", s.ID)
			continue
		}
		data, err := c.Download(ctx, s.URL)
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		if got := SHA256(data); got != s.SHA256 {
			return fmt.Errorf("%s: %s has SHA-256 %s, manifest pins %s; upstream changed, review it and update schemas/manifest.json", s.ID, s.URL, got, s.SHA256)
		}
		if err := writeSource(s, data, dir); err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		_, _ = fmt.Fprintf(log, "%-20s fetched %s\n", s.ID, s.URL)
	}
	return Verify(m, dir)
}

func sourceComplete(s manifest.Source, dir string) bool {
	for _, f := range s.Files {
		p := filepath.Join(dir, filepath.FromSlash(f.To))
		if f.IsDir() {
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
				return false
			}
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || SHA256(data) != f.SHA256 {
			return false
		}
	}
	return true
}

func writeSource(s manifest.Source, data []byte, dir string) error {
	if s.Archive == "" {
		f := s.Files[0]
		return writeFile(dir, f.To, data, f.SHA256)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range s.Files {
		found := false
		for _, zf := range zr.File {
			name := zf.Name
			if zf.FileInfo().IsDir() || strings.HasPrefix(path.Base(name), "._") || strings.HasPrefix(name, "__MACOSX/") {
				continue
			}
			var to string
			switch {
			case f.IsDir() && strings.HasPrefix(name, f.From):
				to = f.To + strings.TrimPrefix(name, f.From)
			case !f.IsDir() && name == f.From:
				to = f.To
			default:
				continue
			}
			found = true
			content, err := readZipFile(zf)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			want := ""
			if !f.IsDir() {
				want = f.SHA256
			}
			if err := writeFile(dir, to, content, want); err != nil {
				return err
			}
		}
		if !found {
			return fmt.Errorf("archive has no %s", f.From)
		}
	}
	return nil
}

func readZipFile(zf *zip.File) ([]byte, error) {
	if zf.UncompressedSize64 > maxDownload {
		return nil, fmt.Errorf("larger than %d bytes", maxDownload)
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxDownload))
}

func writeFile(dir, rel string, data []byte, wantSHA string) error {
	clean := path.Clean(rel)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("refusing to write %q outside %s", rel, dir)
	}
	if wantSHA != "" {
		if got := SHA256(data); got != wantSHA {
			return fmt.Errorf("%s: SHA-256 %s, manifest pins %s", rel, got, wantSHA)
		}
	}
	p := filepath.Join(dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
