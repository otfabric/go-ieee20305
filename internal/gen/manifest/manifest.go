// SPDX-License-Identifier: MIT

// Package manifest reads schemas/manifest.json: the pinned upstream
// sources the generator consumes and the packages it generates.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

// Manifest is the decoded schemas/manifest.json.
type Manifest struct {
	Description string   `json:"description"`
	UserAgent   string   `json:"userAgent"`
	Sources     []Source `json:"sources"`
	Targets     []Target `json:"targets"`
}

// Source is one upstream download: a single file or an archive.
type Source struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Version         string `json:"version"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	Archive         string `json:"archive,omitempty"`
	License         string `json:"license"`
	Redistributable bool   `json:"redistributable"`
	Files           []File `json:"files"`
	Watch           *Watch `json:"watch,omitempty"`
}

// File maps a file of a source to its path below the upstream directory.
// From is the member path inside an archive; it is empty for a source that
// is a single file. A From or To ending in "/" extracts a directory.
type File struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to"`
	SHA256 string `json:"sha256,omitempty"`
}

// IsDir reports whether f extracts a directory of an archive.
func (f File) IsDir() bool { return strings.HasSuffix(f.To, "/") }

// Watch describes how to detect a newer release of a source.
type Watch struct {
	// Page is scanned for links matching Pattern; a match the manifest
	// does not know is a new release.
	Page    string `json:"page,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	// Ignore lists base names of matched links that are deliberately not
	// pinned, such as superseded releases.
	Ignore []string `json:"ignore,omitempty"`
	// Probe lists URLs of pages a future release would publish; one that
	// answers 200 is a new release.
	Probe []string `json:"probe,omitempty"`
}

// Target is a Go package generated from upstream files.
type Target struct {
	Notes           string            `json:"notes,omitempty"`
	Name            string            `json:"name"`
	Dir             string            `json:"dir"`
	Package         string            `json:"package"`
	ImportPath      string            `json:"importPath"`
	Base            string            `json:"base,omitempty"`
	ProfileVersion  string            `json:"profileVersion,omitempty"`
	XSD             string            `json:"xsd"`
	WADL            string            `json:"wadl,omitempty"`
	SchemaLocations map[string]string `json:"schemaLocations,omitempty"`
	Overrides       []Override        `json:"overrides,omitempty"`
}

// Override is an explicit, documented instruction for generating from an
// upstream schema.
//
// Kind "elementRef" resolves an element reference Ref that the schema set
// does not declare as a local element Name of type Type.
//
// Kind "polymorphicElement" generates the element Name of the complex type
// Type as a value whose xsi:type may name any type derived from its declared
// type.
type Override struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
	Reason string `json:"reason"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Load reads and validates the manifest at path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates a manifest.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	ids := map[string]bool{}
	outputs := map[string]bool{}
	for _, s := range m.Sources {
		if s.ID == "" || ids[s.ID] {
			return fmt.Errorf("source %q: missing or duplicate id", s.ID)
		}
		ids[s.ID] = true
		if !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("source %s: url must use https", s.ID)
		}
		if !sha256Hex.MatchString(s.SHA256) {
			return fmt.Errorf("source %s: sha256 must be 64 lowercase hex characters", s.ID)
		}
		switch s.Archive {
		case "", "zip":
		default:
			return fmt.Errorf("source %s: unsupported archive %q", s.ID, s.Archive)
		}
		if len(s.Files) == 0 {
			return fmt.Errorf("source %s: no files", s.ID)
		}
		for _, f := range s.Files {
			if err := checkRelPath(f.To); err != nil {
				return fmt.Errorf("source %s: to: %w", s.ID, err)
			}
			if outputs[f.To] {
				return fmt.Errorf("source %s: %s is written by more than one source", s.ID, f.To)
			}
			outputs[f.To] = true
			if s.Archive == "" {
				if f.From != "" || f.IsDir() || len(s.Files) != 1 {
					return fmt.Errorf("source %s: a single-file source has exactly one file without from", s.ID)
				}
			} else if f.From == "" || strings.HasSuffix(f.From, "/") != f.IsDir() {
				return fmt.Errorf("source %s: archive member %q must be a file mapped to a file or a directory mapped to a directory", s.ID, f.From)
			}
			if f.IsDir() && f.SHA256 != "" {
				return fmt.Errorf("source %s: directory %s cannot have a sha256", s.ID, f.To)
			}
			if !f.IsDir() && !sha256Hex.MatchString(f.SHA256) {
				return fmt.Errorf("source %s: file %s needs a sha256", s.ID, f.To)
			}
		}
		if w := s.Watch; w != nil {
			if (w.Page == "") != (w.Pattern == "") {
				return fmt.Errorf("source %s: watch page and pattern go together", s.ID)
			}
			if w.Pattern != "" {
				if _, err := regexp.Compile(w.Pattern); err != nil {
					return fmt.Errorf("source %s: watch pattern: %w", s.ID, err)
				}
			}
		}
	}
	names := map[string]bool{}
	for _, t := range m.Targets {
		if t.Name == "" || names[t.Name] {
			return fmt.Errorf("target %q: missing or duplicate name", t.Name)
		}
		names[t.Name] = true
		if t.Package == "" || t.ImportPath == "" || t.Dir == "" {
			return fmt.Errorf("target %s: package, importPath and dir are required", t.Name)
		}
		if t.Base != "" && !names[t.Base] {
			return fmt.Errorf("target %s: base %q must be a target listed before it", t.Name, t.Base)
		}
		inputs := []string{t.XSD, t.WADL}
		for _, loc := range t.SchemaLocations {
			inputs = append(inputs, loc)
		}
		for _, in := range inputs {
			if in != "" && m.FileSHA256(in) == "" {
				return fmt.Errorf("target %s: input %s is not a pinned file of any source", t.Name, in)
			}
		}
		for _, o := range t.Overrides {
			switch {
			case o.Reason == "" || o.Name == "" || o.Type == "":
				return fmt.Errorf("target %s: an override needs a name, a type and a reason", t.Name)
			case o.Kind == "elementRef" && o.Ref != "":
			case o.Kind == "polymorphicElement" && o.Ref == "":
			default:
				return fmt.Errorf("target %s: override of kind %q is malformed or unknown", t.Name, o.Kind)
			}
		}
	}
	return nil
}

// checkRelPath rejects paths that could escape the upstream directory.
func checkRelPath(p string) error {
	clean := path.Clean(strings.TrimSuffix(p, "/"))
	if p == "" || path.IsAbs(p) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%q must be a relative path inside the upstream directory", p)
	}
	return nil
}

// FileSHA256 returns the pinned checksum of the upstream file at rel, or ""
// if no source pins it.
func (m *Manifest) FileSHA256(rel string) string {
	for _, s := range m.Sources {
		for _, f := range s.Files {
			if f.To == rel {
				return f.SHA256
			}
		}
	}
	return ""
}

// Target returns the target with the given name.
func (m *Manifest) Target(name string) (*Target, bool) {
	for i := range m.Targets {
		if m.Targets[i].Name == name {
			return &m.Targets[i], true
		}
	}
	return nil, false
}
