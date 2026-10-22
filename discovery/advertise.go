// SPDX-License-Identifier: MIT

package discovery

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// TTLs of the records of an advertisement (RFC 6762, section 10): records
// with a host name in them live 120 seconds, the others 75 minutes.
const (
	hostTTL  = 120
	otherTTL = 4500
	// legacyTTL caps the TTLs of answers to legacy unicast queries
	// (RFC 6762, section 6.7).
	legacyTTL = 10
)

// announceInterval is the interval between the two announcements of an
// advertisement, a variable so that tests can shorten it.
var announceInterval = time.Second

// enumName is the DNS-SD service type enumeration name (RFC 6763,
// section 9).
const enumName = "_services._dns-sd._udp." + Domain

// AdvertiseOptions configures Advertise.
type AdvertiseOptions struct {
	// Interface is the network interface to advertise on when Transport
	// is nil, and whose addresses are advertised when the Service has
	// none. Nil means the system's default multicast interface and the
	// addresses of every interface that is up.
	Interface *net.Interface
	// Transport sends and receives the packets. Nil means a transport
	// from NewMulticastTransport(Interface). The Advertisement closes it
	// when it is closed.
	Transport PacketTransport
	// Logger receives the advertisement's log (see ieee20305.Logger), such
	// as answers that cannot be sent; none when nil.
	Logger ieee20305.Logger
}

// Advertisement is a service being advertised by Advertise.
type Advertisement struct {
	t       PacketTransport
	log     ieee20305.Logger
	svc     Service
	records []dnsmsg.Resource

	mu     sync.Mutex // serialises sends with closing
	closed bool

	closing   chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	err       error
}

// Advertise announces s as an IEEE 2030.5 server with multicast DNS: it
// sends two announcements a second apart, answers the queries that match
// its records, and sends goodbyes when it is closed or ctx is done.
//
// s needs an Instance, a Port and a TXT record with a dcap path starting
// with "/"; "txtvers=1" is added when missing. Host is put in the .local
// domain and defaults to the host name of the system; Addrs default to
// the addresses of
// the interfaces (see AdvertiseOptions.Interface). The service is
// announced under ServiceType and each of its Subtypes. Advertise does
// not probe for name conflicts (RFC 6762, section 8).
func Advertise(ctx context.Context, s Service, o AdvertiseOptions) (*Advertisement, error) {
	s, err := completeService(s, o.Interface)
	if err != nil {
		return nil, err
	}
	t := o.Transport
	if t == nil {
		if t, err = NewMulticastTransport(o.Interface); err != nil {
			return nil, err
		}
	}
	a := &Advertisement{
		t:       t,
		log:     ieee20305.ComponentLogger(o.Logger, "discovery"),
		svc:     s,
		records: records(s),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	if err := a.announce(); err != nil {
		_ = t.Close()
		return nil, err
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		select {
		case <-time.After(announceInterval):
			if err := a.announce(); err != nil {
				a.log.Warnf("announcing %s again: %v", a.svc.Instance, err)
			}
		case <-a.closing:
		}
	}()
	go a.serve()
	go func() {
		select {
		case <-ctx.Done():
			_ = a.Close()
		case <-a.closing:
		}
	}()
	return a, nil
}

// Service returns the service as advertised, with its defaults filled in.
func (a *Advertisement) Service() Service { return a.svc }

// Close sends goodbyes for the records of the advertisement, stops
// answering queries and closes the transport. It returns the error of
// sending the goodbyes or closing the transport, the same on every call.
func (a *Advertisement) Close() error {
	a.closeOnce.Do(func() {
		close(a.closing)
		a.wg.Wait()
		a.mu.Lock()
		bye := make([]dnsmsg.Resource, len(a.records))
		for i, r := range a.records {
			r.TTL = 0
			bye[i] = r
		}
		err := a.sendLocked(&dnsmsg.Message{Header: responseHeader, Answers: bye}, netip.AddrPort{})
		a.closed = true
		a.mu.Unlock()
		a.err = errors.Join(err, a.t.Close())
		<-a.done
	})
	return a.err
}

var responseHeader = dnsmsg.Header{Response: true, Authoritative: true}

func (a *Advertisement) announce() error {
	return a.send(&dnsmsg.Message{Header: responseHeader, Answers: a.records}, netip.AddrPort{})
}

func (a *Advertisement) send(m *dnsmsg.Message, dst netip.AddrPort) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errClosed
	}
	return a.sendLocked(m, dst)
}

func (a *Advertisement) sendLocked(m *dnsmsg.Message, dst netip.AddrPort) error {
	data, err := m.Encode()
	if err != nil {
		return err
	}
	return a.t.WritePacket(data, dst)
}

