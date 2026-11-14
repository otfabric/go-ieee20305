// SPDX-License-Identifier: MIT

package discovery

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// capture is a PacketTransport that records what is written.
type capture struct {
	mu      sync.Mutex
	packets [][]byte
}

func (*capture) ReadPacket() ([]byte, netip.AddrPort, error) { return nil, netip.AddrPort{}, errClosed }
func (*capture) Close() error                                { return nil }
func (c *capture) WritePacket(data []byte, _ netip.AddrPort) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.packets = append(c.packets, append([]byte{}, data...))
	return nil
}

func (c *capture) take() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.packets
	c.packets = nil
	return p
}

// A query that repeats one question and lists many known answers is
// answered with work linear in its size.
func TestRespondManyKnownAnswers(t *testing.T) {
	a := &Advertisement{records: records(meter("m", 1))}
	inst := instanceName("m")
	query := func(questions, known int) []byte {
		m := &dnsmsg.Message{}
		for range questions {
			m.Questions = append(m.Questions, dnsmsg.Question{Name: inst, Type: dnsmsg.TypeANY, Class: dnsmsg.ClassINET})
		}
		for i := range known {
			m.Answers = append(m.Answers, rec(inst, otherTTL, false, &dnsmsg.TXT{Strings: []string{fmt.Sprint(i)}}))
		}
		b, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	src := netip.MustParseAddrPort("192.0.2.50:5353")
	b := query(400, 400)
	if len(b) > maxPacket {
		t.Fatalf("query of %d bytes", len(b))
	}
	m, _, ok := a.respond(b, src)
	if !ok || len(m.Answers) != 2 {
		t.Fatalf("answer %v %+v", ok, m)
	}
	allocs := testing.AllocsPerRun(5, func() { a.respond(b, src) })
	if allocs > 10*800 {
		t.Errorf("%v allocations for 400 questions and 400 known answers", allocs)
	}
	// The same question in another case is answered once.
	m, _, _ = a.respond(func() []byte {
		b, _ := (&dnsmsg.Message{Questions: []dnsmsg.Question{
			{Name: inst, Type: dnsmsg.TypeSRV, Class: dnsmsg.ClassINET},
			{Name: strings.ToUpper(inst), Type: dnsmsg.TypeSRV, Class: dnsmsg.ClassINET},
		}}).Encode()
		return b
	}(), src)
	if len(m.Answers) != 1 {
		t.Errorf("%d answers to a repeated question", len(m.Answers))
	}
}

// Instances whose SRV and TXT records never arrive are queried for at
// doubling intervals, in messages of at most maxPacket bytes.
func TestBrowseResolveBackoff(t *testing.T) {
	b := testBrowser()
	c := &capture{}
	b.t = c
	now := time.Unix(1e9, 0)
	label := strings.Repeat("i", 59)
	for i := range 2 {
		var rs []dnsmsg.Resource
		for j := range 64 {
			rs = append(rs, rec(typeName, ^uint32(0), false, &dnsmsg.PTR{Target: instanceName(fmt.Sprintf("%s%04d", label, i*64+j))}))
		}
		b.handle(packetOf(t, rs...), now)
	}
	sends, split := 0, false
	for s := range 3600 {
		at := now.Add(time.Duration(s) * time.Second)
		b.expire(at)
		b.reconcile(at)
		ps := c.take()
		if len(ps) > 0 {
			sends++
		}
		split = split || len(ps) > 1
		for _, p := range ps {
			if len(p) > maxPacket {
				t.Fatalf("query of %d bytes", len(p))
			}
			m, err := dnsmsg.Parse(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Questions) == 0 {
				t.Fatal("query without questions")
			}
		}
	}
	// Queries at 0, 1, 3, 7, ... seconds: 12 within the hour, plus the
	// ones sent while the instances arrived.
	if sends > 13 || !split {
		t.Errorf("queries sent in %d of 3600 seconds, split %v", sends, split)
	}
	// Once its records arrive, an instance is no longer queried for, and
	// when they go again it is queried for at once.
	inst := instanceName(label + "0000")
	b.handle(packetOf(t,
		rec(inst, 120, true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}),
	), now.Add(time.Hour))
	if _, ok := b.resolved[dnsmsg.Question{Name: strings.ToLower(inst), Type: dnsmsg.TypeSRV, Class: dnsmsg.ClassINET}]; ok {
		t.Error("resolved record still queried for")
	}
	c.take()
	b.handle(packetOf(t, rec(inst, 0, true, &dnsmsg.SRV{Port: 80, Target: "x.local"})), now.Add(time.Hour+time.Second))
	found := false
	for _, p := range c.take() {
		m, _ := dnsmsg.Parse(p)
		for _, q := range m.Questions {
			found = found || (dnsmsg.EqualNames(q.Name, inst) && q.Type == dnsmsg.TypeSRV)
		}
	}
	if !found {
		t.Error("record that went missing again not queried for")
	}
}

