// SPDX-License-Identifier: MIT

package ieee20305

// LinkRef is a link a resource holds to another resource: an element whose
// type derives from Link, such as EndDeviceListLink.
type LinkRef struct {
	// Element is the name of the element holding the link, such as
	// "EndDeviceListLink" or "opModFreqWatt".
	Element string
	// Type is the link type, such as "EndDeviceListLink".
	Type string
	// Resource is the ID of the WADL resource the link points to, such as
	// "EndDeviceList": see LookupResource.
	Resource string
	// Href is the URI of the resource.
	Href string
	// All is the number of items of a list link, when the server gives it.
	All *UInt32
}

// Linker is implemented by every struct type that can hold links.
type Linker interface {
	AppendLinks(out []LinkRef) []LinkRef
}

// Links returns the links v holds, in document order, or nil when v holds
// none. v is a pointer to a struct type of this package or of a profile
// package such as csipaus.
func Links(v any) []LinkRef {
	if l, ok := v.(Linker); ok {
		return l.AppendLinks(nil)
	}
	return nil
}

// Lister is implemented by every list type, such as *EndDeviceList.
type Lister interface {
	// ListItems returns pointers to the items of the list.
	ListItems() []any
	// SetListItems replaces the items of the list.
	SetListItems(items []any) error
	// NewListItem returns a pointer to a new item of the list.
	NewListItem() any
	// ListItemElement returns the element name of the items.
	ListItemElement() string
	// SetListCounts sets the all and results attributes of the list.
	SetListCounts(all, results uint32)
}

// Clone returns a deep copy of e.
func (e ExtensionElement) Clone() ExtensionElement {
	c := ExtensionElement{XMLName: e.XMLName}
	if e.Attrs != nil {
		c.Attrs = append(c.Attrs, e.Attrs...)
	}
	if e.InnerXML != nil {
		c.InnerXML = append([]byte{}, e.InnerXML...)
	}
	return c
}

// CloneExtensions returns a deep copy of extension elements.
func CloneExtensions(es []ExtensionElement) []ExtensionElement {
	if es == nil {
		return nil
	}
	out := make([]ExtensionElement, len(es))
	for i, e := range es {
		out[i] = e.Clone()
	}
	return out
}
