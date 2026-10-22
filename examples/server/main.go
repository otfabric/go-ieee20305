// SPDX-License-Identifier: MIT

// Server serves IEEE 2030.5 over mutual TLS until it is interrupted. It
// writes a test PKI to -pki the first time (a CA, its own certificate and a
// device's), provisions that device with PIN 111115, and publishes a DER
// program with a control that limits the device's output to 50 % for an
// hour. Run examples/client against it:
//
//	go run ./examples/server -pki /tmp/ieee20305-pki
//	go run ./examples/client -pki /tmp/ieee20305-pki
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

func main() {
	dir := flag.String("pki", "ieee20305-pki", "directory of the test PKI, written when it does not exist")
	addr := flag.String("listen", "127.0.0.1:8443", "address to listen on")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *dir, *addr); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir, addr string) error {
	if err := ensurePKI(dir); err != nil {
		return err
	}
	serverCert, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	if err != nil {
		return err
	}
	roots, err := loadPool(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return err
	}
	deviceCert, err := loadCert(filepath.Join(dir, "device.crt"))
	if err != nil {
		return err
	}

	srv, err := server.New(server.Config{
		Logger: ieee20305.NewSlogLogger(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		OnResponse: func(_ context.Context, href string, r any) {
			if resp, ok := r.(*ieee20305.DERControlResponse); ok && resp.Status != nil {
				fmt.Printf("response %d to control %X at %s\n", *resp.Status, []byte(resp.Subject), href)
			}
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()
	lfdi := security.CertificateLFDI(deviceCert)
	if err := seed(ctx, srv.Admin(), lfdi); err != nil {
		return err
	}
	fmt.Printf("provisioned device %s (SFDI %s) with PIN 111115\n", lfdi, lfdi.SFDI())

	hs := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: security.ServerTLSConfig(serverCert, roots)}
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	fmt.Printf("serving https://%s/dcap\n", addr)
	if err := hs.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// seed provisions the device and assigns it a DER program with a control.
func seed(ctx context.Context, a *server.Admin, lfdi security.LFDI) error {
	for _, root := range []struct{ resource, href string }{{"DERProgramList", "/derp"}, {"ResponseSetList", "/rsps"}} {
		if err := a.Root(ctx, root.resource, root.href, a.Document(root.resource)); err != nil {
			return err
		}
	}
	rs, err := a.Create(ctx, "/rsps", &ieee20305.ResponseSet{}, "")
	if err != nil {
		return err
	}
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true, PIN: 111115}); err != nil {
		return err
	}
	fsa, err := a.AssignFSA(ctx, lfdi, &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{0x01}})
	if err != nil {
		return err
	}
	if err := a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", lfdi.String()); err != nil {
		return err
	}
	prog := &ieee20305.DERProgram{Primacy: 1}
	prog.MRID = ieee20305.MRIDType{0x02}
	desc := ieee20305.String32("Example program")
	prog.Description = &desc
	program, err := a.Create(ctx, "/derp", prog, "")
	if err != nil {
		return err
	}
	if err := a.AddMember(ctx, fsa+"/derp", program); err != nil {
		return err
	}
	now := a.Now()
	replyTo, flags := rs+"/rsp", ieee20305.HexBinary8{0x03}
	ctl := &ieee20305.DERControl{DERControlBase: ieee20305.DERControlBase{
		OpModMaxLimW: &ieee20305.PerCentControlType{Value: 5000},
	}}
	ctl.MRID = ieee20305.MRIDType{0x03}
	ctl.CreationTime = ieee20305.TimeType(now.Unix())
	ctl.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix()), Duration: 3600}
	ctl.ReplyTo, ctl.ResponseRequired = &replyTo, &flags
	_, err = a.Create(ctx, program+"/derc", ctl, "")
	return err
}

// ensurePKI writes a test CA, a server certificate for 127.0.0.1 and
// localhost, and a device certificate to dir, unless it holds them.
func ensurePKI(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ca, caKey, err := security.TestCA("example CA")
	if err != nil {
		return err
	}
	srvCert, srvKey, err := security.TestServerCert(ca, caKey, []string{"127.0.0.1", "localhost"})
	if err != nil {
		return err
	}
	devCert, devKey, err := security.TestDeviceCert(ca, caKey, "example device")
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		cert *x509.Certificate
		key  *ecdsa.PrivateKey
	}{{"ca", ca, nil}, {"server", srvCert, srvKey}, {"device", devCert, devKey}} {
		if err := writePEM(filepath.Join(dir, f.name+".crt"), "CERTIFICATE", f.cert.Raw); err != nil {
			return err
		}
		if f.key == nil {
			continue
		}
		der, err := x509.MarshalPKCS8PrivateKey(f.key)
		if err != nil {
			return err
		}
		if err := writePEM(filepath.Join(dir, f.name+".key"), "PRIVATE KEY", der); err != nil {
			return err
		}
	}
	fmt.Println("wrote a test PKI to", dir)
	return nil
}

func writePEM(path, kind string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600)
}

func loadCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(data)
	if b == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	return x509.ParseCertificate(b.Bytes)
}

func loadPool(path string) (*x509.CertPool, error) {
	c, err := loadCert(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return pool, nil
}