// The browser sends what it has to say even when the transport fails.
func TestBrowseSendError(t *testing.T) {
	b := testBrowser()
	if err := b.send([]dnsmsg.Question{{Name: typeName, Type: dnsmsg.TypePTR, Class: dnsmsg.ClassINET}}); err == nil {
		t.Error("no error from a failing transport")
	}
	if err := b.send([]dnsmsg.Question{{Name: "a..b", Type: dnsmsg.TypePTR, Class: dnsmsg.ClassINET}}); err == nil {
		t.Error("no error for a name that cannot be encoded")
	}
}

// Cached records live at most maxCacheTTL seconds, whatever TTL the peer
// gave them.
func TestBrowseTTLClamp(t *testing.T) {
	b := testBrowser()
	now := time.Unix(1e9, 0)
	inst := instanceName("m")
	b.handle(packetOf(t,
		rec(typeName, ^uint32(0), false, &dnsmsg.PTR{Target: inst}),
		rec(inst, ^uint32(0), true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec(inst, ^uint32(0), true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}),
		rec("x.local", ^uint32(0), true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.1")}),
	), now)
	if b.cacheSize() == 0 {
		t.Fatal("nothing cached")
	}
	b.expire(now.Add(maxCacheTTL * time.Second))
	if n := b.cacheSize() - len(b.emitted) - len(b.resolved); n != 0 {
		t.Errorf("%d records left after %d seconds", n, maxCacheTTL)
	}
}

// A packet of which nothing is cached does not make the browser go over
// its instances or send anything.
func TestBrowseIgnoredPacket(t *testing.T) {
	b := testBrowser()
	c := &capture{}
	b.t = c
	now := time.Unix(1e9, 0)
	b.handle(packetOf(t, rec(typeName, 120, false, &dnsmsg.PTR{Target: instanceName("m")})), now)
	c.take()
	for _, p := range []packet{
		packetOf(t),
		packetOf(t, rec("_http._tcp.local", 120, false, &dnsmsg.PTR{Target: "x._http._tcp.local"})),
	} {
		if out := b.handle(p, now); out != nil {
			t.Errorf("services %v", out)
		}
	}
	if ps := c.take(); len(ps) != 0 {
		t.Errorf("%d queries sent for packets of nothing", len(ps))
	}
}

// A TXT record with many keys keeps the keys of the standard and a bounded
// number of the others.
func TestTXTBounded(t *testing.T) {
	var strs []string
	for i := range 2000 {
		strs = append(strs, fmt.Sprintf("k%d=%s", i, strings.Repeat("v", 200)))
	}
	strs = append(strs, "dcap=/d", "https=443", "txtvers=1", "path=/p", "level=-S1")
	m := parseTXT(strs)
	for _, k := range []string{KeyDcap, KeyHTTPS, KeyTXTVersion, KeyPath, KeyLevel} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %s dropped", k)
		}
	}
	size := 0
	for k, v := range m {
		size += len(k) + len(v) + 1
	}
	if len(m) > maxTXTOtherKeys+5 || size > maxTXTBytes+5*255 {
		t.Errorf("%d keys of %d bytes kept", len(m), size)
	}
	m = parseTXT([]string{"a=1", "b=2"})
	if len(m) != 2 {
		t.Errorf("parseTXT = %v", m)
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = fmt.Sprintf("x%d", i)
	}
	if m := parseTXT(many); len(m) != maxTXTOtherKeys {
		t.Errorf("%d other keys kept", len(m))
	}
}

