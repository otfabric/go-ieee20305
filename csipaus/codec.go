// SPDX-License-Identifier: MIT

package csipaus

import (
	"encoding/xml"
	"fmt"

	ieee20305 "github.com/otfabric/go-ieee20305"
	sepxml "github.com/otfabric/go-ieee20305/internal/xml"
)

// Marshal encodes v as an XML document. v is a document type of this package,
// such as *DERControl, or of the base package for documents CSIP-AUS does not
// change.
//
// CSIP-AUS v1.3 profiles IEEE 2030.5-2018, whose schema does not allow the
// schemaVer attribute of IEEE 2030.5-2023, so Marshal does not write it.
func Marshal(v any) ([]byte, error) {
	name, ok := elementName(v)
	if !ok {
		return nil, fmt.Errorf("%w: %T is not an IEEE 2030.5 or CSIP-AUS document type", ieee20305.ErrInvalid, v)
	}
	return sepxml.Marshal(name, "", v)
}

// Unmarshal decodes an XML document into v, a pointer to a document type of
// this package or of the base package. An error in the document matches
// ieee20305.ErrProtocol.
func Unmarshal(data []byte, v any) error {
	name, ok := elementName(v)
	if !ok {
		return fmt.Errorf("%w: %T is not an IEEE 2030.5 or CSIP-AUS document type", ieee20305.ErrInvalid, v)
	}
	if err := sepxml.Unmarshal(data, name, v); err != nil {
		return fmt.Errorf("%w: %v", ieee20305.ErrProtocol, err)
	}
	return nil
}

// Decode decodes an XML document of any document type. Documents that
// CSIP-AUS changes decode into the types of this package, others into the
// types of the base package.
func Decode(data []byte) (any, error) {
	v, err := sepxml.Decode(data, func(name xml.Name) any {
		if v := NewElement(name); v != nil {
			return v
		}
		return ieee20305.NewElement(name)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ieee20305.ErrProtocol, err)
	}
	return v, nil
}

func elementName(v any) (xml.Name, bool) {
	if name, ok := ElementName(v); ok {
		return name, true
	}
	return ieee20305.ElementName(v)
}
