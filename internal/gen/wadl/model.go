// SPDX-License-Identifier: MIT

package wadl

// Application is the root of a parsed WADL document.
type Application struct {
	Resources []Resource
}

// Resource describes a single REST resource with its URI template,
// supported HTTP methods, and optional child resources.
type Resource struct {
	Path     string
	Params   []Param
	Methods  []Method
	Children []Resource
}

// Param describes a URI template parameter (e.g. {eDevId}).
type Param struct {
	Name  string
	Style string // "template", "query"
	Type  string // XSD type (e.g. "xs:string")
}

// Method describes an HTTP method on a resource.
type Method struct {
	Name     string // GET, POST, PUT, DELETE
	Request  *Representation
	Response *Representation
}

// Representation describes the media type and XSD element of a
// request or response body.
type Representation struct {
	MediaType string // e.g. "application/sep+xml"
	Element   string // local name of the XSD element/type
}

// FlatResource is a resolved resource with its full URI pattern
// and all methods collected from the WADL tree.
type FlatResource struct {
	Path    string
	Params  []Param
	Methods []Method
}

// Flatten walks the resource tree and returns a flat list of
// resources with fully-qualified URI paths.
func (a *Application) Flatten() []FlatResource {
	var out []FlatResource
	for _, r := range a.Resources {
		flattenResource("", r, &out)
	}
	return out
}

func flattenResource(prefix string, r Resource, out *[]FlatResource) {
	path := prefix + r.Path
	if len(r.Methods) > 0 {
		*out = append(*out, FlatResource{
			Path:    path,
			Params:  r.Params,
			Methods: r.Methods,
		})
	}
	for _, child := range r.Children {
		flattenResource(path, child, out)
	}
}
