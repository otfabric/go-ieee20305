// SPDX-License-Identifier: MIT

// Csipaus runs a CSIP-AUS server and device in one process: the server, in
// the profile of csipaus/csipserver, publishes default site limits and a
// dynamic operating envelope that starts now; the device, an agent with the
// csipaus codec, reports its connection point and reads both.
//
//	go run ./examples/csipaus
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
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/csipaus/csipclient"
	"github.com/otfabric/go-ieee20305/csipaus/csipserver"
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
	ca, caKey, err := security.TestCA("csipaus example CA")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverCert, serverKey, err := security.TestServerCert(ca, caKey, []string{"127.0.0.1"})
	if err != nil {
		return err
	}
	deviceCert, deviceKey, err := security.TestDeviceCert(ca, caKey, "site-1")
	if err != nil {
		return err
	}
	device := tls.Certificate{Certificate: [][]byte{deviceCert.Raw}, PrivateKey: deviceKey, Leaf: deviceCert}
	lfdi := security.CertificateLFDI(deviceCert)

	// The CSIP-AUS profile: the documents of csipaus, IEEE 2030.5-2018, and
	// a ConnectionPoint for each EndDevice.
	srv, err := server.New(csipserver.Configure(server.Config{}))
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

	c, err := client.New(client.Config{Server: "https://" + l.Addr().String() + "/dcap",
		HTTPClient: client.NewHTTPClient(device, roots), Codec: csipaus.Codec{}})
	if err != nil {
		return err
	}
	active := make(chan struct{}, 1)
	a, err := agent.New(agent.Config{
		Client:   c,
		Identity: client.IdentityOf(deviceCert),
		OnDefaultDERControl: func(_ context.Context, d any) error {
			if lim, ok := csipaus.LimitsOf(d); ok {
				fmt.Println("default limits:", describe(lim))
			}
			return nil
		},
		OnDERControl: func(_ context.Context, t schedule.Transition) error {
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

	// The device reports the NMI of its connection point.
	ed, err := csipclient.EndDevice(ctx, c, srv.Admin().EndDevice(lfdi))
	if err != nil {
		return err
	}
	if err := csipclient.SetConnectionPoint(ctx, c, ed, "4407123456"); err != nil {
		return err
	}
	cp, err := csipclient.ConnectionPoint(ctx, c, ed)
	if err != nil {
		return err
	}
	fmt.Println("connection point:", cp.ConnectionPointId)

	select {
	case <-active:
	case <-ctx.Done():
		return errors.New("the envelope did not become active")
	}
	for _, ctl := range a.Active(agent.DER) {
		if lim, ok := csipaus.LimitsOf(ctl); ok {
			fmt.Println("active envelope:", describe(lim))
		}
	}
	return nil
}

// seed provisions the device, and assigns it a program with default
// limits and an envelope for the next 30 minutes.
func seed(ctx context.Context, a *server.Admin, lfdi security.LFDI) error {
	if err := a.Root(ctx, "DERProgramList", "/derp", a.Document("DERProgramList")); err != nil {
		return err
	}
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Register: true}); err != nil {
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
	// 5 kW export and import when no envelope applies.
	if err := csipserver.SetDefaultLimits(ctx, a, program, csipaus.Limits{
		Export: csipserver.Watts(5000), Import: csipserver.Watts(5000),
	}); err != nil {
		return err
	}
	// 1.5 kW export for the next 30 minutes.
	env := csipserver.Envelope(ieee20305.MRIDType{0x03}, a.Now(), 30*time.Minute, csipaus.Limits{Export: csipserver.Watts(1500)})
	_, err = a.Create(ctx, program+"/derc", env, "")
	return err
}

func describe(l csipaus.Limits) string {
	w := func(p *ieee20305.ActivePower) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprintf("%d W", p.Value)
	}
	return fmt.Sprintf("export %s, import %s, generation %s, load %s", w(l.Export), w(l.Import), w(l.Generation), w(l.Load))
}