func (a *Advertisement) serve() {
	defer close(a.done)
	for {
		data, src, err := a.t.ReadPacket()
		if err != nil {
			return
		}
		if m, dst, ok := a.respond(data, src); ok {
			if err := a.send(m, dst); err != nil {
				a.log.Warnf("answering %v: %v", dst, err)
			}
		}
	}
}

// respond returns the response to the query data from src and where to
// send it, or false when the advertisement has nothing to answer.
func (a *Advertisement) respond(data []byte, src netip.AddrPort) (*dnsmsg.Message, netip.AddrPort, bool) {
	q, err := dnsmsg.Parse(data)
	if err != nil || q.Response || q.Opcode != 0 {
		return nil, netip.AddrPort{}, false
	}
	legacy := src.Port() != Port
	unicast := legacy
	var answers []dnsmsg.Resource
	for _, qu := range q.Questions {
		matched := false
		for _, r := range a.records {
			if matches(qu, r) && !knownAnswer(q.Answers, r) && !contains(answers, r) {
				answers = append(answers, r)
				matched = true
			}
		}
		unicast = unicast || (matched && qu.Unicast)
	}
	if len(answers) == 0 {
		return nil, netip.AddrPort{}, false
	}
	var extra []dnsmsg.Resource
	for _, r := range answers {
		for _, x := range a.additionals(r) {
			if !contains(answers, x) && !contains(extra, x) {
				extra = append(extra, x)
			}
		}
	}
	m := &dnsmsg.Message{Header: responseHeader, Answers: answers, Additionals: extra}
	dst := netip.AddrPort{}
	if unicast {
		dst = src
	}
	if legacy {
		m.ID = q.ID
		m.Questions = q.Questions
		m.Answers = legacyRecords(m.Answers)
		m.Additionals = legacyRecords(m.Additionals)
	}
	return m, dst, true
}

// additionals returns the records RFC 6763, section 12, recommends adding
// to an answer r.
func (a *Advertisement) additionals(r dnsmsg.Resource) []dnsmsg.Resource {
	var target string
	var types []dnsmsg.Type
	switch d := r.Data.(type) {
	case *dnsmsg.PTR:
		target, types = d.Target, []dnsmsg.Type{dnsmsg.TypeSRV, dnsmsg.TypeTXT}
	case *dnsmsg.SRV:
		target = d.Target
	default:
		return nil
	}
	var out []dnsmsg.Resource
	for _, x := range a.records {
		if dnsmsg.EqualNames(x.Name, target) && (len(types) == 0 || containsType(types, x.Type())) {
			out = append(out, x)
			out = append(out, a.additionals(x)...)
		}
	}
	return out
}

func containsType(ts []dnsmsg.Type, t dnsmsg.Type) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func matches(q dnsmsg.Question, r dnsmsg.Resource) bool {
	return (q.Class == dnsmsg.ClassINET || q.Class == dnsmsg.ClassANY) &&
		(q.Type == r.Type() || q.Type == dnsmsg.TypeANY) &&
		dnsmsg.EqualNames(q.Name, r.Name)
}

// knownAnswer reports whether the querier listed r among its known
// answers with at least half its TTL left (RFC 6762, section 7.1).
func knownAnswer(known []dnsmsg.Resource, r dnsmsg.Resource) bool {
	for _, k := range known {
		if sameRecord(k, r) && k.TTL >= r.TTL/2 {
			return true
		}
	}
	return false
}

func contains(rs []dnsmsg.Resource, r dnsmsg.Resource) bool {
	for _, x := range rs {
		if sameRecord(x, r) {
			return true
		}
	}
	return false
}

// sameRecord reports whether two records have the same name, type, class
// and data.
func sameRecord(a, b dnsmsg.Resource) bool {
	if !dnsmsg.EqualNames(a.Name, b.Name) || a.Class != b.Class || a.Type() != b.Type() {
		return false
	}
	switch x := a.Data.(type) {
	case *dnsmsg.PTR:
		y, ok := b.Data.(*dnsmsg.PTR)
		return ok && dnsmsg.EqualNames(x.Target, y.Target)
	case *dnsmsg.SRV:
		y, ok := b.Data.(*dnsmsg.SRV)
		return ok && x.Port == y.Port && x.Priority == y.Priority && x.Weight == y.Weight &&
			dnsmsg.EqualNames(x.Target, y.Target)
	case *dnsmsg.TXT:
		y, ok := b.Data.(*dnsmsg.TXT)
		return ok && strings.Join(x.Strings, "\x00") == strings.Join(y.Strings, "\x00")
	case *dnsmsg.A:
		y, ok := b.Data.(*dnsmsg.A)
		return ok && x.Addr == y.Addr
	case *dnsmsg.AAAA:
		y, ok := b.Data.(*dnsmsg.AAAA)
		return ok && x.Addr == y.Addr
	default:
		return false
	}
}

