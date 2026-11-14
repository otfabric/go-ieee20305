// SPDX-License-Identifier: MIT

package discovery

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
	"github.com/otfabric/go-ieee20305/internal/leakcheck"
)

// delivered returns the services of pending as a receiver of Browse gets
// them.
func delivered(t *testing.T, b *browser, pending []Service) []Service {
	t.Helper()
	out := make(chan Service, len(pending))
	if !b.deliver(context.Background(), out, pending) {
		t.Fatal("deliver stopped")
	}
	close(out)
	return slices.Collect(chanSeq(out))
}

func chanSeq(ch <-chan Service) func(yield func(Service) bool) {
	return func(yield func(Service) bool) {
		for s := range ch {
			if !yield(s) {
				return
			}
		}
	}
}

// TestBrowseDeliversOwnService: a Service that Browse delivers is the
// receiver's. What the receiver changes in its TXT map and its slices
// changes neither what the browser holds, nor what it delivers next, nor a
// Service it delivered before; and the browser delivers nothing for it.
func TestBrowseDeliversOwnService(t *testing.T) {
	b := testBrowser()
	b.queries[strings.ToLower(subtypeName("_derp"))] = "_derp"
	now := time.Unix(1e9, 0)
	inst, host := instanceName("m1"), "h1.local"
	addr, other := netip.AddrFrom4([4]byte{192, 0, 2, 1}), netip.AddrFrom4([4]byte{203, 0, 113, 9})
	first := delivered(t, b, b.handle(packetOf(t,
		rec(typeName, 120, false, &dnsmsg.PTR{Target: inst}),
		rec(subtypeName("_derp"), 120, false, &dnsmsg.PTR{Target: inst}),
		rec(inst, 120, true, &dnsmsg.SRV{Port: 8443, Target: host}),
		rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/dcap", "https=8443"}}),
		rec(host, 120, true, &dnsmsg.A{Addr: addr}),
	), now))
	if len(first) != 1 || first[0].Removed || first[0].TXT[KeyDcap] != "/dcap" || len(first[0].Addrs) != 1 || len(first[0].Subtypes) != 1 {
		t.Fatalf("first delivery: %+v", first)
	}
	s := first[0]
	want := Service{Instance: s.Instance, Host: s.Host, Port: s.Port, TXT: maps.Clone(s.TXT),
		Addrs: slices.Clone(s.Addrs), Subtypes: slices.Clone(s.Subtypes)}

	// The receiver changes what it got.
	delete(s.TXT, KeyDcap)
	s.TXT["added"] = "by the receiver"
	s.Addrs[0] = other
	s.Subtypes[0] = "_changed"

	// The browser has nothing to deliver, and holds what the records say.
	if again := b.reconcile(now.Add(time.Second)); len(again) != 0 {
		t.Fatalf("after the receiver changed its Service, the browser delivers %+v", again)
	}
	held, complete, _ := b.service(strings.ToLower(inst))
	if !complete || !equalServices(held, want) {
		t.Fatalf("the browser holds %+v (complete %t), want %+v", held, complete, want)
	}
	if last := b.emitted[strings.ToLower(inst)]; !equalServices(last, want) {
		t.Fatalf("the browser remembers delivering %+v, want %+v", last, want)
	}

	// A change of a record is delivered with a map and slices of its own
	// again, and leaves the first Service as the receiver made it.
	second := delivered(t, b, b.handle(packetOf(t,
		rec(inst, 120, true, &dnsmsg.TXT{Strings: []string{"dcap=/dcap", "https=443"}}),
	), now.Add(2*time.Second)))
	if len(second) != 1 || second[0].TXT[KeyHTTPS] != "443" || second[0].TXT[KeyDcap] != "/dcap" ||
		!slices.Equal(second[0].Addrs, want.Addrs) || !slices.Equal(second[0].Subtypes, want.Subtypes) {
		t.Fatalf("second delivery: %+v", second)
	}
	if s.TXT["added"] != "by the receiver" || s.Addrs[0] != other || s.TXT[KeyHTTPS] != "8443" {
		t.Fatalf("the first Service changed with the second: %+v", s)
	}
	second[0].TXT[KeyDcap] = "/changed"
	second[0].Addrs[0] = other

	// The Service delivered when the instance goes is the receiver's too,
	// and says what the records said.
	gone := delivered(t, b, b.handle(packetOf(t,
		rec(typeName, 0, false, &dnsmsg.PTR{Target: inst}),
		rec(subtypeName("_derp"), 0, false, &dnsmsg.PTR{Target: inst}),
	), now.Add(3*time.Second)))
	if len(gone) != 1 || !gone[0].Removed || gone[0].TXT[KeyDcap] != "/dcap" || gone[0].Addrs[0] != addr {
		t.Fatalf("delivery of the removal: %+v", gone)
	}
	gone[0].TXT[KeyDcap] = "/gone"
	if second[0].TXT[KeyDcap] != "/changed" {
		t.Fatalf("the second Service changed with the removal: %+v", second[0])
	}
	if txt := b.txt[strings.ToLower(inst)].txt; txt[KeyDcap] != "/dcap" || txt[KeyHTTPS] != "443" {
		t.Fatalf("the browser's TXT record changed: %v", txt)
	}
}

// TestServiceOwn: own copies the TXT map and the slices, and keeps a
// Service without them as it is.
func TestServiceOwn(t *testing.T) {
	if got := (Service{Instance: "m"}).own(); got.TXT != nil || got.Addrs != nil || got.Subtypes != nil || got.Instance != "m" {
		t.Errorf("own of a Service without a map or slices: %+v", got)
	}
	s := meter("m", 1, "_derp")
	c := s.own()
	if !equalServices(s, c) {
		t.Fatalf("own = %+v, want %+v", c, s)
	}
	c.TXT[KeyDcap], c.Addrs[0], c.Subtypes[0] = "/x", netip.Addr{}, "_x"
	if s.TXT[KeyDcap] != "/dcap" || !s.Addrs[0].IsValid() || s.Subtypes[0] != "_derp" {
		t.Errorf("own shares with its Service: %+v", s)
	}
}

// TestAdvertisementCloseTwice: Close of an advertisement may be called
// more than once, and from several goroutines at once; every call returns
// the same, after the goodbye (contract rows L1 and L2).
func TestAdvertisementCloseTwice(t *testing.T) {
	leakcheck.Check(t)
	b := newBus()
	a, err := Advertise(context.Background(), meter("m", 1), AdvertiseOptions{Transport: b.join()})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 16)
	for range 8 {
		wg.Go(func() {
			<-start
			errs <- a.Close()
			errs <- a.Close()
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Close returned %v", err)
		}
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close again returned %v", err)
	}
}
