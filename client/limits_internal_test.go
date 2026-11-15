// SPDX-License-Identifier: MIT

package client

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"
)

// A Retry-After is honoured up to maxRetryAfter, in seconds as in a date,
// and never wraps to a short or negative wait.
func TestRetryAfterBounded(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		v    string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"x", 0},
		{" 7 ", 7 * time.Second},
		{"86400", maxRetryAfter},
		{"86401", maxRetryAfter},
		{"9223372037", maxRetryAfter},
		{"18446744073709551615", maxRetryAfter},
		{"99999999999999999999999", maxRetryAfter},
		{now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
		{now.Add(400 * 24 * time.Hour).Format(http.TimeFormat), maxRetryAfter},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(c.v, now); got != c.want {
			t.Errorf("retryAfter(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestNewHTTPClientBoundsHeaders(t *testing.T) {
	tr := NewHTTPClient(tls.Certificate{}, x509.NewCertPool()).Transport.(*http.Transport)
	if tr.MaxResponseHeaderBytes != maxResponseHeaderBytes {
		t.Errorf("MaxResponseHeaderBytes = %d", tr.MaxResponseHeaderBytes)
	}
}

func TestCutZone(t *testing.T) {
	for _, c := range []struct {
		in, host, zone string
		ok             bool
	}{
		{"sk.example%", "sk.example%", "", false},
		{"[fe80::a%en0]:8443", "[fe80::a]:8443", "en0", true},
		{"[fe80::a]:8443", "[fe80::a]:8443", "", false},
		{"[fe80::a%en0", "[fe80::a%en0", "", false},
	} {
		host, zone, ok := cutZone(c.in)
		if host != c.host || zone != c.zone || ok != c.ok {
			t.Errorf("cutZone(%q) = %q, %q, %v", c.in, host, zone, ok)
		}
	}
}
