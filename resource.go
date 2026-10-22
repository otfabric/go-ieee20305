// SPDX-License-Identifier: MIT

package ieee20305

// GetHref returns the href of the resource, or "" when it has none. Every
// document type embeds Resource and so has GetHref.
func (r *Resource) GetHref() string {
	if r == nil || r.Href == nil {
		return ""
	}
	return *r.Href
}

// SetHref sets the href of the resource.
func (r *Resource) SetHref(href string) { r.Href = &href }

// Hrefer is implemented by every document type: see Resource.
type Hrefer interface {
	GetHref() string
	SetHref(href string)
}
