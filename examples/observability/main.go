// SPDX-License-Identifier: MIT

// Observability shows the plug points of a server and a client: a log/slog
// logger, metrics hooks that count requests by resource and status, and a
// retry policy on the client. The server answers every third request with
// 503, which the client retries.
//
//	go run ./examples/observability
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// counter counts by key; a stand-in for a Prometheus vector.
type counter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counter) inc(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[key]++
}

func (c *counter) print(title string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.n))
	for k := range c.n {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println(title)
	for _, k := range keys {
		fmt.Printf("  %-40s %d\n", k, c.n[k])
	}
}

// serverMetrics implements server.Metrics; NopMetrics supplies the
// optional hooks it leaves out.
type serverMetrics struct {
	server.NopMetrics
	c *counter
}

func (m serverMetrics) OnResponse(method, resource string, status int, _ time.Duration) {
	m.c.inc(fmt.Sprintf("%s %s %d", method, resource, status))
}

// clientMetrics implements client.Metrics and client.AttemptMetrics.
type clientMetrics struct {
	client.NopMetrics
	c *counter
}

func (m clientMetrics) OnResponse(method, path string, status int, _ time.Duration) {
	m.c.inc(fmt.Sprintf("%s %s %d", method, path, status))
}

func (m clientMetrics) OnRetry(method, path string, attempt int, err error, wait time.Duration) {
	m.c.inc(fmt.Sprintf("retry %s %s", method, path))
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ca, caKey, err := security.TestCA("observability example CA")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverCert, serverKey, err := security.TestServerCert(ca, caKey, []string{"127.0.0.1"})
	if err != nil {
		return err
	}
	deviceCert, deviceKey, err := security.TestDeviceCert(ca, caKey, "meter-1")
	if err != nil {
		return err
	}
	device := tls.Certificate{Certificate: [][]byte{deviceCert.Raw}, PrivateKey: deviceKey, Leaf: deviceCert}

	// Warnings and errors of both ends to stderr, with their component.
	logger := ieee20305.NewSlogLogger(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	served, sent := &counter{}, &counter{}
	srv, err := server.New(server.Config{Logger: logger, Metrics: serverMetrics{c: served}})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()
	if _, err := srv.Admin().ProvisionDevice(ctx, server.DeviceSpec{LFDI: security.CertificateLFDI(deviceCert), Register: true}); err != nil {
		return err
	}
	// Every third request is refused with 503 before it reaches the server.
	var n atomic.Int64
	flaky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1)%3 == 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		srv.ServeHTTP(w, r)
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: flaky, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: security.ServerTLSConfig(tls.Certificate{Certificate: [][]byte{serverCert.Raw}, PrivateKey: serverKey}, roots)}
	go func() { _ = hs.ServeTLS(l, "", "") }()
	defer func() { _ = hs.Close() }()

	c, err := client.New(client.Config{
		Server:      "https://" + l.Addr().String() + "/dcap",
		HTTPClient:  client.NewHTTPClient(device, roots),
		Logger:      logger,
		Metrics:     clientMetrics{c: sent},
		RetryPolicy: ieee20305.ExponentialBackoff{Initial: 10 * time.Millisecond, Max: 100 * time.Millisecond, Attempts: 4},
	})
	if err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		dcap, err := c.DeviceCapability(ctx)
		if err != nil {
			return err
		}
		if _, _, err := c.Register(ctx, dcap.EndDeviceListLink.Href, client.IdentityOf(deviceCert)); err != nil {
			return err
		}
	}
	sent.print("client: responses and retries")
	served.print("server: responses by WADL resource")
	return nil
}
