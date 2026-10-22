// SPDX-License-Identifier: MIT

package ieee20305

import "encoding/xml"

// ExtensionElement is an element in a place where the schema admits elements
// it does not declare (xs:any): extensions in other namespaces, such as
// CSIP-AUS elements read through this package, and elements of later
// revisions of IEEE 2030.5. Decoding keeps them, so that a document can be
// encoded again without losing them.
//
// The content is kept as raw XML. Namespace prefixes it uses that are
// declared outside the element are not carried along.
type ExtensionElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	InnerXML []byte     `xml:",innerxml"`
}

// UnmarshalXML decodes the element, dropping namespace declarations from
// its attributes: the encoder writes the declarations it needs itself.
func (e *ExtensionElement) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain ExtensionElement
	if err := d.DecodeElement((*plain)(e), &start); err != nil {
		return err
	}
	attrs := e.Attrs[:0]
	for _, a := range e.Attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		attrs = append(attrs, a)
	}
	if len(attrs) == 0 {
		attrs = nil
	}
	e.Attrs = attrs
	return nil
}
