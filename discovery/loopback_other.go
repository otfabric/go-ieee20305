// SPDX-License-Identifier: MIT

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package discovery

import "net"

// enableLoopback leaves multicast loopback off on this platform.
func enableLoopback(*net.UDPConn, bool) {}