func legacyRecords(rs []dnsmsg.Resource) []dnsmsg.Resource {
	out := make([]dnsmsg.Resource, len(rs))
	for i, r := range rs {
		r.CacheFlush = false
		r.TTL = min(r.TTL, legacyTTL)
		out[i] = r
	}
	return out
}

// completeService checks s and fills in its defaults.
func completeService(s Service, ifi *net.Interface) (Service, error) {
	if s.Instance == "" {
		return s, fmt.Errorf("%w: discovery: service without an instance name", ieee20305.ErrConfiguration)
	}
	if s.Port < 1 || s.Port > 0xffff {
		return s, fmt.Errorf("%w: discovery: service port %d out of range", ieee20305.ErrConfiguration, s.Port)
	}
	if dcap := s.TXT[KeyDcap]; !strings.HasPrefix(dcap, "/") {
		return s, fmt.Errorf("%w: dcap=%q", ErrNoDcap, dcap)
	}
	s.TXT = maps.Clone(s.TXT)
	if _, ok := s.TXT[KeyTXTVersion]; !ok {
		s.TXT[KeyTXTVersion] = "1"
	}
	if s.Host == "" {
		h, err := os.Hostname()
		if err != nil {
			return s, fmt.Errorf("%w: discovery: host name: %w", ieee20305.ErrConfiguration, err)
		}
		s.Host, _, _ = strings.Cut(h, ".")
	}
	s.Host = strings.TrimSuffix(s.Host, ".")
	if !strings.HasSuffix(strings.ToLower(s.Host), "."+Domain) {
		s.Host += "." + Domain
	}
	if len(s.Addrs) == 0 {
		addrs, err := interfaceAddrs(ifi)
		if err != nil {
			return s, err
		}
		s.Addrs = addrs
	}
	if len(s.Addrs) == 0 {
		return s, fmt.Errorf("%w: discovery: no address to advertise", ieee20305.ErrConfiguration)
	}
	subs := make([]string, 0, len(s.Subtypes))
	for _, sub := range s.Subtypes {
		subs = append(subs, subtypeLabel(sub))
	}
	s.Subtypes = subs
	s.Removed = false
	if _, err := (&dnsmsg.Message{Answers: records(s)}).Encode(); err != nil {
		return s, fmt.Errorf("%w: discovery: service cannot be advertised: %w", ieee20305.ErrConfiguration, err)
	}
	return s, nil
}

// interfaceAddrs returns the unicast addresses of ifi, or of every
// interface that is up and not a loopback when ifi is nil.
func interfaceAddrs(ifi *net.Interface) ([]netip.Addr, error) {
	ifis := []net.Interface{}
	if ifi != nil {
		ifis = append(ifis, *ifi)
	} else {
		all, err := net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("discovery: interfaces: %w", err)
		}
		for _, i := range all {
			if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
				ifis = append(ifis, i)
			}
		}
	}
	var out []netip.Addr
	for _, i := range ifis {
		as, err := i.Addrs()
		if err != nil {
			return nil, fmt.Errorf("discovery: addresses of %s: %w", i.Name, err)
		}
		for _, a := range as {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out = append(out, p.Addr().WithZone(""))
			}
		}
	}
	return out, nil
}

// records returns the records that advertise s.
func records(s Service) []dnsmsg.Resource {
	inst := instanceName(s.Instance)
	rec := func(name string, flush bool, ttl uint32, d dnsmsg.RData) dnsmsg.Resource {
		return dnsmsg.Resource{Name: name, Class: dnsmsg.ClassINET, CacheFlush: flush, TTL: ttl, Data: d}
	}
	rs := []dnsmsg.Resource{
		rec(typeName, false, otherTTL, &dnsmsg.PTR{Target: inst}),
	}
	for _, sub := range s.Subtypes {
		rs = append(rs, rec(subtypeName(sub), false, otherTTL, &dnsmsg.PTR{Target: inst}))
	}
	rs = append(rs,
		rec(enumName, false, otherTTL, &dnsmsg.PTR{Target: typeName}),
		rec(inst, true, hostTTL, &dnsmsg.SRV{Port: uint16(s.Port), Target: s.Host}),
		rec(inst, true, otherTTL, &dnsmsg.TXT{Strings: txtStrings(s.TXT)}),
	)
	for _, a := range s.Addrs {
		if a.Unmap().Is4() {
			rs = append(rs, rec(s.Host, true, hostTTL, &dnsmsg.A{Addr: a.Unmap()}))
		} else {
			rs = append(rs, rec(s.Host, true, hostTTL, &dnsmsg.AAAA{Addr: a.WithZone("")}))
		}
	}
	return rs
}
