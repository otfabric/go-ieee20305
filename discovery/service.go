// SPDX-License-Identifier: MIT

package discovery

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
)

// ServiceType is the DNS-SD service type of IEEE 2030.5 servers.
const ServiceType = "_smartenergy._tcp"

// Domain is the multicast DNS domain that Browse and Advertise use.
const Domain = "local"

// Keys of the TXT record of an IEEE 2030.5 service, as defined by the
// table of DNS-SD TXT record parameters in the service discovery clause of
// IEEE Std 2030.5-2018, which also requires clients to ignore other keys
// and to discard records without a non-empty dcap.
const (
	// KeyTXTVersion is the version of the TXT record format, "1".
	KeyTXTVersion = "txtvers"
	// KeyDcap is the path of the server's DeviceCapability resource,
	// a relative reference starting with "/". It is required: clients
	// discard records without it.
	KeyDcap = "dcap"
	// KeyPath is the path of the resource of the function set that a
	// subtype advertisement is for.
	KeyPath = "path"
	// KeyHTTPS is the TCP port of the server's HTTPS service. When it is
	// absent, DcapURL uses http on the port of the SRV record.
	KeyHTTPS = "https"
	// KeyLevel lists the security and compliance levels the server
	// supports, such as "-S1".
	KeyLevel = "level"
)

// ErrNoDcap is matched by the error of DcapURL when the service has no
// usable dcap key.
var ErrNoDcap = fmt.Errorf("%w: discovery: no dcap path in the TXT record", ieee20305.ErrProtocol)

// Service is an IEEE 2030.5 server announced with DNS-SD.
type Service struct {
	// Instance is the service instance name, the first label of the
	// service's DNS name (Instance._smartenergy._tcp.local). It may
	// contain dots and spaces.
	Instance string
	// Host is the target of the SRV record, such as "meter-1.local".
	Host string
	// Port is the port of the SRV record.
	Port int
	// Addrs are the IPv4 and IPv6 addresses of Host.
	Addrs []netip.Addr
	// TXT holds the key/value pairs of the TXT record; keys are in lower
	// case and a key without "=" has the empty value.
	TXT map[string]string
	// Subtypes are the function-set subtypes the service is announced
	// under, such as "_derp" or "_edev".
	Subtypes []string
	// Removed is set on a Service that Browse delivers when the server
	// withdrew it with a goodbye or its records expired.
	Removed bool
}

// DcapURL returns the URL of the server's DeviceCapability resource:
// https on the port of the "https" TXT key when the record has one,
// otherwise http on the SRV port, with the path of the "dcap" key. The
// host is the first IPv4 address of the service, or its first address,
// or Host when it has none.
func (s Service) DcapURL() (string, error) {
	dcap := s.TXT[KeyDcap]
	if !strings.HasPrefix(dcap, "/") || strings.HasPrefix(dcap, "//") {
		return "", fmt.Errorf("%w: dcap=%q", ErrNoDcap, dcap)
	}
	ref, err := url.Parse(dcap)
	if err != nil {
		return "", fmt.Errorf("%w: dcap=%q: %v", ErrNoDcap, dcap, err)
	}
	scheme, port := "http", s.Port
	if v, ok := s.TXT[KeyHTTPS]; ok {
		p, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("%w: discovery: https=%q is not a port", ieee20305.ErrProtocol, v)
		}
		scheme, port = "https", p
	}
	if port < 1 || port > 0xffff {
		return "", fmt.Errorf("%w: discovery: %s port %d out of range", ieee20305.ErrProtocol, scheme, port)
	}
	host := strings.TrimSuffix(s.Host, ".")
	if a, ok := preferredAddr(s.Addrs); ok {
		host = a.String()
	}
	if host == "" {
		return "", fmt.Errorf("%w: discovery: service has neither a host nor an address", ieee20305.ErrProtocol)
	}
	base := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	return base.ResolveReference(ref).String(), nil
}

func preferredAddr(addrs []netip.Addr) (netip.Addr, bool) {
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a.Unmap(), true
		}
	}
	for _, a := range addrs {
		if a.IsValid() {
			return a, true
		}
	}
	return netip.Addr{}, false
}

// parseTXT returns the key/value pairs of TXT strings (RFC 6763, section
// 6): keys are case insensitive, the first occurrence of a key wins, and
// strings without a key are ignored.
func parseTXT(strs []string) map[string]string {
	m := make(map[string]string, len(strs))
	for _, s := range strs {
		k, v, _ := strings.Cut(s, "=")
		if k == "" {
			continue
		}
		k = strings.ToLower(k)
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return m
}

// txtStrings returns the TXT strings of m: txtvers first, as RFC 6763
// recommends, then the other keys in order.
func txtStrings(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != KeyTXTVersion {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(m)+1)
	v, ok := m[KeyTXTVersion]
	if !ok {
		v = "1"
	}
	out = append(out, KeyTXTVersion+"="+v)
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// subtypeLabel returns the label of a subtype with its leading
// underscore, accepting it with or without one.
func subtypeLabel(s string) string {
	if strings.HasPrefix(s, "_") {
		return s
	}
	return "_" + s
}

// typeName is the DNS name of the IEEE 2030.5 service type.
var typeName = ServiceType + "." + Domain

// subtypeName returns the DNS name of a subtype of the service type.
func subtypeName(sub string) string {
	return dnsmsg.JoinLabel(subtypeLabel(sub), "_sub."+typeName)
}

// instanceName returns the DNS name of a service instance.
func instanceName(instance string) string {
	return dnsmsg.JoinLabel(instance, typeName)
}
