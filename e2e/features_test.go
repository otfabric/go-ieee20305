// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/agent"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/discovery"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// bus is an in-memory multicast network for discovery: every packet reaches
// every endpoint.
type bus struct {
	mu    sync.Mutex
	nodes []*busNode
}

type busNode struct {
	bus     *bus
	addr    netip.AddrPort
	packets chan []byte
	done    chan struct{}
	once    sync.Once
}

func (b *bus) join(last byte) *busNode {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := &busNode{bus: b, addr: netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, last}), discovery.Port),
		packets: make(chan []byte, 64), done: make(chan struct{})}
	b.nodes = append(b.nodes, n)
	return n
}

func (n *busNode) ReadPacket() ([]byte, netip.AddrPort, error) {
	select {
	case p := <-n.packets:
		return p, netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, 99}), discovery.Port), nil
	case <-n.done:
		return nil, netip.AddrPort{}, net.ErrClosed
	}
}

func (n *busNode) WritePacket(data []byte, _ netip.AddrPort) error {
	n.bus.mu.Lock()
	defer n.bus.mu.Unlock()
	for _, o := range n.bus.nodes {
		select {
		case o.packets <- append([]byte(nil), data...):
		default:
		}
	}
	return nil
}

func (n *busNode) Close() error {
	n.once.Do(func() { close(n.done) })
	return nil
}

