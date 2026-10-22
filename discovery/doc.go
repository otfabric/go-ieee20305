// SPDX-License-Identifier: MIT

// Package discovery implements DNS-SD service discovery for IEEE 2030.5
// servers, as specified in the standard's service-discovery annex.
//
// A server advertises itself as _smartenergy._tcp on the local network;
// a client browses for that service type and resolves the host, port
// and path of the server's Device Capability resource.
//
// Browse queries with multicast DNS (RFC 6762, RFC 6763) for
// _smartenergy._tcp.local, or for function-set subtypes such as
// _derp._sub._smartenergy._tcp.local, and delivers each Service it
// resolves; Service.DcapURL builds the URL of its DeviceCapability from
// the dcap and https keys of the TXT record. Advertise announces a
// server, answers queries for it and says goodbye when it is closed.
//
// Both send and receive through a PacketTransport: NewMulticastTransport
// joins the mDNS groups 224.0.0.251 and ff02::fb on port 5353, and tests
// can substitute an in-memory network.
package discovery
