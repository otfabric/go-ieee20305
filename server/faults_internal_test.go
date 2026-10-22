// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// errFault is the error a faultStore injects.
var errFault = errors.New("injected store fault")

// faultStore is a Store that fails its n-th call once armed. It hides the
// optional interfaces of the store it wraps (memberCounter, memberFilter),
// so the server takes the paths of a plain Store.
type faultStore struct {
	Store

	mu     sync.Mutex
	calls  int
	failAt int // 0: never
	hit    bool
}

func newFaultStore(failAt int) *faultStore {
	return &faultStore{Store: NewMemStore(nil), failAt: failAt}
}

// arm makes the n-th call from now fail.
func (f *faultStore) arm(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.failAt, f.hit = 0, n, false
}

func (f *faultStore) fault() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt == 0 {
		return nil
	}
	f.calls++
	if f.calls == f.failAt {
		f.hit = true
		return errFault
	}
	return nil
}

func (f *faultStore) wasHit() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hit
}

func (f *faultStore) Get(ctx context.Context, href string) (*Node, error) {
	if err := f.fault(); err != nil {
		return nil, err
	}
	return f.Store.Get(ctx, href)
}

func (f *faultStore) Put(ctx context.Context, n *Node) error {
	if err := f.fault(); err != nil {
		return err
	}
	return f.Store.Put(ctx, n)
}

func (f *faultStore) Delete(ctx context.Context, href string) error {
	if err := f.fault(); err != nil {
		return err
	}
	return f.Store.Delete(ctx, href)
}

func (f *faultStore) AddMember(ctx context.Context, list, member string) error {
	if err := f.fault(); err != nil {
		return err
	}
	return f.Store.AddMember(ctx, list, member)
}

func (f *faultStore) RemoveMember(ctx context.Context, list, member string) error {
	if err := f.fault(); err != nil {
		return err
	}
	return f.Store.RemoveMember(ctx, list, member)
}

func (f *faultStore) Members(ctx context.Context, list string) ([]*Node, error) {
	if err := f.fault(); err != nil {
		return nil, err
	}
	return f.Store.Members(ctx, list)
}

func (f *faultStore) Hrefs(ctx context.Context) ([]string, error) {
	if err := f.fault(); err != nil {
		return nil, err
	}
	return f.Store.Hrefs(ctx)
}

