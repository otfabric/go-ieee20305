// SPDX-License-Identifier: MIT

// Package dnsmsg encodes and decodes DNS messages (RFC 1035) to the extent
// that multicast DNS and DNS-based service discovery (RFC 6762, RFC 6763)
// need: the header, questions, and answer, authority and additional
// records, with PTR, SRV, TXT, A and AAAA data decoded and other types
// kept as raw bytes. Names are compressed when encoding and decompressed
// when decoding. The top bit of a question's class is the mDNS
// unicast-response (QU) bit and that of a record's class the cache-flush
// bit; both are reported separately from the class.
//
// A name is written in presentation form without the trailing dot, with
// the labels separated by dots: a dot inside a label is escaped as `\.`
// and a backslash as `\\`, so that service instance names such as
// "Meter 1.5" survive (RFC 6763, section 4.3). The root name is "".
package dnsmsg

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Type is a resource record type.
type Type uint16

// Resource record types.
const (
	TypeA    Type = 1
	TypePTR  Type = 12
	TypeTXT  Type = 16
	TypeAAAA Type = 28
	TypeSRV  Type = 33
	TypeNSEC Type = 47
	TypeANY  Type = 255
)

// String returns the mnemonic of t, or TYPEn for a type without one.
func (t Type) String() string {
	switch t {
	case TypeA:
		return "A"
	case TypePTR:
		return "PTR"
	case TypeTXT:
		return "TXT"
	case TypeAAAA:
		return "AAAA"
	case TypeSRV:
		return "SRV"
	case TypeNSEC:
		return "NSEC"
	case TypeANY:
		return "ANY"
	default:
		return fmt.Sprintf("TYPE%d", uint16(t))
	}
}

// Class is a resource record class, without the mDNS top bit.
type Class uint16

// Resource record classes.
const (
	ClassINET Class = 1
	ClassANY  Class = 255
)

// topBit is the mDNS unicast-response bit of a question's class and the
// cache-flush bit of a record's class.
const topBit = 0x8000

// Limits of the wire format.
const (
	maxLabel = 63
	maxName  = 255
	maxPtr   = 0x3fff
)

// ErrFormat is matched by every error that Parse returns for a malformed
// message and that Message.Encode returns for a message it cannot encode.
var ErrFormat = errors.New("dnsmsg: malformed message")

func errorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrFormat}, args...)...)
}

// Header is the fixed part of a message. The Z, AD and CD bits are
// neither decoded nor encoded.
type Header struct {
	ID                 uint16
	Response           bool
	Opcode             uint8 // 4 bits
	Authoritative      bool
	Truncated          bool
	RecursionDesired   bool
	RecursionAvailable bool
	RCode              uint8 // 4 bits
}

func (h Header) flags() uint16 {
	f := uint16(h.Opcode&0xf)<<11 | uint16(h.RCode&0xf)
	for _, b := range []struct {
		set bool
		bit uint16
	}{
		{h.Response, 1 << 15},
		{h.Authoritative, 1 << 10},
		{h.Truncated, 1 << 9},
		{h.RecursionDesired, 1 << 8},
		{h.RecursionAvailable, 1 << 7},
	} {
		if b.set {
			f |= b.bit
		}
	}
	return f
}

func headerFrom(id, f uint16) Header {
	return Header{
		ID:                 id,
		Response:           f&(1<<15) != 0,
		Opcode:             uint8(f>>11) & 0xf,
		Authoritative:      f&(1<<10) != 0,
		Truncated:          f&(1<<9) != 0,
		RecursionDesired:   f&(1<<8) != 0,
		RecursionAvailable: f&(1<<7) != 0,
		RCode:              uint8(f & 0xf),
	}
}

// Question is an entry of the question section.
type Question struct {
	Name  string
	Type  Type
	Class Class
	// Unicast is the mDNS QU bit: the querier asks for a unicast response.
	Unicast bool
}

// Resource is a resource record.
type Resource struct {
	Name  string
	Class Class
	// CacheFlush is the mDNS cache-flush bit: this record replaces the
	// cached records of the same name and type.
	CacheFlush bool
	TTL        uint32
	Data       RData
}

// Type returns the type of r's data.
func (r Resource) Type() Type {
	if r.Data == nil {
		return 0
	}
	return r.Data.Type()
}

// RData is the data of a resource record: *PTR, *SRV, *TXT, *A, *AAAA or
// *Unknown.
type RData interface {
	Type() Type
}

// PTR is the data of a PTR record.
type PTR struct{ Target string }

// SRV is the data of an SRV record (RFC 2782).
type SRV struct {
	Priority, Weight, Port uint16
	Target                 string
}

// TXT is the data of a TXT record: a sequence of strings of up to 255
// bytes each.
type TXT struct{ Strings []string }

// A is the data of an A record.
type A struct{ Addr netip.Addr }

// AAAA is the data of an AAAA record.
type AAAA struct{ Addr netip.Addr }

// Unknown is the data of a record of a type this package does not decode.
type Unknown struct {
	T    Type
	Data []byte
}

// Type implements RData.
func (*PTR) Type() Type { return TypePTR }

// Type implements RData.
func (*SRV) Type() Type { return TypeSRV }

// Type implements RData.
func (*TXT) Type() Type { return TypeTXT }

// Type implements RData.
func (*A) Type() Type { return TypeA }

// Type implements RData.
func (*AAAA) Type() Type { return TypeAAAA }

// Type implements RData.
func (u *Unknown) Type() Type { return u.T }

// Message is a DNS message.
type Message struct {
	Header
	Questions   []Question
	Answers     []Resource
	Authorities []Resource
	Additionals []Resource
}

// EqualNames reports whether two names in presentation form are the same
// DNS name, comparing ASCII letters without regard to case. A trailing
// dot is ignored.
func EqualNames(a, b string) bool {
	return strings.EqualFold(trimDot(a), trimDot(b))
}

// trimDot removes a trailing dot from name unless it is escaped.
func trimDot(name string) string {
	if !strings.HasSuffix(name, ".") {
		return name
	}
	n := 0
	for i := len(name) - 2; i >= 0 && name[i] == '\\'; i-- {
		n++
	}
	if n%2 == 1 {
		return name
	}
	return name[:len(name)-1]
}

// JoinLabel returns the name made of the label l, which may contain dots
// and backslashes, followed by the name domain.
func JoinLabel(l, domain string) string {
	l = strings.NewReplacer(`\`, `\\`, `.`, `\.`).Replace(l)
	if domain == "" {
		return l
	}
	return l + "." + domain
}

// SplitName returns the labels of name, with escapes removed.
func SplitName(name string) ([]string, error) {
	if name == "" || name == "." {
		return nil, nil
	}
	var labels []string
	var l []byte
	for i := 0; i < len(name); i++ {
		switch c := name[i]; c {
		case '\\':
			i++
			if i == len(name) {
				return nil, errorf("name %q ends with a backslash", name)
			}
			l = append(l, name[i])
		case '.':
			if len(l) == 0 {
				return nil, errorf("name %q has an empty label", name)
			}
			labels = append(labels, string(l))
			l = l[:0]
		default:
			l = append(l, c)
		}
	}
	if len(l) > 0 {
		labels = append(labels, string(l))
	}
	return labels, nil
}