// A device finds the server with DNS-SD and reads its DeviceCapability.
func TestDiscoveryToClient(t *testing.T) {
	env := newEnv(t, server.Config{})
	u, err := url.Parse(env.url)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b := &bus{}
	ad, err := discovery.Advertise(ctx, discovery.Service{
		Instance: "utility server", Host: "server.local", Port: port,
		Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		TXT:   map[string]string{discovery.KeyDcap: "/dcap", discovery.KeyHTTPS: u.Port()},
	}, discovery.AdvertiseOptions{Transport: b.join(1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ad.Close() }()
	found, err := discovery.Browse(ctx, discovery.BrowseOptions{Transport: b.join(2)})
	if err != nil {
		t.Fatal(err)
	}
	var svc discovery.Service
	select {
	case svc = <-found:
	case <-ctx.Done():
		t.Fatal("the server was not found")
	}
	dcap, err := svc.DcapURL()
	if err != nil {
		t.Fatal(err)
	}
	// The record names the host; the device reaches it at its address.
	du, err := url.Parse(dcap)
	if err != nil {
		t.Fatal(err)
	}
	server := "https://" + net.JoinHostPort(svc.Addrs[0].String(), du.Port())
	c, err := client.New(client.Config{Server: server, DeviceCapabilityPath: du.Path,
		HTTPClient: client.NewHTTPClient(env.pki.device(t, "device-1"), env.pki.roots)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeviceCapability(ctx); err != nil {
		t.Fatal(err)
	}
}

// notifyingAgent returns an agent whose NotificationHandler is served at the
// NotificationURI of its configuration: the listener comes first, for its
// URL, and passes the notifications on once the agent exists.
func notifyingAgent(t *testing.T, env *env, name string, cfg agent.Config) (*agent.Agent, security.LFDI) {
	t.Helper()
	var target atomic.Pointer[http.Handler]
	url := env.listen(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := target.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	cfg.NotificationURI = url + "/ntfy"
	a, lfdi := newAgent(t, env, name, cfg)
	h := a.NotificationHandler()
	target.Store(&h)
	return a, lfdi
}

// An agent with a NotificationURI subscribes to the lists it polls and
// learns of a new control by notification, long before its next poll.
func TestAgentNotificationsServed(t *testing.T) {
	d := newDERSite(t)
	a, lfdi := notifyingAgent(t, d.env, "device-1", agent.Config{PollRate: time.Hour})
	d.device(t, lfdi)
	prog := d.program(t, 1)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	stopped := false
	defer func() {
		if !stopped {
			stop()
			<-done // nolint:errcheck // stopped: its error is ctx.Err()
		}
	}()
	ed := d.admin.EndDevice(lfdi)
	// Subscribed to its FSAs, its programs, their controls and its flow
	// reservation responses; then the poll that follows the subscriptions.
	eventually(t, "the subscriptions", func() bool {
		ms, err := d.admin.Members(context.Background(), ed+"/sub")
		return err == nil && len(ms) == 4
	})
	var polls int
	eventually(t, "the polls to settle", func() bool {
		p := a.State().Polls
		time.Sleep(50 * time.Millisecond)
		polls = a.State().Polls
		return p == polls && polls >= 2
	})
	// A new control: notified, polled, active; no poll would come within
	// the hour.
	d.control(t, prog, 1, time.Now(), 3600)
	eventually(t, "the control by notification", func() bool { return len(a.Active(agent.DER)) == 1 })
	if a.State().Polls <= polls {
		t.Error("no poll after the notification")
	}
	// Stopped, the agent deletes its subscriptions.
	stop()
	stopped = true
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v", err)
	}
	if ms, err := d.admin.Members(context.Background(), ed+"/sub"); err != nil || len(ms) != 0 {
		t.Errorf("%d subscriptions after the agent stopped, %v", len(ms), err)
	}
}

func TestAgentWithoutSubscriptions(t *testing.T) {
	// A server that takes no subscriptions: the agent polls as usual.
	d := &derSite{statuses: map[string][]uint8{}}
	d.env = newEnv(t, server.Config{Disable: map[string][]string{"SubscriptionList": {"POST"}}})
	d.admin = d.env.srv.Admin()
	if err := d.admin.Root(context.Background(), "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	if err := d.admin.Root(context.Background(), "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{}); err != nil {
		t.Fatal(err)
	}
	set, err := d.admin.Create(context.Background(), "/rsps", examples.ResponseSet(0), "")
	if err != nil {
		t.Fatal(err)
	}
	d.responses = set + "/rsp"
	a, lfdi := notifyingAgent(t, d.env, "device-1", agent.Config{})
	d.device(t, lfdi)
	prog := d.program(t, 1)
	d.control(t, prog, 1, time.Now(), 3600)
	run(t, a)
	eventually(t, "the control by polling", func() bool { return len(a.Active(agent.DER)) == 1 })
}

// SelfDevice: the server's own device, linked from DeviceCapability.
func TestSelfDevice(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	self := &ieee20305.SelfDevice{}
	self.SFDI = 123456789012
	if err := env.srv.Admin().Root(ctx, "SelfDevice", "/sdev", self); err != nil {
		t.Fatal(err)
	}
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	dc, err := c.DeviceCapability(ctx)
	if err != nil || dc.SelfDeviceLink == nil {
		t.Fatalf("DeviceCapability: %+v, %v", dc, err)
	}
	var got ieee20305.SelfDevice
	if err := c.Get(ctx, dc.SelfDeviceLink.Href, &got); err != nil || got.SFDI != self.SFDI {
		t.Errorf("SelfDevice: %+v, %v", got, err)
	}
}

// An aggregator registers the devices it manages, and acts for them.
func TestAggregatorClient(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	agg := env.pki.device(t, "aggregator")
	if _, err := a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: security.CertificateLFDI(agg.Leaf), Role: server.RoleAggregator}); err != nil {
		t.Fatal(err)
	}
	if err := a.Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}); err != nil {
		t.Fatal(err)
	}
	c := env.client(t, agg, nil)
	for _, name := range []string{"inverter-1", "inverter-2"} {
		dev := env.pki.device(t, name)
		id := client.IdentityOf(dev.Leaf)
		ed, created, err := c.Register(ctx, "/edev", id)
		if err != nil || !created {
			t.Fatalf("%s: Register %v, %v", name, created, err)
		}
		if _, err := c.Post(ctx, ed.LogEventListLink.Href, examples.LogEvent(0)); err != nil {
			t.Errorf("%s: a LogEvent by the aggregator: %v", name, err)
		}
		if err := c.Put(ctx, ed.DeviceStatusLink.Href, examples.DeviceStatus(0)); err != nil {
			t.Errorf("%s: a DeviceStatus by the aggregator: %v", name, err)
		}
		// The device reads what its aggregator wrote.
		own := env.client(t, dev, nil)
		var l ieee20305.LogEventList
		if err := own.GetList(ctx, ed.LogEventListLink.Href, client.ListOptions{Limit: 5}, &l); err != nil || l.All != 1 {
			t.Errorf("%s: its LogEvents: %d, %v", name, l.All, err)
		}
	}
	var all ieee20305.EndDeviceList
	if err := c.GetList(ctx, "/edev", client.ListOptions{Limit: 10}, &all); err != nil || all.All != 3 { // its own and the two
		t.Errorf("the aggregator's EndDevices: %d, %v", all.All, err)
	}
}

