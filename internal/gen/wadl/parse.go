// SPDX-License-Identifier: MIT

package wadl

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ParseFile reads and parses a WADL file.
func ParseFile(path string) (*Application, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	app, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return app, nil
}

// Parse parses a WADL document. Nested resources are flattened, with their
// sample paths as written in the document.
func Parse(data []byte) (*Application, error) {
	var raw rawApplication
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.XMLName.Space != Namespace || raw.XMLName.Local != "application" {
		return nil, fmt.Errorf("root element is not a WADL application")
	}
	ns := map[string]string{}
	for _, a := range raw.Attrs {
		if a.Name.Space == "xmlns" {
			ns[a.Name.Local] = a.Value
		}
	}
	if len(raw.Resources) != 1 {
		return nil, fmt.Errorf("expected one resources element, found %d", len(raw.Resources))
	}
	app := &Application{}
	ids := map[string]bool{}
	var walk func([]rawResource) error
	walk = func(rs []rawResource) error {
		for i := range rs {
			r, err := convertResource(&rs[i], ns)
			if err != nil {
				return err
			}
			if ids[r.ID] {
				return fmt.Errorf("resource id %q appears twice", r.ID)
			}
			ids[r.ID] = true
			app.Resources = append(app.Resources, r)
			if err := walk(rs[i].Resources); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(raw.Resources[0].Resources); err != nil {
		return nil, err
	}
	return app, nil
}

func convertResource(rr *rawResource, ns map[string]string) (*Resource, error) {
	if rr.ID == "" {
		return nil, fmt.Errorf("resource without an id")
	}
	if err := unexpected("resource "+rr.ID, rr.Other); err != nil {
		return nil, err
	}
	r := &Resource{ID: rr.ID, SamplePath: rr.SamplePath}
	for i := range rr.Methods {
		rm := &rr.Methods[i]
		where := "resource " + rr.ID + " method " + rm.ID
		if err := unexpected(where, rm.Other); err != nil {
			return nil, err
		}
		switch rm.Mode {
		case "M", "O", "D", "E":
		default:
			return nil, fmt.Errorf("%s: unknown wx:mode %q", where, rm.Mode)
		}
		m := &Method{ID: rm.ID, Name: rm.Name, Mode: rm.Mode}
		if rm.Request != nil {
			for _, p := range rm.Request.Params {
				if p.Style != "query" {
					return nil, fmt.Errorf("%s: request param %s has style %q", where, p.Name, p.Style)
				}
				m.QueryParams = append(m.QueryParams, p.Name)
			}
			reps, err := representations(rm.Request.Representations, ns, where)
			if err != nil {
				return nil, err
			}
			m.Request = reps
		}
		for _, resp := range rm.Responses {
			out := Response{}
			for _, s := range strings.Fields(resp.Status) {
				n, err := strconv.Atoi(s)
				if err != nil {
					return nil, fmt.Errorf("%s: bad status %q", where, s)
				}
				out.Statuses = append(out.Statuses, n)
			}
			for _, p := range resp.Params {
				if p.Style != "header" {
					return nil, fmt.Errorf("%s: response param %s has style %q", where, p.Name, p.Style)
				}
				out.HeaderParams = append(out.HeaderParams, p.Name)
			}
			reps, err := representations(resp.Representations, ns, where)
			if err != nil {
				return nil, err
			}
			out.Representations = reps
			m.Responses = append(m.Responses, out)
		}
		r.Methods = append(r.Methods, m)
	}
	return r, nil
}

func representations(raws []rawRepresentation, ns map[string]string, where string) ([]Representation, error) {
	var out []Representation
	for _, rr := range raws {
		rep := Representation{MediaType: rr.MediaType}
		if rr.Element != "" {
			prefix, local, ok := strings.Cut(rr.Element, ":")
			if !ok {
				return nil, fmt.Errorf("%s: element %q has no namespace prefix", where, rr.Element)
			}
			space, ok := ns[prefix]
			if !ok {
				return nil, fmt.Errorf("%s: undeclared prefix in %q", where, rr.Element)
			}
			rep.ElementSpace, rep.Element = space, local
		}
		out = append(out, rep)
	}
	return out, nil
}

func unexpected(where string, other []rawOther) error {
	if len(other) > 0 {
		return fmt.Errorf("%s: unexpected element %s", where, other[0].XMLName.Local)
	}
	return nil
}

type rawOther struct {
	XMLName xml.Name
}

type rawApplication struct {
	XMLName   xml.Name
	Attrs     []xml.Attr     `xml:",any,attr"`
	Resources []rawResources `xml:"resources"`
}

type rawResources struct {
	Resources []rawResource `xml:"resource"`
}

type rawResource struct {
	ID           string        `xml:"id,attr"`
	Path         string        `xml:"path,attr"`
	SamplePath   string        `xml:"urn:ieee:std:2030.5:wadlExt samplePath,attr"`
	Doc          []rawOther    `xml:"doc"`
	Methods      []rawMethod   `xml:"method"`
	Resources    []rawResource `xml:"resource"`
	SampleParams []rawOther    `xml:"urn:ieee:std:2030.5:wadlExt sampleParam"`
	Other        []rawOther    `xml:",any"`
}

type rawMethod struct {
	ID        string        `xml:"id,attr"`
	Name      string        `xml:"name,attr"`
	Mode      string        `xml:"urn:ieee:std:2030.5:wadlExt mode,attr"`
	Doc       []rawOther    `xml:"doc"`
	Request   *rawRequest   `xml:"request"`
	Responses []rawResponse `xml:"response"`
	Other     []rawOther    `xml:",any"`
}

type rawRequest struct {
	Params          []rawParam          `xml:"param"`
	Representations []rawRepresentation `xml:"representation"`
}

type rawResponse struct {
	Status          string              `xml:"status,attr"`
	Params          []rawParam          `xml:"param"`
	Representations []rawRepresentation `xml:"representation"`
}

type rawParam struct {
	Name  string `xml:"name,attr"`
	Style string `xml:"style,attr"`
}

type rawRepresentation struct {
	MediaType string `xml:"mediaType,attr"`
	Element   string `xml:"element,attr"`
}
