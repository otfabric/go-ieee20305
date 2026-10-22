// SPDX-License-Identifier: MIT

package ieee20305

import (
	"fmt"

	sepxml "github.com/otfabric/go-ieee20305/internal/xml"
)

// Edition is an edition of IEEE 2030.5. The types of this package follow
// IEEE 2030.5-2023; a document reduced to what IEEE 2030.5-2018 defines is a
// 2018 document, which 2018 implementations read.
type Edition uint8

// Editions of IEEE 2030.5.
const (
	// Edition2023 is IEEE 2030.5-2023, the edition the types follow.
	Edition2023 Edition = iota
	// Edition2018 is IEEE 2030.5-2018.
	Edition2018
)

func (e Edition) String() string {
	switch e {
	case Edition2023:
		return "2023"
	case Edition2018:
		return "2018"
	}
	return fmt.Sprintf("Edition(%d)", uint8(e))
}

// ParseEdition returns the edition named "2018" or "2023".
func ParseEdition(s string) (Edition, error) {
	switch s {
	case "2023":
		return Edition2023, nil
	case "2018":
		return Edition2018, nil
	}
	return 0, fmt.Errorf("%w: %q is not an edition of IEEE 2030.5 (2018 or 2023)", ErrInvalid, s)
}

// Stripper is implemented by every struct type: Strip2018 removes what IEEE
// 2030.5-2018 does not define.
type Stripper interface {
	Strip2018()
}

// MarshalEdition encodes v, a pointer to a document type, as a document of
// edition e. For 2018 it encodes a copy of v reduced to what 2018 defines,
// without the schemaVer attribute, which 2018 does not define either.
func MarshalEdition(v any, e Edition) ([]byte, error) {
	switch e {
	case Edition2023:
		return Marshal(v)
	case Edition2018:
	default:
		return nil, fmt.Errorf("%w: %v", ErrInvalid, e)
	}
	name, ok := ElementName(v)
	if !ok {
		return nil, fmt.Errorf("%w: %T is not an IEEE 2030.5 document type", ErrInvalid, v)
	}
	c, ok := Clone(v).(Stripper)
	if !ok {
		return nil, fmt.Errorf("%w: MarshalEdition needs a pointer to a document type, not a %T", ErrInvalid, v)
	}
	c.Strip2018()
	return sepxml.Marshal(name, "", c)
}

// InEdition reports whether the WADL resource id is a resource of edition
// e: see LookupResource.
func InEdition(id string, e Edition) bool {
	if e == Edition2018 {
		return InEdition2018(id)
	}
	_, ok := LookupResource(id)
	return ok
}

// Codec returns the Codec of the documents of edition e: it encodes with
// MarshalEdition and decodes documents of either edition.
func (e Edition) Codec() Codec { return editionCodec{e} }

type editionCodec struct{ e Edition }

func (c editionCodec) Marshal(v any) ([]byte, error)      { return MarshalEdition(v, c.e) }
func (c editionCodec) Unmarshal(data []byte, v any) error { return Unmarshal(data, v) }
func (c editionCodec) Decode(data []byte) (any, error)    { return Decode(data) }
