// SPDX-License-Identifier: MIT

package discovery

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// testBrowser is a browser of the service type, without a transport that
// sends anything.
func testBrowser() *browser {
	b := &browser{
		t:        failing{},
		log:      nopLogger{},
		queries:  map[string]string{strings.ToLower(typeName): ""},
		order:    []string{typeName},
		ptr:      map[string]map[string]time.Time{},
		srv:      map[string]srvEntry{},
		txt:      map[string]txtEntry{},
		addrs:    map[string]map[netip.Addr]time.Time{},
		names:    map[string]string{},
		emitted:  map[string]Service{},
		resolved: map[dnsmsg.Question]time.Time{},
	}
	return b
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

var peer = netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, 66}), Port)

func packetOf(t *testing.T, rs ...dnsmsg.Resource) packet {
	t.Helper()
	data, err := (&dnsmsg.Message{Header: responseHeader, Answers: rs}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	return packet{data, peer}
}

// cacheSize is the number of records the browser holds.
func (b *browser) cacheSize() int {
	n := len(b.srv) + len(b.txt) + len(b.names) + len(b.emitted) + len(b.resolved)
	for _, m := range b.ptr {
		n += len(m)
	}
	for _, m := range b.addrs {
		n += len(m)
	}
	return n
}

// A peer on the link that floods records of names nobody browses, of new
// instances, and addresses of one host, with the longest TTL, does not grow
// the browser's cache without bound.
func TestBrowseCacheFlood(t *testing.T) {
	b := testBrowser()
	now := time.Unix(1e9, 0)
	const flood = 5000
	for i := range flood / 50 {
		var rs []dnsmsg.Resource
		for j := range 50 {
			k := i*50 + j
			inst := instanceName(fmt.Sprintf("m%d", k))
			host := fmt.Sprintf("h%d.local", k)
			rs = append(rs,
				rec(inst, ^uint32(0), false, &dnsmsg.SRV{Port: 1, Target: host}),
				rec(fmt.Sprintf("junk%d.local", k), ^uint32(0), false, &dnsmsg.TXT{Strings: []string{"dcap=/dcap"}}),
				rec(fmt.Sprintf("junk%d.local", k), ^uint32(0), false, &dnsmsg.SRV{Port: 1, Target: host}),
				rec(typeName, ^uint32(0), false, &dnsmsg.PTR{Target: inst}),
				rec(host, ^uint32(0), false, &dnsmsg.A{Addr: netip.AddrFrom4([4]byte{10, 0, byte(k >> 8), byte(k)})}),
				rec("one.local", ^uint32(0), false, &dnsmsg.AAAA{Addr: netip.AddrFrom16([16]byte{0xfd, 14: byte(k >> 8), 15: byte(k)})}),
			)
		}
		b.handle(packetOf(t, rs...), now)
	}
	if n := b.cacheSize(); n > 6*maxCacheEntries+maxHostAddrs {
		t.Errorf("%d records cached after a flood of %d, want at most %d", n, 6*flood, 6*maxCacheEntries+maxHostAddrs)
	}
	if n := len(b.addrs["one.local"]); n > maxHostAddrs {
		t.Errorf("%d addresses of one host", n)
	}
	// The cache is still of use: an instance of a full cache is found once
	// the flood expires.
	b.expire(now.Add(time.Duration(^uint32(0))*time.Second + time.Second))
	if n := b.cacheSize(); n != len(b.emitted)+len(b.resolved) {
		t.Errorf("%d records left after expiry", n-len(b.emitted)-len(b.resolved))
	}
}

// Records whose names are no instance of the service type are not cached as
// SRV or TXT records of an instance.
func TestBrowseIgnoresForeignSRV(t *testing.T) {
	b := testBrowser()
	now := time.Unix(1e9, 0)
	b.handle(packetOf(t,
		rec("printer._ipp._tcp.local", 120, false, &dnsmsg.SRV{Port: 631, Target: "p.local"}),
		rec("printer._ipp._tcp.local", 120, false, &dnsmsg.TXT{Strings: []string{"x=1"}}),
	), now)
	if len(b.srv)+len(b.txt) != 0 {
		t.Errorf("foreign records cached: %v %v", b.srv, b.txt)
	}
}

// Packets that are no response, or from another port, or malformed, change
// nothing.
func TestBrowseHostilePackets(t *testing.T) {
	b := testBrowser()
	now := time.Unix(1e9, 0)
	good := packetOf(t, rec(typeName, 120, false, &dnsmsg.PTR{Target: instanceName("m")}))
	for name, p := range map[string]packet{
		"short":       {[]byte{0, 0, 0x84}, peer},
		"other port":  {good.data, netip.AddrPortFrom(peer.Addr(), 53)},
		"query":       {append([]byte{0, 0, 0, 0}, good.data[4:]...), peer},
		"rcode":       {append([]byte{0, 0, 0x84, 3}, good.data[4:]...), peer},
		"truncated":   {good.data[:len(good.data)-3], peer},
		"pointer":     {[]byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0xc0, 12, 0, 12, 0, 1, 0, 0, 0, 1, 0, 2, 0xc0, 12}, peer},
		"huge counts": {[]byte{0, 0, 0x84, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, peer},
	} {
		if out := b.handle(p, now); len(out) != 0 || b.cacheSize() != 0 {
			t.Errorf("%s: %d services, %d records", name, len(out), b.cacheSize())
		}
	}
}

// FuzzBrowse feeds arbitrary packets to the browser: it never panics, and
// its cache stays bounded.
func FuzzBrowse(f *testing.F) {
	for _, rs := range [][]dnsmsg.Resource{
		{rec(typeName, 120, false, &dnsmsg.PTR{Target: instanceName("m")}),
			rec(instanceName("m"), 120, true, &dnsmsg.SRV{Port: 8443, Target: "h.local"}),
			rec(instanceName("m"), 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/dcap", "https=8443"}}),
			rec("h.local", 120, true, &dnsmsg.A{Addr: netip.AddrFrom4([4]byte{192, 0, 2, 1})})},
		{rec(typeName, 0, false, &dnsmsg.PTR{Target: instanceName("m")})},
		{rec("h.local", 120, true, &dnsmsg.AAAA{Addr: netip.MustParseAddr("fe80::1")})},
	} {
		data, err := (&dnsmsg.Message{Header: responseHeader, Answers: rs}).Encode()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		b := testBrowser()
		now := time.Unix(1e9, 0)
		for range 2 {
			b.handle(packet{data, peer}, now)
		}
		b.expire(now.Add(time.Hour))
		b.reconcile(now.Add(time.Hour))
		if n := b.cacheSize(); n > 6*maxCacheEntries+maxHostAddrs {
			t.Fatalf("%d records", n)
		}
	})
}
