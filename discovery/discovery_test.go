// SPDX-License-Identifier: MIT

package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

func init() {
	announceInterval = 50 * time.Millisecond
}

func meter(name string, last byte, subs ...string) Service {
	return Service{
		Instance: name,
		Host:     strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		Port:     8443,
		Addrs:    []netip.Addr{netip.AddrFrom4([4]byte{198, 51, 100, last}), netip.MustParseAddr("2001:db8::1")},
		TXT:      map[string]string{KeyDcap: "/dcap", KeyHTTPS: "8443", KeyLevel: "-S1"},
		Subtypes: subs,
	}
}

func advertise(t *testing.T, b *bus, s Service) *Advertisement {
	t.Helper()
	a, err := Advertise(context.Background(), s, AdvertiseOptions{Transport: b.join()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func browse(t *testing.T, b *bus, o BrowseOptions) (<-chan Service, *node, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n := b.join()
	o.Transport = n
	ch, err := Browse(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	return ch, n, cancel
}

// next returns the next service from ch, failing after a timeout.
func next(t *testing.T, ch <-chan Service) Service {
	t.Helper()
	select {
	case s, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no service")
		return Service{}
	}
}

// collect returns the next n services from ch by instance name.
func collect(t *testing.T, ch <-chan Service, n int) map[string]Service {
	t.Helper()
	out := map[string]Service{}
	for len(out) < n {
		s := next(t, ch)
		out[s.Instance] = s
	}
	return out
}

func TestBrowseAdvertise(t *testing.T) {
	b := newBus()
	a1 := advertise(t, b, meter("Meter 1.5", 1, "_derp", "edev"))
	advertise(t, b, meter("meter-2", 2, "_edev"))

	all, _, _ := browse(t, b, BrowseOptions{})
	got := collect(t, all, 2)
	m1 := got["Meter 1.5"]
	want := Service{
		Instance: "Meter 1.5",
		Host:     "meter-1.5.local",
		Port:     8443,
		Addrs:    []netip.Addr{netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("2001:db8::1")},
		TXT:      map[string]string{KeyDcap: "/dcap", KeyHTTPS: "8443", KeyLevel: "-S1", KeyTXTVersion: "1"},
	}
	if !reflect.DeepEqual(m1, want) {
		t.Errorf("browsed\n %+v\nwant\n %+v", m1, want)
	}
	if u, err := m1.DcapURL(); err != nil || u != "https://198.51.100.1:8443/dcap" {
		t.Errorf("DcapURL = %q, %v", u, err)
	}
	if !reflect.DeepEqual(a1.Service().Subtypes, []string{"_derp", "_edev"}) {
		t.Errorf("advertised subtypes %q", a1.Service().Subtypes)
	}

	derp, _, _ := browse(t, b, BrowseOptions{Subtypes: []string{"_derp"}})
	s := next(t, derp)
	if s.Instance != "Meter 1.5" || !reflect.DeepEqual(s.Subtypes, []string{"_derp"}) {
		t.Errorf("_derp browse found %+v", s)
	}

	edev, _, _ := browse(t, b, BrowseOptions{Subtypes: []string{"edev", "_derp"}})
	got = collect(t, edev, 2)
	if subs := got["Meter 1.5"].Subtypes; !reflect.DeepEqual(subs, []string{"_derp", "_edev"}) {
		t.Errorf("Meter 1.5 subtypes %q", subs)
	}
	if subs := got["meter-2"].Subtypes; !reflect.DeepEqual(subs, []string{"_edev"}) {
		t.Errorf("meter-2 subtypes %q", subs)
	}

	// Goodbye.
	if err := a1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a1.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
	for _, ch := range []<-chan Service{all, derp, edev} {
		s := next(t, ch)
		if s.Instance != "Meter 1.5" || !s.Removed {
			t.Errorf("after goodbye got %+v", s)
		}
	}
}

func TestBrowseCancel(t *testing.T) {
	b := newBus()
	ch, n, cancel := browse(t, b, BrowseOptions{})
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("service after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed")
	}
	select {
	case <-n.done:
	default:
		t.Error("transport not closed")
	}
}

func TestBrowseCancelWhileDelivering(t *testing.T) {
	b := newBus()
	advertise(t, b, meter("m", 1))
	ch, _, cancel := browse(t, b, BrowseOptions{})
	// Nobody reads ch; cancelling must still end the browser.
	time.Sleep(100 * time.Millisecond)
	cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed")
		}
	}
}

func TestBrowseTransportFails(t *testing.T) {
	b := newBus()
	ch, n, _ := browse(t, b, BrowseOptions{})
	_ = n.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("service from a closed transport")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed")
	}
}

