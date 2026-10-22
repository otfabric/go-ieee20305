// SPDX-License-Identifier: MIT

package discovery

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// errClosed is the error of the use of something of this package after it
// was closed: it matches ieee20305.ErrClosed and net.ErrClosed.
var errClosed = fmt.Errorf("%w: discovery: %w", ieee20305.ErrClosed, net.ErrClosed)

// mDNS group addresses and port (RFC 6762).
var (
	GroupIPv4 = netip.MustParseAddrPort("224.0.0.251:5353")
	GroupIPv6 = netip.MustParseAddrPort("[ff02::fb]:5353")
)

// Port is the multicast DNS port.
const Port = 5353

// maxPacket is the largest multicast DNS message (RFC 6762, section 17).
const maxPacket = 9000

// PacketTransport sends and receives multicast DNS packets. Browse and
// Advertise take ownership of the transport they are given and close it
// when they finish. Its methods are called from several goroutines.
type PacketTransport interface {
	// ReadPacket blocks until a packet arrives and returns it with the
	// address it came from. After Close it returns an error.
	ReadPacket() (data []byte, src netip.AddrPort, err error)
	// WritePacket sends data to dst, or to the multicast DNS group of
	// every address family the transport has joined when dst is the
	// zero AddrPort.
	WritePacket(data []byte, dst netip.AddrPort) error
	// Close closes the transport and unblocks ReadPacket.
	Close() error
}

type packet struct {
	data []byte
	src  netip.AddrPort
}

// multicastTransport is the PacketTransport over UDP sockets joined to the
// IPv4 and IPv6 mDNS groups on port 5353.
type multicastTransport struct {
	conns   []*net.UDPConn
	groups  []netip.AddrPort
	packets chan packet
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

// NewMulticastTransport returns a PacketTransport that joins the multicast
// DNS groups on ifi, or on the system's default multicast interface when
// ifi is nil. It joins the IPv4 group and, where the host supports it, the
// IPv6 group, and fails only when it can join neither. Multicast loopback
// is enabled where the platform allows, so that services on this host
// find each other.
func NewMulticastTransport(ifi *net.Interface) (PacketTransport, error) {
	t := &multicastTransport{packets: make(chan packet, 16), done: make(chan struct{})}
	var errs []error
	for _, g := range []struct {
		network string
		group   netip.AddrPort
	}{{"udp4", GroupIPv4}, {"udp6", GroupIPv6}} {
		c, err := net.ListenMulticastUDP(g.network, ifi, net.UDPAddrFromAddrPort(g.group))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		enableLoopback(c, g.group.Addr().Is4())
		t.conns = append(t.conns, c)
		t.groups = append(t.groups, g.group)
	}
	if len(t.conns) == 0 {
		return nil, errors.Join(errs...)
	}
	for _, c := range t.conns {
		t.wg.Add(1)
		go t.read(c)
	}
	return t, nil
}

func (t *multicastTransport) read(c *net.UDPConn) {
	defer t.wg.Done()
	var backoff time.Duration
	for {
		b := make([]byte, maxPacket)
		n, src, err := c.ReadFromUDPAddrPort(b)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// An error that persists (a socket in a bad state) must not
			// spin: wait longer each time, up to a second.
			backoff = min(max(2*backoff, time.Millisecond), time.Second)
			select {
			case <-time.After(backoff):
			case <-t.done:
				return
			}
			continue
		}
		backoff = 0
		select {
		case t.packets <- packet{b[:n], src}:
		case <-t.done:
			return
		}
	}
}

func (t *multicastTransport) ReadPacket() ([]byte, netip.AddrPort, error) {
	select {
	case p := <-t.packets:
		return p.data, p.src, nil
	case <-t.done:
		return nil, netip.AddrPort{}, errClosed
	}
}

func (t *multicastTransport) WritePacket(data []byte, dst netip.AddrPort) error {
	var errs []error
	sent := false
	for i, c := range t.conns {
		g := t.groups[i]
		switch {
		case !dst.IsValid():
			_, err := c.WriteToUDPAddrPort(data, g)
			errs = append(errs, err)
			sent = sent || err == nil
		case dst.Addr().Unmap().Is4() && g.Addr().Is4():
			_, err := c.WriteToUDPAddrPort(data, netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port()))
			return err
		case !dst.Addr().Unmap().Is4() && !g.Addr().Is4():
			_, err := c.WriteToUDPAddrPort(data, dst)
			return err
		}
	}
	if !dst.IsValid() {
		if sent {
			// Multicast went out on at least one address family.
			return nil
		}
		return errors.Join(errs...)
	}
	return fmt.Errorf("%w: discovery: no socket for the address family of %s", ieee20305.ErrConfiguration, dst)
}

func (t *multicastTransport) Close() error {
	var errs []error
	t.once.Do(func() {
		close(t.done)
		for _, c := range t.conns {
			errs = append(errs, c.Close())
		}
		t.wg.Wait()
	})
	return errors.Join(errs...)
}
