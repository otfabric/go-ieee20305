// SPDX-License-Identifier: MIT

package client_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/otfabric/go-ieee20305/client"
)

// FuzzResolve resolves arbitrary hrefs against servers whose names have the
// letters Unicode folds beyond ASCII (s, k) and an IPv6 zone: Resolve never
// panics, and what it resolves is on the client's server. Case is
// insignificant in the host in ASCII only (RFC 3986 section 6.2.2.1).
func FuzzResolve(f *testing.F) {
	for _, s := range []string{"/edev/1", "edev", "https://utility.example:8443/dcap", "https://evil.example/dcap",
		"//evil.example/x", "/a/../../b", "%zz", "https://utility.example:8443@evil.example/",
		"//user@utility.example:8443/", "//@utility.example:8443/", "//utility.example.:8443/", "//utility.example:08443/",
		"//\u017fk.example:8443/", "//s\u212a.example:8443/", "//%C5%BFk.example:8443/",
		"//[fe80::a%25EN0]:8443/", "//[FE80::A%25en0]:8443/"} {
		f.Add(s)
	}
	var clients []*client.Client
	var hosts []string
	for _, tc := range []struct{ server, host string }{
		{"https://utility.example:8443/sep2", "utility.example:8443"},
		{"https://sk.example:8443", "sk.example:8443"},
		{"https://[fe80::a%25en0]:8443", "[fe80::a%en0]:8443"},
	} {
		c, err := client.New(client.Config{Server: tc.server, HTTPClient: &http.Client{}})
		if err != nil {
			f.Fatal(err)
		}
		clients, hosts = append(clients, c), append(hosts, tc.host)
	}
	f.Fuzz(func(t *testing.T, href string) {
		for i, c := range clients {
			u, err := c.Resolve(href)
			if err != nil {
				continue
			}
			if u.Scheme != "https" || !onHost(u.Host, hosts[i]) || u.User != nil {
				t.Fatalf("%q resolved to %s, off the server %s", href, u, hosts[i])
			}
		}
	})
}

// onHost reports whether got is want, a lowercase host, but for the case of
// its ASCII letters before an IPv6 zone.
func onHost(got, want string) bool {
	name, zone, found := strings.Cut(got, "%")
	folded := []byte(name)
	for i, c := range folded {
		if 'A' <= c && c <= 'Z' {
			folded[i] = c + 'a' - 'A'
		}
	}
	if found {
		return string(folded)+"%"+zone == want
	}
	return string(folded) == want
}
