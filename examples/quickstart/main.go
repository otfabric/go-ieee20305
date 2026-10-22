// SPDX-License-Identifier: MIT

// Quickstart runs an IEEE 2030.5 server and a device agent in one process,
// over mutual TLS on the loopback interface. The server provisions the
// device and publishes a DER program with a control that starts now; the
// agent registers, finds the control, schedules it, and answers the server
// with the Responses it asks for.
//
//	go run ./examples/quickstart
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A throwaway PKI: a CA, the server's certificate and a device's.
	ca, caKey, err := security.TestCA("quickstart CA")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverCert, serverKey, err := security.TestServerCert(ca, caKey, []string{"127.0.0.1"})
	if err != nil {
		return err
	}
	deviceCert, deviceKey, err := security.TestDeviceCert(ca, caKey, "inverter-1")
	if err != nil {
		return err
	}
	device := tls.Certificate{Certificate: [][]byte{deviceCert.Raw}, PrivateKey: deviceKey, Leaf: deviceCert}
	lfdi := security.CertificateLFDI(deviceCert)
	fmt.Printf("device LFDI %s, SFDI %s\n", lfdi, lfdi.SFDI())

	// The server, told of the responses devices post.
	srv, err := server.New(server.Config{
		OnResponse: func(_ context.Context, _ string, r any) {
			if resp, ok := r.(*ieee20305.DERControlResponse); ok && resp.Status != nil {
				fmt.Printf("server: response %d to control %X\n", *resp.Status, []byte(resp.Subject))
			}
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()
	if err := seed(ctx, srv.Admin(), lfdi); err != nil {
		return err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: security.ServerTLSConfig(tls.Certificate{Certificate: [][]byte{serverCert.Raw}, PrivateKey: serverKey}, roots)}
	go func() { _ = hs.ServeTLS(l, "", "") }()
	defer func() { _ = hs.Close() }()
	url := "https://" + l.Addr().String() + "/dcap"
	fmt.Println("server listening at", url)

	// The device: a client with its certificate, and an agent on it.
	c, err := client.New(client.Config{Server: url, HTTPClient: client.NewHTTPClient(device, roots)})
	if err != nil {
		return err
	}
	active := make(chan struct{}, 1)
	a, err := agent.New(agent.Config{
		Client:    c,
		Identity:  client.IdentityOf(deviceCert),
		PIN:       111115,
		VerifyPIN: true,
		OnDERControl: func(_ context.Context, t schedule.Transition) error {
			fmt.Printf("agent: control %s is %v\n", t.Event.MRID, t.To)
			if t.To == schedule.Active {
				select {
				case active <- struct{}{}:
				default:
				}
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Start(ctx); err != nil {
		return err
	}
	select {
	case <-active:
	case <-ctx.Done():
		return errors.New("the control did not become active")
	}
	// Let the agent post its responses before the program ends.
	time.Sleep(500 * time.Millisecond)
	return nil
}

// seed provisions the device with its PIN, and assigns it a DER program
// with a control that limits its output to 50 % for an hour from now.
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
	program, err := a.Create(ctx, "/derp", prog, "")
	if err != nil {
		return err
	}
	if err := a.AddMember(ctx, fsa+"/derp", program); err != nil {
		return err
	}
	now := a.Now()
	replyTo, flags := rs+"/rsp", ieee20305.HexBinary8{0x03} // received and started
	ctl := &ieee20305.DERControl{DERControlBase: ieee20305.DERControlBase{
		OpModMaxLimW: &ieee20305.PerCentControlType{Value: 5000}, // 50.00 %
	}}
	ctl.MRID = ieee20305.MRIDType{0x03}
	ctl.CreationTime = ieee20305.TimeType(now.Unix())
	ctl.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix()), Duration: 3600}
	ctl.ReplyTo, ctl.ResponseRequired = &replyTo, &flags
	_, err = a.Create(ctx, program+"/derc", ctl, "")
	return err
}
