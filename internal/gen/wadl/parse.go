// SPDX-License-Identifier: MIT

package wadl

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// ParseFile reads and parses a WADL file from disk.
func ParseFile(path string) (*Application, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read wadl: %w", err)
	}
	return Parse(data)
}

// Parse parses WADL XML bytes into a typed Application.
func Parse(data []byte) (*Application, error) {
	var raw rawApplication
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal wadl: %w", err)
	}
	app := &Application{}
	if raw.Resources != nil {
		for _, rr := range raw.Resources.Resources {
			app.Resources = append(app.Resources, convertResource(rr))
		}
	}
	return app, nil
}

func convertResource(rr rawResource) Resource {
	r := Resource{
		Path: rr.Path,
	}
	for _, rp := range rr.Params {
		r.Params = append(r.Params, Param{
			Name:  rp.Name,
			Style: rp.Style,
			Type:  stripNS(rp.Type),
		})
	}
	for _, rm := range rr.Methods {
		m := Method{Name: rm.Name}
		if rm.Request != nil && len(rm.Request.Representations) > 0 {
			rep := rm.Request.Representations[0]
			m.Request = &Representation{
				MediaType: rep.MediaType,
				Element:   stripNS(rep.Element),
			}
		}
		if rm.Response != nil && len(rm.Response.Representations) > 0 {
			rep := rm.Response.Representations[0]
			m.Response = &Representation{
				MediaType: rep.MediaType,
				Element:   stripNS(rep.Element),
			}
		}
		r.Methods = append(r.Methods, m)
	}
	for _, child := range rr.Resources {
		r.Children = append(r.Children, convertResource(child))
	}
	return r
}

// stripNS removes a namespace prefix from a QName (e.g. "sep:EndDevice" → "EndDevice").
func stripNS(s string) string {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Raw XML types for unmarshal.

type rawApplication struct {
	XMLName   xml.Name      `xml:"application"`
	Resources *rawResources `xml:"resources"`
}

type rawResources struct {
	Base      string        `xml:"base,attr"`
	Resources []rawResource `xml:"resource"`
}

type rawResource struct {
	Path      string        `xml:"path,attr"`
	Params    []rawParam    `xml:"param"`
	Methods   []rawMethod   `xml:"method"`
	Resources []rawResource `xml:"resource"`
}

type rawParam struct {
	Name  string `xml:"name,attr"`
	Style string `xml:"style,attr"`
	Type  string `xml:"type,attr"`
}

type rawMethod struct {
	Name     string       `xml:"name,attr"`
	Request  *rawBody     `xml:"request"`
	Response *rawBody     `xml:"response"`
}

type rawBody struct {
	Representations []rawRepresentation `xml:"representation"`
}

type rawRepresentation struct {
	MediaType string `xml:"mediaType,attr"`
	Element   string `xml:"element,attr"`
}
