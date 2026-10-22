// SPDX-License-Identifier: MIT

// Package discovery implements DNS-SD service discovery for IEEE 2030.5
// servers, as specified in the standard's service-discovery annex.
//
// A server advertises itself as _smartenergy._tcp on the local network;
// a client browses for that service type and resolves the host, port
// and path of the server's Device Capability resource.
package discovery