// sweep runs op on a fresh server (made by setup, on a faultStore) once for
// each store call op makes, failing that call: every error path of op runs,
// and must neither panic nor report success when its store failed (unless
// op tolerates the fault, as a trim does). It stops at the first run whose
// fault was not reached, which must succeed.
func sweep(t *testing.T, name string, cfg Config, setup func(t *testing.T, s *Server), op func(s *Server) error) {
	t.Helper()
	for n := 1; ; n++ {
		if n > 500 {
			t.Fatalf("%s: over 500 store calls", name)
		}
		f := newFaultStore(0)
		c := cfg
		c.Store = f
		s, err := New(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		setup(t, s)
		f.arm(n)
		err = op(s)
		hit := f.wasHit()
		f.arm(0)
		_ = s.Close()
		if !hit {
			if err != nil {
				t.Errorf("%s without a fault: %v", name, err)
			}
			return
		}
	}
}

var testLFDI = security.LFDI{0x11, 0x22, 0x33}

func devicePrincipal() Principal {
	return Principal{LFDI: testLFDI, SFDI: testLFDI.SFDI(), Edition: ieee20305.Edition2023}
}

func fullMirrorReading() *ieee20305.MirrorMeterReading {
	mmr := examples.MirrorMeterReading(0)
	mmr.MRID = ieee20305.MRIDType{1, 2, 3}
	set := examples.MirrorReadingSet(0)
	set.MRID = ieee20305.MRIDType{4, 5, 6}
	set.Reading = []ieee20305.Reading{*examples.Reading(0), *examples.Reading(0)}
	mmr.MirrorReadingSet = []ieee20305.MirrorReadingSet{*set}
	mmr.ReadingType, mmr.Reading = examples.ReadingType(0), examples.Reading(0)
	return mmr
}

func mirror() *ieee20305.MirrorUsagePoint {
	mup := examples.MirrorUsagePoint(0)
	mup.MRID = ieee20305.MRIDType{9}
	mup.DeviceLFDI = testLFDI[:]
	mup.MirrorMeterReading = []ieee20305.MirrorMeterReading{*fullMirrorReading()}
	return mup
}

func mustRoot(t *testing.T, s *Server, resource, href string, v any) {
	t.Helper()
	if err := s.Admin().Root(context.Background(), resource, href, v); err != nil {
		t.Fatal(err)
	}
}

// postMirror posts the mirror as the test device, as a POST to /mup does.
func postMirror(s *Server) error {
	ctx := context.Background()
	list, err := s.store.Get(ctx, "/mup")
	if err != nil {
		return err
	}
	_, err = s.createMirror(ctx, &Request{Principal: devicePrincipal(), Node: list, Doc: mirror()})
	return err
}

// Every store failure of the mirroring of metering fails the POST.
func TestMirrorStoreFaults(t *testing.T) {
	ctx := context.Background()
	mup := func(t *testing.T, s *Server) {
		mustRoot(t, s, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{})
	}
	sweep(t, "first mirror", Config{}, mup, postMirror)
	sweep(t, "mirror again", Config{}, func(t *testing.T, s *Server) {
		mup(t, s)
		if err := postMirror(s); err != nil {
			t.Fatal(err)
		}
	}, postMirror)
	readings := func(s *Server) error {
		n, err := s.store.Get(ctx, "/mup/1")
		if err != nil {
			return err
		}
		mmr := fullMirrorReading()
		mmr.MRID = ieee20305.MRIDType{7}
		list := &ieee20305.MirrorMeterReadingList{MirrorMeterReading: []ieee20305.MirrorMeterReading{*mmr, *fullMirrorReading()}}
		_, err = s.postMirrorReadings(ctx, &Request{Principal: devicePrincipal(), Node: n, Doc: list})
		return err
	}
	sweep(t, "readings", Config{}, func(t *testing.T, s *Server) {
		mup(t, s)
		if err := postMirror(s); err != nil {
			t.Fatal(err)
		}
	}, readings)
	// A mirror the operator made, without a UsagePoint: the first reading
	// makes one.
	sweep(t, "readings of an operator's mirror", Config{}, func(t *testing.T, s *Server) {
		mup(t, s)
		m := mirror()
		m.MirrorMeterReading = nil
		if _, err := s.Admin().Create(ctx, "/mup", m, testLFDI.String()); err != nil {
			t.Fatal(err)
		}
	}, readings)
}

// Every store failure of a flow reservation and of a Response fails the
// POST.
func TestFlowAndResponseStoreFaults(t *testing.T) {
	ctx := context.Background()
	device := func(t *testing.T, s *Server) {
		if _, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI}); err != nil {
			t.Fatal(err)
		}
	}
	sweep(t, "flow reservation", Config{}, device, func(s *Server) error {
		list, err := s.store.Get(ctx, "/edev/1/frq")
		if err != nil {
			return err
		}
		_, err = s.createFlowReservation(ctx, &Request{Principal: devicePrincipal(), Node: list, Doc: examples.FlowReservationRequest(0)})
		return err
	})
	sweep(t, "response", Config{MaxListItems: 1}, func(t *testing.T, s *Server) {
		mustRoot(t, s, "ResponseSetList", "/rsps", &ieee20305.ResponseSetList{})
		if _, err := s.Admin().Create(ctx, "/rsps", &ieee20305.ResponseSet{}, ""); err != nil {
			t.Fatal(err)
		}
		r := examples.Response(0)
		r.EndDeviceLFDI = testLFDI[:]
		r.Subject = ieee20305.MRIDType{1}
		if _, err := s.Admin().Create(ctx, "/rsps/1/rsp", r, testLFDI.String()); err != nil {
			t.Fatal(err)
		}
	}, func(s *Server) error {
		list, err := s.store.Get(ctx, "/rsps/1/rsp")
		if err != nil {
			return err
		}
		r := examples.Response(0)
		r.EndDeviceLFDI = testLFDI[:]
		r.Subject = ieee20305.MRIDType{2}
		_, err = s.createResponse(ctx, &Request{Principal: devicePrincipal(), Node: list, Doc: r})
		return err
	})
}

