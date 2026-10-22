// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/wadl"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

// Resources generates resources_gen.go, the resource table of a WADL. Every
// representation must name a document element of set.
func Resources(app *wadl.Application, set *xsd.Set, pkg, header string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`// Conformance is the support IEEE 2030.5 requires of a server for a
// method on a resource (the wx:mode of the WADL).
type Conformance uint8

// Conformance levels.
const (
	// ConformanceMandatory: the server supports the method.
	ConformanceMandatory Conformance = iota + 1
	// ConformanceOptional: the server may support the method.
	ConformanceOptional
	// ConformanceDiscouraged: the server should not support the method.
	ConformanceDiscouraged
	// ConformanceError: the server answers the method with an error.
	ConformanceError
)

// ResourceSpec describes a resource of the IEEE 2030.5 WADL.
type ResourceSpec struct {
	// ID is the resource id of the WADL, such as "EndDeviceList".
	ID string
	// SamplePath is the sample URI of the WADL. Servers choose their own
	// URIs; clients follow links rather than build paths.
	SamplePath string
	Methods    []MethodSpec
}

// MethodSpec describes an HTTP method on a resource.
type MethodSpec struct {
	Method      string
	Conformance Conformance
	// Request and Response are the document elements of the bodies, or
	// "" for none.
	Request  string
	Response string
	// QueryParams are the query parameters the method accepts.
	QueryParams []string
	// Statuses are the status codes the WADL lists for the method.
	Statuses []int
}

// Resources lists the resources of the IEEE 2030.5 WADL, sorted by ID.
var Resources = []ResourceSpec{
`)
	resources := append([]*wadl.Resource{}, app.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
	modes := map[string]string{"M": "ConformanceMandatory", "O": "ConformanceOptional", "D": "ConformanceDiscouraged", "E": "ConformanceError"}
	for _, r := range resources {
		fmt.Fprintf(&b, "\t{\n\t\tID: %q,\n\t\tSamplePath: %q,\n\t\tMethods: []MethodSpec{\n", r.ID, r.SamplePath)
		for _, m := range r.Methods {
			for _, reps := range [][]wadl.Representation{m.Request} {
				if err := checkReps(reps, set, r.ID); err != nil {
					return nil, err
				}
			}
			for _, resp := range m.Responses {
				if err := checkReps(resp.Representations, set, r.ID); err != nil {
					return nil, err
				}
			}
			fmt.Fprintf(&b, "\t\t\t{Method: %q, Conformance: %s", m.Name, modes[m.Mode])
			if e := m.RequestElement(); e != "" {
				fmt.Fprintf(&b, ", Request: %q", e)
			}
			if e := m.ResponseElement(); e != "" {
				fmt.Fprintf(&b, ", Response: %q", e)
			}
			if len(m.QueryParams) > 0 {
				qs := make([]string, len(m.QueryParams))
				for i, q := range m.QueryParams {
					qs[i] = strconv.Quote(q)
				}
				fmt.Fprintf(&b, ", QueryParams: []string{%s}", strings.Join(qs, ", "))
			}
			if st := m.Statuses(); len(st) > 0 {
				ss := make([]string, len(st))
				for i, s := range st {
					ss[i] = strconv.Itoa(s)
				}
				fmt.Fprintf(&b, ", Statuses: []int{%s}", strings.Join(ss, ", "))
			}
			b.WriteString("},\n")
		}
		b.WriteString("\t\t},\n\t},\n")
	}
	b.WriteString(`}

// LookupResource returns the resource with the given WADL id.
func LookupResource(id string) (ResourceSpec, bool) {
	i := sort.Search(len(Resources), func(i int) bool { return Resources[i].ID >= id })
	if i < len(Resources) && Resources[i].ID == id {
		return Resources[i], true
	}
	return ResourceSpec{}, false
}
`)
	g := &gen{cfg: Config{Package: pkg, Header: header}}
	return g.file(b.String())
}

func checkReps(reps []wadl.Representation, set *xsd.Set, id string) error {
	for _, r := range reps {
		if r.Element == "" {
			continue
		}
		if _, ok := set.Elements[xsd.QName{Space: r.ElementSpace, Local: r.Element}]; !ok {
			return fmt.Errorf("resource %s: representation element {%s}%s is not a global element of the schema", id, r.ElementSpace, r.Element)
		}
	}
	return nil
}