// Sources on a prefix of the interfaces and link-local sources are on the
// link; others are not. The prefixes are read again for an unknown source
// at most once per linkRefresh.
func TestOnLink(t *testing.T) {
	calls := 0
	l := &onLink{prefixes: func(*net.Interface) ([]netip.Prefix, error) {
		calls++
		return []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/64")}, nil
	}}
	for a, want := range map[string]bool{
		"192.0.2.7":        true,
		"::ffff:192.0.2.7": true,
		"2001:db8::7":      true,
		"fe80::1%en0":      true,
		"169.254.3.4":      true,
		"203.0.113.7":      false,
		"2001:db8:1::7":    false,
		"0.0.0.0":          false,
	} {
		if got := l.accept(netip.MustParseAddr(a)); got != want {
			t.Errorf("accept(%s) = %v", a, got)
		}
	}
	if calls != 1 {
		t.Errorf("prefixes read %d times", calls)
	}
	defer func(d time.Duration) { linkRefresh = d }(linkRefresh)
	linkRefresh = 0
	l.prefixes = func(*net.Interface) ([]netip.Prefix, error) { calls++; return nil, errClosed }
	if l.accept(netip.MustParseAddr("203.0.113.7")) || !l.accept(netip.MustParseAddr("192.0.2.1")) {
		t.Error("prefixes lost on an error")
	}
	if calls != 2 {
		t.Errorf("prefixes read %d times", calls)
	}
}

// The multicast transport drops packets from sources that are not on the
// link before they reach Browse or Advertise.
func TestTransportDropsOffLink(t *testing.T) {
	defer func(d time.Duration) { linkRefresh = d }(linkRefresh)
	linkRefresh = 0
	tr := loopTransport(t, "127.0.0.1:0")
	var on atomic.Bool
	asked := make(chan struct{}, 4)
	tr.link = &onLink{prefixes: func(*net.Interface) ([]netip.Prefix, error) {
		p := netip.MustParsePrefix("192.0.2.0/24")
		if on.Load() {
			p = netip.MustParsePrefix("127.0.0.0/8")
		}
		asked <- struct{}{}
		return []netip.Prefix{p}, nil
	}}
	tr.start()
	if err := tr.WritePacket([]byte("off link"), netip.AddrPort{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("packet not checked")
	}
	on.Store(true)
	if err := tr.WritePacket([]byte("on link"), netip.AddrPort{}); err != nil {
		t.Fatal(err)
	}
	if got := readOne(t, tr); got != "on link" {
		t.Errorf("read %q", got)
	}
}

// The prefixes of the interfaces include those of the loopback interface.
func TestInterfacePrefixes(t *testing.T) {
	ps, err := interfacePrefixes(nil)
	if err != nil {
		t.Skipf("interfaces: %v", err)
	}
	ifis, _ := net.Interfaces()
	for _, i := range ifis {
		if i.Flags&net.FlagLoopback == 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		own, err := interfacePrefixes(&i)
		if err != nil || len(own) == 0 {
			t.Skipf("loopback prefixes: %v", err)
		}
		if !containsAddr(ps, own[0].Addr()) {
			t.Errorf("%v not in %v", own[0], ps)
		}
		return
	}
	t.Skip("no loopback interface")
}
