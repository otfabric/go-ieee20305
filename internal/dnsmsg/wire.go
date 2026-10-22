// SPDX-License-Identifier: MIT

package dnsmsg

import (
	"encoding/binary"
	"net/netip"
	"strings"
)

const headerLen = 12

// Encode returns the wire form of m. Names in the question and record
// sections and the targets of PTR and SRV records are compressed against
// the names written before them, as multicast DNS allows (RFC 6762,
// section 18.14).
func (m *Message) Encode() ([]byte, error) {
	e := encoder{b: make([]byte, headerLen, 512), names: map[string]int{}}
	counts := []int{len(m.Questions), len(m.Answers), len(m.Authorities), len(m.Additionals)}
	for _, n := range counts {
		if n > 0xffff {
			return nil, errorf("%d entries in a section", n)
		}
	}
	binary.BigEndian.PutUint16(e.b[0:], m.ID)
	binary.BigEndian.PutUint16(e.b[2:], m.flags())
	for i, n := range counts {
		binary.BigEndian.PutUint16(e.b[4+2*i:], uint16(n))
	}
	for _, q := range m.Questions {
		if err := e.name(q.Name); err != nil {
			return nil, err
		}
		class := uint16(q.Class) &^ topBit
		if q.Unicast {
			class |= topBit
		}
		e.b = binary.BigEndian.AppendUint16(e.b, uint16(q.Type))
		e.b = binary.BigEndian.AppendUint16(e.b, class)
	}
	for _, section := range [][]Resource{m.Answers, m.Authorities, m.Additionals} {
		for _, r := range section {
			if err := e.resource(r); err != nil {
				return nil, err
			}
		}
	}
	return e.b, nil
}

type encoder struct {
	b []byte
	// names maps the presentation form of each name suffix written so far
	// to its offset.
	names map[string]int
}

func (e *encoder) name(name string) error {
	labels, err := SplitName(name)
	if err != nil {
		return err
	}
	escaped := make([]string, len(labels))
	n := 1
	for i, l := range labels {
		if len(l) > maxLabel {
			return errorf("label of %d bytes in %q", len(l), name)
		}
		n += 1 + len(l)
		escaped[i] = JoinLabel(l, "")
	}
	if n > maxName {
		return errorf("name of %d bytes: %q", n, name)
	}
	for i, l := range labels {
		suffix := strings.Join(escaped[i:], ".")
		if off, ok := e.names[suffix]; ok {
			e.b = binary.BigEndian.AppendUint16(e.b, 0xc000|uint16(off))
			return nil
		}
		if len(e.b) <= maxPtr {
			e.names[suffix] = len(e.b)
		}
		e.b = append(e.b, byte(len(l)))
		e.b = append(e.b, l...)
	}
	e.b = append(e.b, 0)
	return nil
}

func (e *encoder) resource(r Resource) error {
	if r.Data == nil {
		return errorf("record %q has no data", r.Name)
	}
	if err := e.name(r.Name); err != nil {
		return err
	}
	class := uint16(r.Class) &^ topBit
	if r.CacheFlush {
		class |= topBit
	}
	e.b = binary.BigEndian.AppendUint16(e.b, uint16(r.Data.Type()))
	e.b = binary.BigEndian.AppendUint16(e.b, class)
	e.b = binary.BigEndian.AppendUint32(e.b, r.TTL)
	lenAt := len(e.b)
	e.b = append(e.b, 0, 0)
	switch d := r.Data.(type) {
	case *PTR:
		if err := e.name(d.Target); err != nil {
			return err
		}
	case *SRV:
		e.b = binary.BigEndian.AppendUint16(e.b, d.Priority)
		e.b = binary.BigEndian.AppendUint16(e.b, d.Weight)
		e.b = binary.BigEndian.AppendUint16(e.b, d.Port)
		if err := e.name(d.Target); err != nil {
			return err
		}
	case *TXT:
		if len(d.Strings) == 0 {
			// RFC 6763, section 6.1: an empty TXT record holds one
			// empty string.
			e.b = append(e.b, 0)
		}
		for _, s := range d.Strings {
			if len(s) > 255 {
				return errorf("TXT string of %d bytes in %q", len(s), r.Name)
			}
			e.b = append(e.b, byte(len(s)))
			e.b = append(e.b, s...)
		}
	case *A:
		if !d.Addr.Unmap().Is4() {
			return errorf("A record %q with address %v", r.Name, d.Addr)
		}
		a := d.Addr.Unmap().As4()
		e.b = append(e.b, a[:]...)
	case *AAAA:
		if !d.Addr.IsValid() {
			return errorf("AAAA record %q without an address", r.Name)
		}
		a := d.Addr.As16()
		e.b = append(e.b, a[:]...)
	case *Unknown:
		e.b = append(e.b, d.Data...)
	default:
		return errorf("record %q has data of type %T", r.Name, r.Data)
	}
	n := len(e.b) - lenAt - 2
	if n > 0xffff {
		return errorf("record %q has %d bytes of data", r.Name, n)
	}
	binary.BigEndian.PutUint16(e.b[lenAt:], uint16(n))
	return nil
}

