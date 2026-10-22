// SPDX-License-Identifier: MIT

package ieee20305

import (
	"fmt"

	sepxml "github.com/otfabric/go-ieee20305/internal/xml"
)

// MediaType is the media type of IEEE 2030.5 XML documents.
const MediaType = "application/sep+xml"

// Codec encodes and decodes IEEE 2030.5 documents. StandardCodec is the one
// of this package; a profile package such as csipaus has its own.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
	Decode(data []byte) (any, error)
}

// StandardCodec is the Codec of Marshal, Unmarshal and Decode.
var StandardCodec Codec = standardCodec{}

type standardCodec struct{}

func (standardCodec) Marshal(v any) ([]byte, error)      { return Marshal(v) }
func (standardCodec) Unmarshal(data []byte, v any) error { return Unmarshal(data, v) }
func (standardCodec) Decode(data []byte) (any, error)    { return Decode(data) }

// Marshal encodes v, a document type such as *EndDevice or *DERControlList,
// as an IEEE 2030.5 XML document with schemaVer set to SchemaVersion.
func Marshal(v any) ([]byte, error) {
	name, ok := ElementName(v)
	if !ok {
		return nil, fmt.Errorf("%w: %T is not an IEEE 2030.5 document type", ErrInvalid, v)
	}
	return sepxml.Marshal(name, SchemaVersion, v)
}

// Unmarshal decodes an IEEE 2030.5 XML document into v, a pointer to a
// document type. The document element must be the one of v's type. An error
// in the document matches ErrProtocol.
func Unmarshal(data []byte, v any) error {
	name, ok := ElementName(v)
	if !ok {
		return fmt.Errorf("%w: %T is not an IEEE 2030.5 document type", ErrInvalid, v)
	}
	if err := sepxml.Unmarshal(data, name, v); err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return nil
}

// Decode decodes an IEEE 2030.5 XML document of any document type and
// returns a pointer to it, such as *EndDevice. An error in the document
// matches ErrProtocol.
func Decode(data []byte) (any, error) {
	v, err := sepxml.Decode(data, NewElement)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return v, nil
}
