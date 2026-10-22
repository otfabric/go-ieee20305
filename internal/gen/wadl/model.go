// SPDX-License-Identifier: MIT

// Package wadl reads the IEEE 2030.5 WADL: the resources of the protocol
// with their sample URIs, methods, conformance levels and representations.
package wadl

// Namespace of WADL documents.
const Namespace = "http://wadl.dev.java.net/2009/02"

// ExtNamespace of the IEEE 2030.5 WADL extension attributes.
const ExtNamespace = "urn:ieee:std:2030.5:wadlExt"

// Application is a parsed WADL document.
type Application struct {
	Resources []*Resource
}

// Resource is one resource of the WADL.
type Resource struct {
	ID         string
	SamplePath string
	Methods    []*Method
}

// Method is an HTTP method on a resource.
type Method struct {
	ID   string
	Name string
	// Mode is the wx:mode conformance letter: M (mandatory), O (optional),
	// D (discouraged) or E (error).
	Mode        string
	QueryParams []string
	Request     []Representation
	Responses   []Response
}

// Representation is a body of a request or response.
type Representation struct {
	MediaType string
	// Element is the local name of the document element, in the namespace
	// ElementSpace.
	ElementSpace string
	Element      string
}

// Response is a possible response of a method.
type Response struct {
	Statuses        []int
	Representations []Representation
	HeaderParams    []string
}

// ResponseElement returns the document element of the method's successful
// response, or "" if it has no body.
func (m *Method) ResponseElement() string {
	for _, r := range m.Responses {
		ok := len(r.Statuses) == 0
		for _, s := range r.Statuses {
			ok = ok || (s >= 200 && s < 300)
		}
		if ok && len(r.Representations) > 0 {
			return r.Representations[0].Element
		}
	}
	return ""
}

// RequestElement returns the document element of the method's request
// body, or "" if it has none.
func (m *Method) RequestElement() string {
	if len(m.Request) == 0 {
		return ""
	}
	return m.Request[0].Element
}

// Statuses returns the explicit status codes of the method's responses.
func (m *Method) Statuses() []int {
	var out []int
	for _, r := range m.Responses {
		out = append(out, r.Statuses...)
	}
	return out
}

// Renamed returns a copy of r with the ID id and the sample path path, whose
// representations name the elements elements maps their names to.
func (r *Resource) Renamed(id, path string, elements map[string]string) *Resource {
	rename := func(reps []Representation) []Representation {
		out := make([]Representation, len(reps))
		for i, rep := range reps {
			if to, ok := elements[rep.Element]; ok {
				rep.Element = to
			}
			out[i] = rep
		}
		return out
	}
	c := &Resource{ID: id, SamplePath: path}
	for _, m := range r.Methods {
		mc := *m
		mc.QueryParams = append([]string(nil), m.QueryParams...)
		mc.Request = rename(m.Request)
		mc.Responses = make([]Response, len(m.Responses))
		for i, resp := range m.Responses {
			resp.Statuses = append([]int(nil), resp.Statuses...)
			resp.Representations = rename(resp.Representations)
			resp.HeaderParams = append([]string(nil), resp.HeaderParams...)
			mc.Responses[i] = resp
		}
		c.Methods = append(c.Methods, &mc)
	}
	return c
}
