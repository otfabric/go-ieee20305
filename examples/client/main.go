// SPDX-License-Identifier: MIT

// Client connects to an IEEE 2030.5 server as a device, over mutual TLS:
// it reads the DeviceCapability, finds or creates its EndDevice, checks
// the PIN of its Registration, and walks the resources the server offers
// it, printing each with the link it was reached by. Run it against
// examples/server:
//
//	go run ./examples/server -pki /tmp/ieee20305-pki
//	go run ./examples/client -pki /tmp/ieee20305-pki
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/otfabric/go-ieee20305/client"
)

func main() {
	dir := flag.String("pki", "ieee20305-pki", "directory with ca.crt, device.crt and device.key")
	url := flag.String("url", "https://127.0.0.1:8443/dcap", "URL of the server's DeviceCapability")
	pin := flag.Uint("pin", 111115, "PIN the device was given out of band")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, *dir, *url, uint32(*pin)); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir, url string, pin uint32) error {
	device, err := tls.LoadX509KeyPair(filepath.Join(dir, "device.crt"), filepath.Join(dir, "device.key"))
	if err != nil {
		return err
	}
	if device.Leaf == nil {
		if device.Leaf, err = x509.ParseCertificate(device.Certificate[0]); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return fmt.Errorf("%s: no CA certificate", dir)
	}

	c, err := client.New(client.Config{Server: url, HTTPClient: client.NewHTTPClient(device, roots)})
	if err != nil {
		return err
	}
	dcap, err := c.DeviceCapability(ctx)
	if err != nil {
		return err
	}
	if dcap.EndDeviceListLink == nil {
		return fmt.Errorf("the server offers no EndDeviceList")
	}
	id := client.IdentityOf(device.Leaf)
	ed, created, err := c.Register(ctx, dcap.EndDeviceListLink.Href, id)
	if err != nil {
		return err
	}
	fmt.Printf("EndDevice %s (created: %v) for LFDI %X\n", ed.GetHref(), created, id.LFDI[:])
	reg, err := c.Registration(ctx, ed)
	if err != nil {
		return err
	}
	if err := client.CheckPIN(reg, pin); err != nil {
		return err
	}
	fmt.Println("the PIN of the Registration matches")

	return c.Walk(ctx, dcap.GetHref(), client.WalkOptions{MaxDepth: 4}, func(s client.Step) error {
		via := "root"
		if s.Link.Element != "" {
			via = s.Link.Element
		}
		fmt.Printf("%s%-40s %T (via %s)\n", strings.Repeat("  ", s.Depth), s.Href, s.Doc, via)
		return nil
	})
}
