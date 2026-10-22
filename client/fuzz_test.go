// SPDX-License-Identifier: MIT

package client_test

import (
	"net/http"
	"testing"

	"github.com/otfabric/go-ieee20305/client"
)

// FuzzResolve resolves arbitrary hrefs: Resolve never panics, and what it
// resolves is on the client's server.
func FuzzResolve(f *testing.F) {
	for _, s := range []string{"/edev/1", "edev", "https://utility.example:8443/dcap", "https://evil.example/dcap",
		"//evil.example/x", "/a/../../b", "%zz", "https://utility.example:8443@evil.example/"} {
		f.Add(s)
	}
	c, err := client.New(client.Config{Server: "https://utility.example:8443/sep2", HTTPClient: &http.Client{}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, href string) {
		u, err := c.Resolve(href)
		if err != nil {
			return
		}
		if u.Scheme != "https" || u.Host != "utility.example:8443" || u.User != nil {
			t.Fatalf("%q resolved to %s, off the server", href, u)
		}
	})
}
