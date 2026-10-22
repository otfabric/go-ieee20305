// SPDX-License-Identifier: MIT

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package discovery

import (
	"net"
	"syscall"
)

// enableLoopback turns multicast loopback back on for c, which
// net.ListenMulticastUDP turns off, so that a browser receives the
// announcements of a service on the same host. Failure is not fatal.
func enableLoopback(c *net.UDPConn, v4 bool) {
	rc, err := c.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		if v4 {
			_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		} else {
			_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_LOOP, 1)
		}
	})
}