// Parse decodes the message b. Bytes after the last record are ignored.
func Parse(b []byte) (*Message, error) {
	if len(b) < headerLen {
		return nil, errorf("message of %d bytes", len(b))
	}
	d := decoder{b: b, off: headerLen}
	m := &Message{Header: headerFrom(d.u16at(0), d.u16at(2))}
	qd := int(d.u16at(4))
	for i := 0; i < qd; i++ {
		name, err := d.name()
		if err != nil {
			return nil, err
		}
		if d.off+4 > len(b) {
			return nil, errorf("question %q is truncated", name)
		}
		class := d.u16at(d.off + 2)
		m.Questions = append(m.Questions, Question{
			Name:    name,
			Type:    Type(d.u16at(d.off)),
			Class:   Class(class &^ topBit),
			Unicast: class&topBit != 0,
		})
		d.off += 4
	}
	for i, section := range []*[]Resource{&m.Answers, &m.Authorities, &m.Additionals} {
		n := int(d.u16at(6 + 2*i))
		for j := 0; j < n; j++ {
			r, err := d.resource()
			if err != nil {
				return nil, err
			}
			*section = append(*section, r)
		}
	}
	return m, nil
}

type decoder struct {
	b   []byte
	off int
}

func (d *decoder) u16at(off int) uint16 { return binary.BigEndian.Uint16(d.b[off:]) }

// name decodes the name at d.off and advances past it.
func (d *decoder) name() (string, error) {
	name, end, err := d.nameAt(d.off)
	if err != nil {
		return "", err
	}
	d.off = end
	return name, nil
}

// nameAt decodes the name at off and returns it with the offset after its
// first pointer or its terminating zero. A pointer must point before the
// offset of the previous pointer, so that decoding ends.
func (d *decoder) nameAt(off int) (string, int, error) {
	var sb strings.Builder
	end := -1
	limit := off
	wire := 1
	for {
		if off >= len(d.b) {
			return "", 0, errorf("name at offset %d is truncated", off)
		}
		c := int(d.b[off])
		switch c & 0xc0 {
		case 0x00:
			if c == 0 {
				if end < 0 {
					end = off + 1
				}
				return sb.String(), end, nil
			}
			if off+1+c > len(d.b) {
				return "", 0, errorf("label at offset %d is truncated", off)
			}
			if wire += 1 + c; wire > maxName {
				return "", 0, errorf("name at offset %d is longer than %d bytes", off, maxName)
			}
			if sb.Len() > 0 {
				sb.WriteByte('.')
			}
			sb.WriteString(JoinLabel(string(d.b[off+1:off+1+c]), ""))
			off += 1 + c
		case 0xc0:
			if off+2 > len(d.b) {
				return "", 0, errorf("pointer at offset %d is truncated", off)
			}
			target := int(d.u16at(off) & maxPtr)
			if target >= limit {
				return "", 0, errorf("pointer at offset %d points forward to %d", off, target)
			}
			if end < 0 {
				end = off + 2
			}
			limit, off = target, target
		default:
			return "", 0, errorf("label type %#x at offset %d", c&0xc0, off)
		}
	}
}

func (d *decoder) resource() (Resource, error) {
	name, err := d.name()
	if err != nil {
		return Resource{}, err
	}
	if d.off+10 > len(d.b) {
		return Resource{}, errorf("record %q is truncated", name)
	}
	t := Type(d.u16at(d.off))
	class := d.u16at(d.off + 2)
	r := Resource{
		Name:       name,
		Class:      Class(class &^ topBit),
		CacheFlush: class&topBit != 0,
		TTL:        binary.BigEndian.Uint32(d.b[d.off+4:]),
	}
	start := d.off + 10
	end := start + int(d.u16at(d.off+8))
	if end > len(d.b) {
		return Resource{}, errorf("data of record %q is truncated", name)
	}
	rdata := d.b[start:end]
	d.off = end
	// nameIn decodes a name that must end exactly at the end of the data.
	nameIn := func(off int) (string, error) {
		n, after, err := d.nameAt(off)
		if err != nil {
			return "", err
		}
		if after != end {
			return "", errorf("data of %v record %q has %d bytes, not %d", t, name, after-start, end-start)
		}
		return n, nil
	}
	switch t {
	case TypePTR:
		target, err := nameIn(start)
		if err != nil {
			return Resource{}, err
		}
		r.Data = &PTR{Target: target}
	case TypeSRV:
		if len(rdata) < 7 {
			return Resource{}, errorf("SRV record %q of %d bytes", name, len(rdata))
		}
		target, err := nameIn(start + 6)
		if err != nil {
			return Resource{}, err
		}
		r.Data = &SRV{
			Priority: d.u16at(start),
			Weight:   d.u16at(start + 2),
			Port:     d.u16at(start + 4),
			Target:   target,
		}
	case TypeTXT:
		txt := &TXT{Strings: []string{}}
		for i := 0; i < len(rdata); {
			n := int(rdata[i])
			if i+1+n > len(rdata) {
				return Resource{}, errorf("TXT record %q is truncated", name)
			}
			txt.Strings = append(txt.Strings, string(rdata[i+1:i+1+n]))
			i += 1 + n
		}
		r.Data = txt
	case TypeA:
		if len(rdata) != 4 {
			return Resource{}, errorf("A record %q of %d bytes", name, len(rdata))
		}
		r.Data = &A{Addr: netip.AddrFrom4([4]byte(rdata))}
	case TypeAAAA:
		if len(rdata) != 16 {
			return Resource{}, errorf("AAAA record %q of %d bytes", name, len(rdata))
		}
		r.Data = &AAAA{Addr: netip.AddrFrom16([16]byte(rdata))}
	default:
		r.Data = &Unknown{T: t, Data: append([]byte{}, rdata...)}
	}
	return r, nil
}
