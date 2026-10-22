// SPDX-License-Identifier: MIT

package discovery

import (
	"context"
	"maps"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// Intervals of the browser, variables so that tests can shorten them.
var (
	// firstRequery is the interval between the first and second query;
	// it doubles after each query up to maxRequery (RFC 6762, section 5.2).
	firstRequery = time.Second
	maxRequery   = time.Hour
	// sweepInterval is how often expired records are removed.
	sweepInterval = time.Second
	// resolveInterval is the shortest interval between two queries for
	// the missing records of one instance.
	resolveInterval = time.Second
)

// BrowseOptions configures Browse.
type BrowseOptions struct {
	// Subtypes restricts browsing to servers announced under these
	// function-set subtypes, such as "_derp" or "_edev" (the leading
	// underscore is optional). Without subtypes Browse finds every
	// IEEE 2030.5 server.
	Subtypes []string
	// Interface is the network interface to browse on when Transport is
	// nil, and the zone of the IPv6 link-local addresses found. Nil means
	// the system's default multicast interface.
	Interface *net.Interface
	// Transport sends and receives the packets. Nil means a transport
	// from NewMulticastTransport(Interface). Browse closes it when it
	// ends.
	Transport PacketTransport
	// Unicast sets the unicast-response (QU) bit on the first query, so
	// that responders answer it directly instead of over multicast
	// (RFC 6762, section 5.4).
	Unicast bool
	// Logger receives the browser's log (see ieee20305.Logger), such as
	// queries that cannot be sent; none when nil.
	Logger ieee20305.Logger
}

// Browse finds IEEE 2030.5 servers with multicast DNS: it queries for the
// PTR records of _smartenergy._tcp.local, or of
// <subtype>._sub._smartenergy._tcp.local for each subtype, at growing
// intervals, resolves the SRV, TXT, A and AAAA records of each instance
// found, and delivers a Service when an instance is complete or changes.
// When an instance is withdrawn with a goodbye or its records expire,
// Browse delivers it again with Removed set. Instances whose TXT record
// has no dcap key are ignored, as the standard requires.
//
// Browsing goes on until ctx is done; then the channel is closed.
func Browse(ctx context.Context, o BrowseOptions) (<-chan Service, error) {
	t := o.Transport
	if t == nil {
		var err error
		if t, err = NewMulticastTransport(o.Interface); err != nil {
			return nil, err
		}
	}
	b := &browser{
		t:        t,
		ifi:      o.Interface,
		log:      ieee20305.ComponentLogger(o.Logger, "discovery"),
		queries:  map[string]string{},
		ptr:      map[string]map[string]time.Time{},
		srv:      map[string]srvEntry{},
		txt:      map[string]txtEntry{},
		addrs:    map[string]map[netip.Addr]time.Time{},
		names:    map[string]string{},
		emitted:  map[string]Service{},
		resolved: map[dnsmsg.Question]time.Time{},
	}
	if len(o.Subtypes) == 0 {
		b.queries[strings.ToLower(typeName)] = ""
		b.order = append(b.order, typeName)
	}
	for _, s := range o.Subtypes {
		n := subtypeName(s)
		b.queries[strings.ToLower(n)] = subtypeLabel(s)
		b.order = append(b.order, n)
	}
	if err := b.query(o.Unicast); err != nil {
		_ = t.Close()
		return nil, err
	}
	out := make(chan Service)
	go b.run(ctx, out)
	return out, nil
}

type srvEntry struct {
	target string
	port   uint16
	expiry time.Time
}

type txtEntry struct {
	txt    map[string]string
	expiry time.Time
}

type browser struct {
	t   PacketTransport
	ifi *net.Interface
	log ieee20305.Logger
	// queries maps the lower-case names browsed for to their subtype
	// label, "" for the service type; order lists them as given.
	queries map[string]string
	order   []string
	// The cache, keyed by lower-case names: ptr maps an instance to the
	// browsed names that point to it, addrs a host to its addresses, each
	// with the expiry of the record.
	ptr   map[string]map[string]time.Time
	names map[string]string // instance key to the name as received
	srv   map[string]srvEntry
	txt   map[string]txtEntry
	addrs map[string]map[netip.Addr]time.Time
	// emitted holds the last Service delivered for each instance;
	// resolved the time of the last query for each missing record.
	emitted  map[string]Service
	resolved map[dnsmsg.Question]time.Time
}

func (b *browser) send(qs []dnsmsg.Question) error {
	data, err := (&dnsmsg.Message{Questions: qs}).Encode()
	if err != nil {
		return err
	}
	return b.t.WritePacket(data, netip.AddrPort{})
}

func (b *browser) query(unicast bool) error {
	qs := make([]dnsmsg.Question, 0, len(b.order))
	for _, n := range b.order {
		qs = append(qs, dnsmsg.Question{Name: n, Type: dnsmsg.TypePTR, Class: dnsmsg.ClassINET, Unicast: unicast})
	}
	return b.send(qs)
}

func (b *browser) run(ctx context.Context, out chan<- Service) {
	in := make(chan packet)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			data, src, err := b.t.ReadPacket()
			if err != nil {
				close(in)
				return
			}
			select {
			case in <- packet{data, src}:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() {
		_ = b.t.Close()
		<-readerDone
		close(out)
	}()

	interval := firstRequery
	requery := time.NewTimer(interval)
	defer requery.Stop()
	sweep := time.NewTicker(sweepInterval)
	defer sweep.Stop()
	for {
		var pending []Service
		select {
		case <-ctx.Done():
			return
		case p, ok := <-in:
			if !ok {
				return
			}
			pending = b.handle(p, time.Now())
		case <-requery.C:
			if err := b.query(false); err != nil {
				b.log.Warnf("sending a query: %v", err)
			}
			interval = min(2*interval, maxRequery)
			requery.Reset(interval)
		case now := <-sweep.C:
			b.expire(now)
			pending = b.reconcile(now)
		}
		for _, s := range pending {
			select {
			case out <- s:
			case <-ctx.Done():
				return
			}
		}
	}
}

// handle caches the records of a response and returns the services to
// deliver.
func (b *browser) handle(p packet, now time.Time) []Service {
	if p.src.Port() != Port {
		// RFC 6762, section 6: responses must come from port 5353.
		return nil
	}
	m, err := dnsmsg.Parse(p.data)
	if err != nil || !m.Response || m.Opcode != 0 || m.RCode != 0 {
		return nil
	}
	flushed := map[string]bool{}
	for _, r := range append(m.Answers, m.Additionals...) {
		if r.Class != dnsmsg.ClassINET {
			continue
		}
		key := strings.ToLower(r.Name)
		expiry := now.Add(time.Duration(r.TTL) * time.Second)
		switch d := r.Data.(type) {
		case *dnsmsg.PTR:
			if _, ok := b.queries[key]; !ok {
				continue
			}
			if _, ok := instanceOf(d.Target); !ok {
				continue
			}
			inst := strings.ToLower(d.Target)
			if r.TTL == 0 {
				delete(b.ptr[inst], key)
				continue
			}
			if b.ptr[inst] == nil {
				b.ptr[inst] = map[string]time.Time{}
			}
			b.names[inst] = d.Target
			b.ptr[inst][key] = expiry
		case *dnsmsg.SRV:
			if r.TTL == 0 {
				delete(b.srv, key)
				continue
			}
			b.srv[key] = srvEntry{target: d.Target, port: d.Port, expiry: expiry}
		case *dnsmsg.TXT:
			if r.TTL == 0 {
				delete(b.txt, key)
				continue
			}
			b.txt[key] = txtEntry{txt: parseTXT(d.Strings), expiry: expiry}
		case *dnsmsg.A:
			b.addAddr(key, d.Addr, r, expiry, flushed)
		case *dnsmsg.AAAA:
			b.addAddr(key, d.Addr, r, expiry, flushed)
		}
	}
	return b.reconcile(now)
}

// addAddr caches an address of host. The first cache-flush record of a
// host in a packet replaces the addresses cached before.
func (b *browser) addAddr(host string, a netip.Addr, r dnsmsg.Resource, expiry time.Time, flushed map[string]bool) {
	if r.CacheFlush && !flushed[host] {
		flushed[host] = true
		delete(b.addrs, host)
	}
	if r.TTL == 0 {
		delete(b.addrs[host], a)
		return
	}
	if b.addrs[host] == nil {
		b.addrs[host] = map[netip.Addr]time.Time{}
	}
	b.addrs[host][a] = expiry
}

// expire removes the records that expired at now.
func (b *browser) expire(now time.Time) {
	for inst, names := range b.ptr {
		maps.DeleteFunc(names, func(_ string, e time.Time) bool { return !now.Before(e) })
		if len(names) == 0 {
			delete(b.ptr, inst)
		}
	}
	maps.DeleteFunc(b.names, func(inst string, _ string) bool { return len(b.ptr[inst]) == 0 })
	maps.DeleteFunc(b.srv, func(_ string, e srvEntry) bool { return !now.Before(e.expiry) })
	maps.DeleteFunc(b.txt, func(_ string, e txtEntry) bool { return !now.Before(e.expiry) })
	for host, as := range b.addrs {
		maps.DeleteFunc(as, func(_ netip.Addr, e time.Time) bool { return !now.Before(e) })
		if len(as) == 0 {
			delete(b.addrs, host)
		}
	}
}

// reconcile returns the services that became complete, changed or went
// away, and queries for the missing records of incomplete instances.
func (b *browser) reconcile(now time.Time) []Service {
	insts := map[string]bool{}
	for inst := range b.ptr {
		insts[inst] = true
	}
	for inst := range b.emitted {
		insts[inst] = true
	}
	keys := slices.Sorted(maps.Keys(insts))
	var out []Service
	var missing []dnsmsg.Question
	for _, inst := range keys {
		s, complete, need := b.service(inst)
		last, wasEmitted := b.emitted[inst]
		switch {
		case complete:
			if !wasEmitted || !equalServices(last, s) {
				b.emitted[inst] = s
				out = append(out, s)
			}
		case wasEmitted:
			delete(b.emitted, inst)
			last.Removed = true
			out = append(out, last)
		}
		for _, q := range need {
			key := q
			key.Name = strings.ToLower(q.Name)
			if now.Sub(b.resolved[key]) >= resolveInterval {
				b.resolved[key] = now
				missing = append(missing, q)
			}
		}
	}
	maps.DeleteFunc(b.resolved, func(_ dnsmsg.Question, t time.Time) bool { return now.Sub(t) >= resolveInterval })
	if len(missing) > 0 {
		if err := b.send(missing); err != nil {
			b.log.Warnf("sending a query: %v", err)
		}
	}
	return out
}

// service builds the Service of an instance from the cache and reports
// whether it is complete, or which questions would complete it.
func (b *browser) service(inst string) (Service, bool, []dnsmsg.Question) {
	names := b.ptr[inst]
	if len(names) == 0 {
		return Service{}, false, nil
	}
	var s Service
	var full string
	for n := range names {
		if sub := b.queries[n]; sub != "" {
			s.Subtypes = append(s.Subtypes, sub)
		}
	}
	sort.Strings(s.Subtypes)
	srv, hasSRV := b.srv[inst]
	txt, hasTXT := b.txt[inst]
	if hasSRV {
		full = srv.target
	}
	var need []dnsmsg.Question
	q := func(name string, t dnsmsg.Type) {
		need = append(need, dnsmsg.Question{Name: name, Type: t, Class: dnsmsg.ClassINET})
	}
	if !hasSRV {
		q(b.names[inst], dnsmsg.TypeSRV)
	}
	if !hasTXT {
		q(b.names[inst], dnsmsg.TypeTXT)
	}
	if !hasSRV || !hasTXT {
		return s, false, need
	}
	s.Instance, _ = instanceOf(b.names[inst])
	s.Host = strings.TrimSuffix(full, ".")
	s.Port = int(srv.port)
	s.TXT = txt.txt
	for a := range b.addrs[strings.ToLower(full)] {
		if b.ifi != nil && a.Is6() && a.IsLinkLocalUnicast() {
			a = a.WithZone(b.ifi.Name)
		}
		s.Addrs = append(s.Addrs, a)
	}
	slices.SortFunc(s.Addrs, netip.Addr.Compare)
	if len(s.Addrs) == 0 {
		q(full, dnsmsg.TypeA)
		q(full, dnsmsg.TypeAAAA)
		return s, false, need
	}
	return s, s.TXT[KeyDcap] != "", nil
}

// instanceOf returns the instance label of name when name is an instance
// of the IEEE 2030.5 service type.
func instanceOf(name string) (string, bool) {
	labels, err := dnsmsg.SplitName(name)
	if err != nil || len(labels) != 4 {
		return "", false
	}
	if !dnsmsg.EqualNames(strings.Join(labels[1:], "."), typeName) {
		return "", false
	}
	return labels[0], true
}

func equalServices(a, b Service) bool {
	return a.Instance == b.Instance && a.Host == b.Host && a.Port == b.Port &&
		a.Removed == b.Removed &&
		slices.Equal(a.Addrs, b.Addrs) && slices.Equal(a.Subtypes, b.Subtypes) &&
		maps.Equal(a.TXT, b.TXT)
}