func TestBrowseUnicast(t *testing.T) {
	b := newBus()
	advertise(t, b, meter("m", 1))
	ch, n, _ := browse(t, b, BrowseOptions{Unicast: true})
	if s := next(t, ch); s.Instance != "m" {
		t.Errorf("found %+v", s)
	}
	if b.unicastTo(n.addr) == 0 {
		t.Error("QU query was not answered with unicast")
	}
}

func TestAdvertiseCancel(t *testing.T) {
	b := newBus()
	obs := b.join()
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := Advertise(ctx, meter("m", 1, "_derp"), AdvertiseOptions{Transport: b.join()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		m, _ := obs.recv(t)
		if len(m.Answers) != 7 || m.Answers[0].TTL != otherTTL {
			t.Fatalf("announcement %d: %+v", i, m.Answers)
		}
	}
	cancel()
	m, _ := obs.recv(t)
	for _, r := range m.Answers {
		if r.TTL != 0 {
			t.Errorf("goodbye with TTL %d: %+v", r.TTL, r)
		}
	}
	if len(m.Answers) != 7 {
		t.Errorf("goodbye of %d records", len(m.Answers))
	}
}

// responder sends hand-made responses to a browser.
type responder struct {
	t *testing.T
	n *node
}

func (r responder) respond(rs ...dnsmsg.Resource) {
	r.n.send(r.t, &dnsmsg.Message{Header: responseHeader, Answers: rs}, netip.AddrPort{})
}

// expectQuery waits for a query asking for name and type.
func (r responder) expectQuery(name string, typ dnsmsg.Type) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, _ := r.n.recv(r.t)
		for _, q := range m.Questions {
			if !m.Response && q.Type == typ && dnsmsg.EqualNames(q.Name, name) {
				return
			}
		}
	}
	r.t.Fatalf("no query for %s %v", name, typ)
}

func rec(name string, ttl uint32, flush bool, d dnsmsg.RData) dnsmsg.Resource {
	return dnsmsg.Resource{Name: name, Class: dnsmsg.ClassINET, TTL: ttl, CacheFlush: flush, Data: d}
}