// Every store failure of the provisioning and registration of a device
// fails it; the bootstrap of a server fails New.
func TestDeviceAndBootstrapStoreFaults(t *testing.T) {
	ctx := context.Background()
	none := func(*testing.T, *Server) {}
	sweep(t, "provision", Config{}, none, func(s *Server) error {
		_, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI, Register: true, PIN: 1})
		return err
	})
	sweep(t, "remove", Config{}, func(t *testing.T, s *Server) {
		if _, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI}); err != nil {
			t.Fatal(err)
		}
	}, func(s *Server) error { return s.Admin().RemoveDevice(ctx, testLFDI) })
	sweep(t, "new list", Config{}, none, func(s *Server) error {
		return s.Admin().NewList(ctx, "DERProgramList", "/derp", "/dcap", "DERProgramListLink", "")
	})
	// A store that holds a device and a subscription, as after a restart.
	for n := 1; ; n++ {
		f := newFaultStore(0)
		s, err := New(Config{Store: f, InBandRegistration: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI}); err != nil {
			t.Fatal(err)
		}
		sub := &ieee20305.Subscription{NotificationURI: "https://device.example/n"}
		sub.SubscribedResource = "/edev/1"
		if _, err := s.Admin().Create(ctx, "/edev/1/sub", sub, testLFDI.String()); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		f.arm(n)
		again, err := New(Config{Store: f, InBandRegistration: true})
		hit := f.wasHit()
		if err == nil {
			_ = again.Close()
		}
		if !hit {
			if err != nil {
				t.Errorf("New without a fault: %v", err)
			}
			break
		}
		if n > 500 {
			t.Fatal("over 500 store calls")
		}
	}
	// A fresh store: the bootstrap creates /dcap, /tm and /edev.
	for n := 1; ; n++ {
		f := newFaultStore(n)
		s, err := New(Config{Store: f, InBandRegistration: true})
		hit := f.wasHit()
		if err == nil {
			_ = s.Close()
		}
		if !hit {
			if err != nil {
				t.Errorf("New without a fault: %v", err)
			}
			break
		}
	}
}

// getFailing is a store whose Get of one href fails once.
type getFailing struct {
	Store
	href string
	once sync.Once
}

func (g *getFailing) Get(ctx context.Context, href string) (*Node, error) {
	fail := false
	if href == g.href {
		g.once.Do(func() { fail = true })
	}
	if fail {
		return nil, errFault
	}
	return g.Store.Get(ctx, href)
}

// A store error in the bootstrap fails New. It does not: bootstrap creates
// /dcap, /tm and /edev only when their Get fails with ErrNoNode, and goes on
// without them on any other error, so New returns a server without Time
// (GET /tm answers 404) and without an error.
func TestBootstrapGetError(t *testing.T) {
	s, err := New(Config{Store: &getFailing{Store: NewMemStore(nil), href: "/tm"}})
	if err == nil {
		defer func() { _ = s.Close() }()
		_, gerr := s.store.Get(context.Background(), "/tm")
		t.Fatalf("New succeeded with a failing store; /tm: %v", gerr)
	}
}

// A store without the optional interfaces of the memory store: lists are
// paged and counted from Members, whose errors fail the request.
func TestPlainStore(t *testing.T) {
	ctx := context.Background()
	f := newFaultStore(0)
	s, err := New(Config{Store: f})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admin().AssignFSA(ctx, testLFDI, examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	p := devicePrincipal()
	if c := s.count(ctx, p, &Node{Href: "/edev/1/fsa", Resource: "FunctionSetAssignmentsList"}); c != 1 {
		t.Errorf("count %d, want 1", c)
	}
	list, _ := s.store.Get(ctx, "/edev")
	doc, err := s.list(ctx, &Request{Principal: p, Node: list, Query: Query{SFDI: uint64(testLFDI.SFDI()), HasSFDI: true}})
	if err != nil || doc.(*ieee20305.EndDeviceList).All != 1 {
		t.Errorf("list: %v", err)
	}
	f.arm(1)
	if c := s.count(ctx, p, &Node{Href: "/edev/1/fsa", Resource: "FunctionSetAssignmentsList"}); c != 0 {
		t.Errorf("count with a failing store %d", c)
	}
	f.arm(1)
	if _, err := s.list(ctx, &Request{Principal: p, Node: list}); !errors.Is(err, errFault) {
		t.Errorf("list with a failing store: %v", err)
	}
	f.arm(1)
	if _, err := s.getCurrentDERProgram(ctx, &Request{Principal: p, Node: &Node{Href: "/edev/1/der/1/cdp"}}); !errors.Is(err, errFault) {
		t.Errorf("CurrentDERProgram with a failing store: %v", err)
	}
	// The store fails the Get of a request, and the Delete of a DELETE.
	f.arm(1)
	op := Principal{Role: RoleOperator}
	if err := s.serve(ctx, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dcap", nil), op); !errors.Is(err, errFault) {
		t.Errorf("GET with a failing store: %v", err)
	}
	if err := s.Admin().Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admin().Create(ctx, "/derp", &ieee20305.DERProgram{}, ""); err != nil {
		t.Fatal(err)
	}
	f.arm(2)
	if err := s.serve(ctx, httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/derp/1", nil), op); !errors.Is(err, errFault) {
		t.Errorf("DELETE with a failing store: %v", err)
	}
	f.arm(1)
	if s.notifier.matches(ctx, "/edev", true, "/elsewhere/1") {
		t.Error("a failing store matches")
	}
	f.arm(1)
	if _, err := s.notifier.read(ctx, p, &subscription{resource: "/edev/1"}); !errors.Is(err, errFault) {
		t.Errorf("read with a failing store: %v", err)
	}
	f.arm(0)
}
