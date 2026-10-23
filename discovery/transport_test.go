// SPDX-License-Identifier: MIT

package discovery

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// loopTransport is a multicastTransport over unicast UDP sockets on the
// loopback addresses given, each "group" being the socket itself, so that
// what it sends to the group it reads back. Its readers are not started.
func loopTransport(t *testing.T, addrs ...string) *multicastTransport {
	t.Helper()
	tr := &multicastTransport{packets: make(chan packet, 16), done: make(chan struct{})}
	for _, a := range addrs {
		ap := netip.MustParseAddrPort(a)
		network := "udp4"
		if ap.Addr().Is6() {
			network = "udp6"
		}
		c, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(ap))
		if err != nil {
			t.Skipf("no %s loopback: %v", network, err)
		}
		tr.conns = append(tr.conns, c)
		tr.groups = append(tr.groups, c.LocalAddr().(*net.UDPAddr).AddrPort())
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func (t *multicastTransport) start() {
	for _, c := range t.conns {
		t.wg.Add(1)
		go t.read(c)
	}
}

func readOne(t *testing.T, tr PacketTransport) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		b, _, err := tr.ReadPacket()
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(b)
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no packet")
		return ""
	}
}

func TestTransportWrite(t *testing.T) {
	tr := loopTransport(t, "127.0.0.1:0", "[::1]:0")
	tr.start()
	v4, v6 := tr.groups[0], tr.groups[1]

	// To the groups: one packet on each address family.
	if err := tr.WritePacket([]byte("group"), netip.AddrPort{}); err != nil {
		t.Fatal(err)
	}
	if a, b := readOne(t, tr), readOne(t, tr); a != "group" || b != "group" {
		t.Errorf("read %q and %q", a, b)
	}
	// To one address, an IPv4-mapped one too, on the socket of its family.
	mapped := netip.AddrPortFrom(netip.AddrFrom16(v4.Addr().As16()), v4.Port())
	for _, dst := range []netip.AddrPort{v4, mapped, v6} {
		if err := tr.WritePacket([]byte("unicast"), dst); err != nil {
			t.Fatalf("to %v: %v", dst, err)
		}
		if got := readOne(t, tr); got != "unicast" {
			t.Errorf("to %v: read %q", dst, got)
		}
	}

	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.ReadPacket(); !errors.Is(err, ieee20305.ErrClosed) || !errors.Is(err, net.ErrClosed) {
		t.Errorf("ReadPacket after Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestTransportWriteErrors(t *testing.T) {
	// No socket of the family of the destination.
	only4 := loopTransport(t, "127.0.0.1:0")
	if err := only4.WritePacket([]byte("x"), netip.MustParseAddrPort("[::1]:5353")); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Errorf("to IPv6 without an IPv6 socket: %v", err)
	}
	// Multicast that goes out on no socket fails with the errors of each.
	_ = only4.conns[0].Close()
	if err := only4.WritePacket([]byte("x"), netip.AddrPort{}); !errors.Is(err, net.ErrClosed) {
		t.Errorf("to the groups on closed sockets: %v", err)
	}
}

// A socket whose reads fail backs off, and Close stops it while it waits.
func TestTransportReadBackoff(t *testing.T) {
	tr := loopTransport(t, "127.0.0.1:0")
	_ = tr.conns[0].SetReadDeadline(time.Now().Add(-time.Second))
	tr.start()
	time.Sleep(20 * time.Millisecond) // a few timeouts, each waiting longer
	closed := make(chan struct{})
	go func() { _ = tr.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on a backing-off reader")
	}
}

// Close stops a reader blocked on a full queue that nobody reads.
func TestTransportCloseWithFullQueue(t *testing.T) {
	tr := loopTransport(t, "127.0.0.1:0")
	tr.start()
	deadline := time.Now().Add(5 * time.Second)
	for len(tr.packets) < cap(tr.packets) {
		if time.Now().After(deadline) {
			t.Fatalf("queue holds %d packets", len(tr.packets))
		}
		_ = tr.WritePacket([]byte("x"), netip.AddrPort{})
		time.Sleep(time.Millisecond)
	}
	// One more for the reader to hold while the queue is full.
	_ = tr.WritePacket([]byte("x"), netip.AddrPort{})
	time.Sleep(20 * time.Millisecond)
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewMulticastTransportNoInterface(t *testing.T) {
	tr, err := NewMulticastTransport(&net.Interface{Index: 1 << 20, Name: "none0", Flags: net.FlagUp | net.FlagMulticast})
	if err == nil {
		_ = tr.Close()
		t.Skip("this host joined the groups on an interface that does not exist")
	}
}
