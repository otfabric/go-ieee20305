// SPDX-License-Identifier: MIT

package discovery

import (
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// bus is an in-memory multicast network: a packet sent to the group
// reaches every endpoint, the sender included, and a unicast packet the
// endpoint with that address.
type bus struct {
	mu    sync.Mutex
	nodes map[netip.AddrPort]*node
	next  byte
	// unicast records the unicast packets sent, by destination.
	unicast map[netip.AddrPort]int
}

func newBus() *bus {
	return &bus{nodes: map[netip.AddrPort]*node{}, next: 1, unicast: map[netip.AddrPort]int{}}
}

type node struct {
	bus     *bus
	addr    netip.AddrPort
	packets chan packet
	done    chan struct{}
	once    sync.Once
}

// join adds an endpoint on port 5353 with the next address.
func (b *bus) join() *node {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, b.next}), Port)
	b.next++
	return b.joinAt(a)
}

func (b *bus) joinAt(a netip.AddrPort) *node {
	n := &node{bus: b, addr: a, packets: make(chan packet, 64), done: make(chan struct{})}
	b.nodes[a] = n
	return n
}

func (n *node) ReadPacket() ([]byte, netip.AddrPort, error) {
	select {
	case p := <-n.packets:
		return p.data, p.src, nil
	case <-n.done:
		return nil, netip.AddrPort{}, net.ErrClosed
	}
}

func (n *node) WritePacket(data []byte, dst netip.AddrPort) error {
	select {
	case <-n.done:
		return net.ErrClosed
	default:
	}
	n.bus.mu.Lock()
	var to []*node
	if dst.IsValid() {
		n.bus.unicast[dst]++
		if d, ok := n.bus.nodes[dst]; ok {
			to = append(to, d)
		}
	} else {
		for _, d := range n.bus.nodes {
			to = append(to, d)
		}
	}
	n.bus.mu.Unlock()
	for _, d := range to {
		select {
		case d.packets <- packet{append([]byte{}, data...), n.addr}:
		default:
			// A full queue drops the packet, as UDP would.
		}
	}
	return nil
}

func (n *node) Close() error {
	n.once.Do(func() {
		close(n.done)
		n.bus.mu.Lock()
		delete(n.bus.nodes, n.addr)
		n.bus.mu.Unlock()
	})
	return nil
}

func (b *bus) unicastTo(a netip.AddrPort) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unicast[a]
}

// send sends m from n, failing the test on an error.
func (n *node) send(t *testing.T, m *dnsmsg.Message, dst netip.AddrPort) {
	t.Helper()
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.WritePacket(data, dst); err != nil {
		t.Fatal(err)
	}
}

// recv returns the next message n receives, failing after a timeout.
func (n *node) recv(t *testing.T) (*dnsmsg.Message, netip.AddrPort) {
	t.Helper()
	select {
	case p := <-n.packets:
		m, err := dnsmsg.Parse(p.data)
		if err != nil {
			t.Fatal(err)
		}
		return m, p.src
	case <-time.After(5 * time.Second):
		t.Fatal("no packet")
		return nil, netip.AddrPort{}
	}
}