// What a device reports of itself: LoadShedAvailability, DeviceInformation,
// DeviceStatus, PowerStatus and Configuration round trips, and its
// LogEvents paged newest first.
func TestDeviceResources(t *testing.T) {
	for _, edition := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		t.Run(edition.String(), func(t *testing.T) {
			env := newEnv(t, server.Config{})
			ctx := context.Background()
			c, ed, _ := env.provisioned(t, "device-1", edition)
			var doc ieee20305.EndDevice
			if err := c.Get(ctx, ed, &doc); err != nil {
				t.Fatal(err)
			}
			info := examples.DeviceInformation(0)
			info.MfModel = "model-x"
			status := examples.DeviceStatus(0)
			power := examples.PowerStatus(0)
			power.BatteryStatus = 2
			cfg := examples.Configuration(0)
			cfg.UserDeviceName = "garage"
			for href, v := range map[string]any{
				doc.DeviceInformationLink.Href: info, doc.DeviceStatusLink.Href: status,
				doc.PowerStatusLink.Href: power, doc.ConfigurationLink.Href: cfg,
			} {
				if err := c.Put(ctx, href, v); err != nil {
					t.Errorf("PUT %s: %v", href, err)
				}
			}
			var gotInfo ieee20305.DeviceInformation
			var gotPower ieee20305.PowerStatus
			var gotCfg ieee20305.Configuration
			if err := c.Get(ctx, doc.DeviceInformationLink.Href, &gotInfo); err != nil || gotInfo.MfModel != "model-x" {
				t.Errorf("DeviceInformation %+v, %v", gotInfo, err)
			}
			if err := c.Get(ctx, doc.PowerStatusLink.Href, &gotPower); err != nil || gotPower.BatteryStatus != 2 {
				t.Errorf("PowerStatus %+v, %v", gotPower, err)
			}
			if err := c.Get(ctx, doc.ConfigurationLink.Href, &gotCfg); err != nil || gotCfg.UserDeviceName != "garage" {
				t.Errorf("Configuration %+v, %v", gotCfg, err)
			}
			// LoadShedAvailability: posted, read, changed.
			lsa := examples.LoadShedAvailability(0)
			loc, err := c.Post(ctx, doc.LoadShedAvailabilityListLink.Href, lsa)
			if err != nil {
				t.Fatal(err)
			}
			pct := ieee20305.PerCent(5000)
			lsa.SheddablePercent = &pct
			if err := c.Put(ctx, loc, lsa); err != nil {
				t.Error(err)
			}
			var gotLSA ieee20305.LoadShedAvailability
			if err := c.Get(ctx, loc, &gotLSA); err != nil || gotLSA.SheddablePercent == nil || *gotLSA.SheddablePercent != 5000 {
				t.Errorf("LoadShedAvailability %+v, %v", gotLSA, err)
			}
			// LogEvents, newest first, by pages of 3.
			for i := 1; i <= 7; i++ {
				ev := examples.LogEvent(0)
				ev.CreatedDateTime = ieee20305.TimeType(1_700_000_000 + i)
				ev.LogEventID = ieee20305.UInt16(i)
				if _, err := c.Post(ctx, doc.LogEventListLink.Href, ev); err != nil {
					t.Fatal(err)
				}
			}
			var ids []int
			if err := c.Iterate(ctx, doc.LogEventListLink.Href, 3, func(it any) error {
				ids = append(ids, int(it.(*ieee20305.LogEvent).LogEventID))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(ids) != 7 || ids[0] != 7 || ids[6] != 1 {
				t.Errorf("LogEvents %v", ids)
			}
		})
	}
}

// A DERControl that refers to a DERCurve: the device follows the link.
func TestDERCurves(t *testing.T) {
	d := newDERSite(t)
	dev := d.env.pki.device(t, "device-1")
	d.device(t, security.CertificateLFDI(dev.Leaf))
	prog := d.program(t, 1)
	ctx := context.Background()
	curve := examples.DERCurve(0)
	curve.CurveData = []ieee20305.CurveData{{Xvalue: 90, Yvalue: 50}, {Xvalue: 110, Yvalue: -50}}
	ch, err := d.admin.Create(ctx, prog+"/dc", curve, "")
	if err != nil {
		t.Fatal(err)
	}
	ctl := d.control(t, prog, 1, time.Now(), 3600)
	n, err := d.admin.Get(ctx, ctl)
	if err != nil {
		t.Fatal(err)
	}
	c := n.Value.(*ieee20305.DERControl)
	c.DERControlBase.OpModVoltVar = &ieee20305.DERCurveLink{Link: ieee20305.Link{Href: ch}}
	if err := d.admin.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	cl := d.env.client(t, dev, nil)
	var got ieee20305.DERControl
	if err := cl.Get(ctx, ctl, &got); err != nil || got.DERControlBase.OpModVoltVar == nil {
		t.Fatalf("DERControl %+v, %v", got, err)
	}
	var gotCurve ieee20305.DERCurve
	if err := cl.Get(ctx, got.DERControlBase.OpModVoltVar.Href, &gotCurve); err != nil || len(gotCurve.CurveData) != 2 {
		t.Errorf("DERCurve %+v, %v", gotCurve, err)
	}
	var curves ieee20305.DERCurveList
	if err := cl.GetList(ctx, prog+"/dc", client.ListOptions{Limit: 5}, &curves); err != nil || curves.All != 1 {
		t.Errorf("DERCurveList %d, %v", curves.All, err)
	}
}

// Prepayment: the account balance, credit registers and operation status
// of a Prepayment, read by its device.
func TestPrepaymentReads(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	a := env.srv.Admin()
	if err := a.Root(ctx, "PrepaymentList", "/ppy", &ieee20305.PrepaymentList{}); err != nil {
		t.Fatal(err)
	}
	ppy, err := a.Create(ctx, "/ppy", examples.Prepayment(0), "")
	if err != nil {
		t.Fatal(err)
	}
	balance := examples.AccountBalance(0)
	balance.AvailableCredit = ieee20305.AccountingUnit{Value: 1234}
	if err := a.Put(ctx, &server.Node{Href: ppy + "/ab", Resource: "AccountBalance", Value: balance}); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, &server.Node{Href: ppy + "/os", Resource: "PrepayOperationStatus", Value: examples.PrepayOperationStatus(0)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := a.Create(ctx, ppy+"/cr", examples.CreditRegister(0), ""); err != nil {
			t.Fatal(err)
		}
	}
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	var p ieee20305.Prepayment
	if err := c.Get(ctx, ppy, &p); err != nil {
		t.Fatal(err)
	}
	var gotBalance ieee20305.AccountBalance
	if err := c.Get(ctx, p.AccountBalanceLink.Href, &gotBalance); err != nil || gotBalance.AvailableCredit.Value != 1234 {
		t.Errorf("AccountBalance %+v, %v", gotBalance, err)
	}
	var os ieee20305.PrepayOperationStatus
	if err := c.Get(ctx, p.PrepayOperationStatusLink.Href, &os); err != nil {
		t.Errorf("PrepayOperationStatus: %v", err)
	}
	var crs ieee20305.CreditRegisterList
	if err := c.GetList(ctx, p.CreditRegisterListLink.Href, client.ListOptions{Limit: 5}, &crs); err != nil || crs.All != 2 {
		t.Errorf("CreditRegisterList %d, %v", crs.All, err)
	}
	if !strings.HasPrefix(p.ActiveCreditRegisterListLink.Href, ppy) {
		t.Errorf("ActiveCreditRegisterListLink %q", p.ActiveCreditRegisterListLink.Href)
	}
}