func TestBrowseResolves(t *testing.T) {
	b := newBus()
	r := responder{t, b.join()}
	ifi := &net.Interface{Name: "en9"}
	ch, _, _ := browse(t, b, BrowseOptions{Interface: ifi})
	inst := instanceName("x")
	r.respond(rec(typeName, 4500, false, &dnsmsg.PTR{Target: inst}))
	r.expectQuery(inst, dnsmsg.TypeSRV)
	r.respond(
		rec(inst, 120, true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec(inst, 4500, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}),
	)
	r.expectQuery("x.local", dnsmsg.TypeA)
	r.respond(
		rec("x.local", 120, true, &dnsmsg.AAAA{Addr: netip.MustParseAddr("fe80::5")}),
		rec("x.local", 120, true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.200")}),
	)
	s := next(t, ch)
	want := []netip.Addr{netip.MustParseAddr("192.0.2.200"), netip.MustParseAddr("fe80::5%en9")}
	if !reflect.DeepEqual(s.Addrs, want) || s.Host != "x.local" || s.Port != 80 {
		t.Errorf("resolved %+v", s)
	}
	if u, err := s.DcapURL(); err != nil || u != "http://192.0.2.200:80/d" {
		t.Errorf("DcapURL = %q, %v", u, err)
	}

	// A cache-flush record replaces the addresses; the change is delivered.
	r.respond(rec("x.local", 120, true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.201")}))
	s = next(t, ch)
	if !reflect.DeepEqual(s.Addrs, []netip.Addr{netip.MustParseAddr("192.0.2.201")}) {
		t.Errorf("after cache flush %v", s.Addrs)
	}
	// Removing the last address makes the instance incomplete.
	r.respond(rec("x.local", 0, false, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.201")}))
	if s = next(t, ch); !s.Removed {
		t.Errorf("without addresses %+v", s)
	}
	// A shared address record adds to the cache.
	r.respond(rec("x.local", 120, false, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.202")}))
	if s = next(t, ch); s.Removed || len(s.Addrs) != 1 {
		t.Errorf("after new address %+v", s)
	}
	// Goodbyes of SRV and TXT.
	r.respond(rec(inst, 0, false, &dnsmsg.SRV{Port: 80, Target: "x.local"}), rec(inst, 0, false, &dnsmsg.TXT{}))
	if s = next(t, ch); !s.Removed {
		t.Errorf("after SRV goodbye %+v", s)
	}
}

func TestBrowseIgnores(t *testing.T) {
	b := newBus()
	r := responder{t, b.join()}
	ch, _, _ := browse(t, b, BrowseOptions{Subtypes: []string{"_derp"}})
	inst := instanceName("x")
	full := []dnsmsg.Resource{
		rec(inst, 120, true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec("x.local", 120, true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.200")}),
	}
	// From another port than 5353.
	other := responder{t, b.joinAt(netip.MustParseAddrPort("192.0.2.99:1234"))}
	other.respond(append(full, rec(subtypeName("_derp"), 120, false, &dnsmsg.PTR{Target: inst}),
		rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}))...)
	// Malformed, a query, a PTR of a type not browsed for, a PTR to a
	// name that is not an instance, a record of another class, and a TXT
	// record without dcap.
	_ = r.n.WritePacket([]byte{1, 2, 3}, netip.AddrPort{})
	r.n.send(t, &dnsmsg.Message{Answers: full}, netip.AddrPort{})
	r.respond(append(full,
		rec(typeName, 120, false, &dnsmsg.PTR{Target: inst}),
		rec(subtypeName("_derp"), 120, false, &dnsmsg.PTR{Target: "x.local"}),
		dnsmsg.Resource{Name: subtypeName("_derp"), Class: 3, TTL: 120, Data: &dnsmsg.PTR{Target: inst}},
	)...)
	r.respond(rec(subtypeName("_derp"), 120, false, &dnsmsg.PTR{Target: inst}),
		rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap="}}))
	// A PTR goodbye for an instance never seen.
	r.respond(rec(subtypeName("_derp"), 0, false, &dnsmsg.PTR{Target: instanceName("y")}))
	select {
	case s := <-ch:
		t.Fatalf("found %+v", s)
	case <-time.After(300 * time.Millisecond):
	}
	// The TXT record gets its dcap and the service appears; a PTR
	// goodbye removes it.
	r.respond(rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}))
	if s := next(t, ch); s.Instance != "x" || s.Removed {
		t.Errorf("found %+v", s)
	}
	r.respond(rec(subtypeName("_derp"), 0, false, &dnsmsg.PTR{Target: inst}))
	if s := next(t, ch); !s.Removed {
		t.Errorf("after goodbye %+v", s)
	}
}

func TestBrowseExpiry(t *testing.T) {
	defer func(s, q time.Duration) { sweepInterval, firstRequery = s, q }(sweepInterval, firstRequery)
	sweepInterval, firstRequery = 20*time.Millisecond, 20*time.Millisecond
	b := newBus()
	r := responder{t, b.join()}
	ch, _, _ := browse(t, b, BrowseOptions{})
	inst := instanceName("x")
	r.respond(
		rec(typeName, 1, false, &dnsmsg.PTR{Target: inst}),
		rec(inst, 1, true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec(inst, 1, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}),
		rec("x.local", 1, true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.200")}),
	)
	if s := next(t, ch); s.Removed {
		t.Fatalf("found %+v", s)
	}
	start := time.Now()
	if s := next(t, ch); !s.Removed {
		t.Errorf("after expiry %+v", s)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("expired after %v", d)
	}
}

func TestBrowseErrors(t *testing.T) {
	if _, err := Browse(context.Background(), BrowseOptions{Transport: failing{}}); err == nil {
		t.Error("Browse with a failing transport")
	}
	b := newBus()
	if _, err := Browse(context.Background(), BrowseOptions{Transport: b.join(), Subtypes: []string{strings.Repeat("x", 70)}}); err == nil {
		t.Error("Browse with a subtype too long")
	}
}

type failing struct{}

func (failing) ReadPacket() ([]byte, netip.AddrPort, error) {
	return nil, netip.AddrPort{}, net.ErrClosed
}
func (failing) WritePacket([]byte, netip.AddrPort) error { return errors.New("no network") }
func (failing) Close() error                             { return nil }

func TestAdvertiseErrors(t *testing.T) {
	ok := meter("m", 1)
	for name, mod := range map[string]func(*Service){
		"no instance":   func(s *Service) { s.Instance = "" },
		"no port":       func(s *Service) { s.Port = 0 },
		"big port":      func(s *Service) { s.Port = 70000 },
		"no dcap":       func(s *Service) { s.TXT = nil },
		"relative dcap": func(s *Service) { s.TXT = map[string]string{KeyDcap: "dcap"} },
		"long instance": func(s *Service) { s.Instance = strings.Repeat("x", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			s := ok
			mod(&s)
			if _, err := Advertise(context.Background(), s, AdvertiseOptions{Transport: newBus().join()}); err == nil {
				t.Error("Advertise succeeded")
			}
		})
	}
	if _, err := Advertise(context.Background(), ok, AdvertiseOptions{Transport: failing{}}); err == nil {
		t.Error("Advertise with a failing transport")
	}
}

func TestAdvertiseDefaults(t *testing.T) {
	s := meter("m", 1)
	s.Host, s.Addrs = "", nil
	s.TXT = map[string]string{KeyDcap: "/dcap", KeyTXTVersion: "2"}
	c, err := completeService(s, nil)
	if err != nil {
		t.Skipf("no interface addresses: %v", err)
	}
	if !strings.HasSuffix(c.Host, ".local") || strings.Count(c.Host, ".") != 1 {
		t.Errorf("default host %q", c.Host)
	}
	if c.TXT[KeyTXTVersion] != "2" || s.TXT[KeyTXTVersion] != "2" {
		t.Errorf("TXT %v", c.TXT)
	}
	ifis, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, ifi := range ifis {
		if as, err := interfaceAddrs(&ifi); err != nil || len(as) == 0 {
			continue
		}
		if c, err = completeService(s, &ifi); err != nil || len(c.Addrs) == 0 {
			t.Errorf("addresses of %s: %v, %v", ifi.Name, c.Addrs, err)
		}
		break
	}
	if _, err := completeService(s, &net.Interface{Index: 1 << 20, Name: "none"}); err == nil {
		t.Error("addresses of a missing interface")
	}
}

func TestRespond(t *testing.T) {
	b := newBus()
	a := advertise(t, b, meter("m", 1, "_derp"))
	src := netip.MustParseAddrPort("192.0.2.50:5353")
	query := func(m *dnsmsg.Message, src netip.AddrPort) (*dnsmsg.Message, netip.AddrPort, bool) {
		t.Helper()
		data, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return a.respond(data, src)
	}
	q := func(name string, typ dnsmsg.Type) dnsmsg.Question {
		return dnsmsg.Question{Name: name, Type: typ, Class: dnsmsg.ClassINET}
	}
	inst := instanceName("m")

	// PTR: answer plus SRV, TXT and addresses.
	r, dst, ok := query(&dnsmsg.Message{Questions: []dnsmsg.Question{q(subtypeName("derp"), dnsmsg.TypePTR)}}, src)
	if !ok || dst.IsValid() || len(r.Answers) != 1 || len(r.Additionals) != 4 {
		t.Errorf("PTR: %v %v %+v", ok, dst, r)
	}
	// ANY of the instance in upper case: SRV and TXT, with addresses.
	r, _, _ = query(&dnsmsg.Message{Questions: []dnsmsg.Question{q(strings.ToUpper(inst), dnsmsg.TypeANY)}}, src)
	if len(r.Answers) != 2 || len(r.Additionals) != 2 {
		t.Errorf("ANY: %+v", r)
	}
	// Service type enumeration.
	r, _, _ = query(&dnsmsg.Message{Questions: []dnsmsg.Question{q(enumName, dnsmsg.TypePTR)}}, src)
	if len(r.Answers) != 1 || r.Answers[0].Data.(*dnsmsg.PTR).Target != typeName {
		t.Errorf("enumeration: %+v", r)
	}
	// Known-answer suppression.
	known := rec(typeName, otherTTL, false, &dnsmsg.PTR{Target: inst})
	if _, _, ok := query(&dnsmsg.Message{Questions: []dnsmsg.Question{q(typeName, dnsmsg.TypePTR)},
		Answers: []dnsmsg.Resource{known}}, src); ok {
		t.Error("answered a known answer")
	}
	known.TTL = 10
	if _, _, ok := query(&dnsmsg.Message{Questions: []dnsmsg.Question{q(typeName, dnsmsg.TypePTR)},
		Answers: []dnsmsg.Resource{known}}, src); !ok {
		t.Error("known answer with little TTL left suppressed the answer")
	}
	// QU bit: unicast to the querier.
	qu := q(typeName, dnsmsg.TypePTR)
	qu.Unicast = true
	if _, dst, _ := query(&dnsmsg.Message{Questions: []dnsmsg.Question{qu}}, src); dst != src {
		t.Errorf("QU answered to %v", dst)
	}
	// Legacy unicast: ID, questions, short TTLs, no cache flush.
	legacy := netip.MustParseAddrPort("192.0.2.50:40000")
	r, dst, _ = query(&dnsmsg.Message{Header: dnsmsg.Header{ID: 42},
		Questions: []dnsmsg.Question{q(inst, dnsmsg.TypeSRV)}}, legacy)
	if dst != legacy || r.ID != 42 || len(r.Questions) != 1 {
		t.Errorf("legacy: %v %+v", dst, r)
	}
	for _, x := range append(r.Answers, r.Additionals...) {
		if x.TTL > legacyTTL || x.CacheFlush {
			t.Errorf("legacy record %+v", x)
		}
	}
	// Not answered: responses, other opcodes, garbage, unknown names,
	// other classes.
	for name, m := range map[string]*dnsmsg.Message{
		"response": {Header: dnsmsg.Header{Response: true}, Questions: []dnsmsg.Question{q(typeName, dnsmsg.TypePTR)}},
		"opcode":   {Header: dnsmsg.Header{Opcode: 4}, Questions: []dnsmsg.Question{q(typeName, dnsmsg.TypePTR)}},
		"name":     {Questions: []dnsmsg.Question{q("_http._tcp.local", dnsmsg.TypePTR)}},
		"class":    {Questions: []dnsmsg.Question{{Name: typeName, Type: dnsmsg.TypePTR, Class: 3}}},
	} {
		if _, _, ok := query(m, src); ok {
			t.Errorf("answered %s", name)
		}
	}
	if _, _, ok := a.respond([]byte{1}, src); ok {
		t.Error("answered garbage")
	}
}

func TestSameRecord(t *testing.T) {
	addr4 := netip.MustParseAddr("192.0.2.1")
	addr6 := netip.MustParseAddr("2001:db8::1")
	rs := []dnsmsg.Resource{
		rec("a", 1, false, &dnsmsg.PTR{Target: "x"}),
		rec("a", 1, false, &dnsmsg.SRV{Port: 1, Target: "x"}),
		rec("a", 1, false, &dnsmsg.TXT{Strings: []string{"a"}}),
		rec("a", 1, false, &dnsmsg.A{Addr: addr4}),
		rec("a", 1, false, &dnsmsg.AAAA{Addr: addr6}),
		rec("a", 1, false, &dnsmsg.Unknown{T: 99}),
	}
	for i, x := range rs {
		for j, y := range rs {
			want := i == j && i != len(rs)-1
			if got := sameRecord(x, y); got != want {
				t.Errorf("sameRecord(%d, %d) = %v", i, j, got)
			}
		}
	}
	if sameRecord(rs[0], rec("b", 1, false, &dnsmsg.PTR{Target: "x"})) {
		t.Error("different names")
	}
	if sameRecord(rs[0], rec("a", 1, false, &dnsmsg.Unknown{T: dnsmsg.TypePTR})) ||
		sameRecord(rs[1], rec("a", 1, false, &dnsmsg.Unknown{T: dnsmsg.TypeSRV})) {
		t.Error("PTR or SRV equal to Unknown")
	}
}

func TestDcapURL(t *testing.T) {
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("fe80::1%en0")
	for _, tc := range []struct {
		name string
		s    Service
		want string
		err  bool
	}{
		{"https", Service{Host: "m.local.", Port: 80, TXT: map[string]string{"dcap": "/dcap", "https": "443"}},
			"https://m.local:443/dcap", false},
		{"http", Service{Host: "m.local", Port: 8080, TXT: map[string]string{"dcap": "/sep2/dcap?x=1"}},
			"http://m.local:8080/sep2/dcap?x=1", false},
		{"IPv4 preferred", Service{Host: "m.local", Port: 80, Addrs: []netip.Addr{v6, v4}, TXT: map[string]string{"dcap": "/d"}},
			"http://192.0.2.1:80/d", false},
		{"IPv6 zone", Service{Port: 80, Addrs: []netip.Addr{v6}, TXT: map[string]string{"dcap": "/d"}},
			"http://[fe80::1%25en0]:80/d", false},
		{"no dcap", Service{Host: "m", Port: 80}, "", true},
		{"relative dcap", Service{Host: "m", Port: 80, TXT: map[string]string{"dcap": "dcap"}}, "", true},
		{"network-path dcap", Service{Host: "m", Port: 80, TXT: map[string]string{"dcap": "//evil/dcap"}}, "", true},
		{"bad dcap", Service{Host: "m", Port: 80, TXT: map[string]string{"dcap": "/%zz"}}, "", true},
		{"bad https", Service{Host: "m", Port: 80, TXT: map[string]string{"dcap": "/d", "https": "x"}}, "", true},
		{"https range", Service{Host: "m", Port: 80, TXT: map[string]string{"dcap": "/d", "https": "0"}}, "", true},
		{"port range", Service{Host: "m", TXT: map[string]string{"dcap": "/d"}}, "", true},
		{"no host", Service{Port: 80, TXT: map[string]string{"dcap": "/d"}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.s.DcapURL()
			if got != tc.want || (err != nil) != tc.err {
				t.Errorf("DcapURL = %q, %v; want %q", got, err, tc.want)
			}
			if tc.name == "no dcap" && !errors.Is(err, ErrNoDcap) {
				t.Errorf("error %v is not ErrNoDcap", err)
			}
		})
	}
}

func TestTXT(t *testing.T) {
	got := parseTXT([]string{"txtvers=1", "DCAP=/dcap", "dcap=/other", "=x", "", "flag", "path=/a=b"})
	want := map[string]string{"txtvers": "1", "dcap": "/dcap", "flag": "", "path": "/a=b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTXT = %v", got)
	}
	strs := txtStrings(map[string]string{"https": "443", "dcap": "/d"})
	if !reflect.DeepEqual(strs, []string{"txtvers=1", "dcap=/d", "https=443"}) {
		t.Errorf("txtStrings = %q", strs)
	}
}

func TestInstanceOf(t *testing.T) {
	for name, want := range map[string]string{
		instanceName("a.b"):            "a.b",
		"X._SmartEnergy._TCP.local.":   "X",
		"x._http._tcp.local":           "",
		"_smartenergy._tcp.local":      "",
		`x\`:                           "",
		"x.y._smartenergy._tcp.local":  "",
		"x._smartenergy._tcp.local.ex": "",
	} {
		got, ok := instanceOf(name)
		if got != want || ok != (want != "") {
			t.Errorf("instanceOf(%q) = %q, %v", name, got, ok)
		}
	}
}

// needMulticast skips a test that needs multicast, or fails it when
// IEEE20305_REQUIRE_MULTICAST is set: the CI job that measures coverage sets it.
func needMulticast(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("IEEE20305_REQUIRE_MULTICAST") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format+" (IEEE20305_REQUIRE_MULTICAST=1 makes this a failure)", args...)
}

// TestMulticast browses for a service advertised on this host over real
// multicast sockets. It is skipped where multicast is not available.
func TestMulticast(t *testing.T) {
	if testing.Short() {
		t.Skip("multicast in -short mode")
	}
	ta, err := NewMulticastTransport(nil)
	if err != nil {
		needMulticast(t, "multicast not available: %v", err)
	}
	s := meter("go-ieee20305-test-"+time.Now().Format("150405.000000000"), 1, "_derp")
	s.Addrs = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	a, err := Advertise(context.Background(), s, AdvertiseOptions{Transport: ta})
	if err != nil {
		_ = ta.Close()
		needMulticast(t, "multicast not available: %v", err)
	}
	defer func() { _ = a.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := Browse(ctx, BrowseOptions{Subtypes: []string{"_derp"}})
	if err != nil {
		needMulticast(t, "multicast not available: %v", err)
	}
	for found := range ch {
		if found.Instance == s.Instance {
			if found.Port != s.Port || found.TXT[KeyDcap] != "/dcap" {
				t.Errorf("found %+v", found)
			}
			return
		}
	}
	needMulticast(t, "multicast packets are not delivered on this host")
}
