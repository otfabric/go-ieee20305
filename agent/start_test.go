// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

func TestStartAndState(t *testing.T) {
	f := newFake(t)
	a := newTestAgent(t, f, Config{PIN: 111115, VerifyPIN: true})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := a.State()
	if st.EndDevice.GetHref() != "/edev/1" || st.Registration == nil || st.Polls != 1 || len(st.Assignments) != 1 {
		t.Fatalf("state %+v", st)
	}
	// The State is a deep copy.
	*st.EndDevice.Href = "/changed"
	*st.Assignments[0].Href = "/changed"
	st.Registration.PIN = 1
	*st.DeviceCapability.PollRate = 1
	st.Time.Time.CurrentTime = 1
	again := a.State()
	if again.EndDevice.GetHref() != "/edev/1" || again.Assignments[0].GetHref() != "/fsa/1" ||
		again.Registration.PIN != 111115 || *again.DeviceCapability.PollRate != 60 || again.Time.Time.CurrentTime == 1 {
		t.Errorf("State shares documents with the agent: %+v", again)
	}
}

func TestStartClosed(t *testing.T) {
	a := newTestAgent(t, newFake(t), Config{})
	a.Close()
	if err := a.Start(context.Background()); !errors.Is(err, ieee20305.ErrClosed) {
		t.Fatalf("Start after Close: %v", err)
	}
}

func TestStartErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   Config
		setup func(f *fakeServer)
		is    error
		code  int
	}{
		{name: "DeviceCapability", setup: func(f *fakeServer) { f.failWith("GET /dcap", 500) }, code: 500},
		{name: "Time", setup: func(f *fakeServer) { f.failWith("GET /tm", 503) }, code: 503},
		{name: "no EndDeviceList", setup: func(f *fakeServer) {
			f.set("/dcap", `<DeviceCapability `+ns+` href="/dcap"><TimeLink href="/tm"/></DeviceCapability>`)
		}, is: ieee20305.ErrProtocol},
		{name: "Register", setup: func(f *fakeServer) { f.failWith("GET /edev", 500) }, code: 500},
		{name: "Registration", setup: func(f *fakeServer) { f.failWith("GET /edev/1/rg", 500) }, code: 500},
		{name: "PIN", cfg: Config{PIN: 1, VerifyPIN: true}, is: client.ErrPINMismatch},
		{name: "DeviceInformation", cfg: Config{DeviceInformation: &ieee20305.DeviceInformation{}}, setup: func(f *fakeServer) {
			f.setEndDevice(`<DeviceInformationLink href="/edev/1/di"/><RegistrationLink href="/edev/1/rg"/>`)
			f.failWith("PUT /edev/1/di", 500)
		}, code: 500},
		{name: "assignments", setup: func(f *fakeServer) { f.failWith("GET /edev/1/fsa", 500) }, code: 500},
		{name: "programs", setup: func(f *fakeServer) { f.failWith("GET /derp", 500) }, code: 500},
		{name: "controls", setup: func(f *fakeServer) { f.failWith("GET /derp/1/derc", 500) }, code: 500},
		{name: "files", cfg: Config{OnFile: func(context.Context, *ieee20305.File) error { return nil }},
			setup: func(f *fakeServer) { f.failWith("GET /file", 500) }, code: 500},
		{name: "DefaultDERControl", cfg: Config{OnDefaultDERControl: func(context.Context, any) error { return nil }},
			setup: func(f *fakeServer) { f.failWith("GET /derp/1/dderc", 500) }, code: 500},
		{name: "OnAssignments", cfg: Config{OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
			return ieee20305.ErrInvalid
		}}, is: ieee20305.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			a := newTestAgent(t, f, tc.cfg)
			err := a.Start(context.Background())
			if err == nil {
				t.Fatal("Start succeeded")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("error %v, want %v", err, tc.is)
			}
			var se *ieee20305.StatusError
			if tc.code != 0 && (!errors.As(err, &se) || se.StatusCode != tc.code) {
				t.Errorf("error %v, want status %d", err, tc.code)
			}
		})
	}
}

func TestStartDeviceInformation(t *testing.T) {
	f := newFake(t)
	f.setEndDevice(`<DeviceInformationLink href="/edev/1/di"/><RegistrationLink href="/edev/1/rg"/>`)
	a := newTestAgent(t, f, Config{DeviceInformation: &ieee20305.DeviceInformation{}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT /edev/1/di") != 1 {
		t.Error("the DeviceInformation was not put")
	}
}

// TestStartWaitingForRegistration has the agent read its EndDevice again
// while the server has not registered it, and start over when it is gone.
func TestStartWaitingForRegistration(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.setEndDevice("")
	a := newTestAgent(t, f, Config{})
	if err := a.Start(ctx); !errors.Is(err, client.ErrNotRegistered) {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Start(ctx); !errors.Is(err, client.ErrNotRegistered) {
		t.Fatalf("Start again: %v", err)
	}
	// Start reads it twice: itself and for the Time.
	if n := f.count("GET /dcap"); n != 2 {
		t.Errorf("DeviceCapability read %d times while waiting", n)
	}
	if n := f.count("GET /edev/1"); n != 1 {
		t.Errorf("EndDevice read %d times", n)
	}

	// A failure to read the EndDevice is returned.
	f.failWith("GET /edev/1", 500)
	var se *ieee20305.StatusError
	if err := a.Start(ctx); !errors.As(err, &se) || se.StatusCode != 500 {
		t.Fatalf("Start with the EndDevice failing: %v", err)
	}
	f.failWith("GET /edev/1", 0)

	// The EndDevice is gone: the agent registers again, and the server
	// has now registered it.
	f.setEndDevice(`<RegistrationLink href="/edev/1/rg"/>`)
	f.set("/edev/1", "")
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start after the EndDevice went: %v", err)
	}
	if n := f.count("GET /dcap"); n != 4 {
		t.Errorf("DeviceCapability read %d times, want 4", n)
	}
}

func TestRunPermanentStartError(t *testing.T) {
	f := newFake(t)
	a := newTestAgent(t, f, Config{PIN: 42, VerifyPIN: true})
	if err := a.Run(context.Background()); !errors.Is(err, client.ErrPINMismatch) {
		t.Fatalf("Run: %v", err)
	}
	// Run closed the agent.
	if err := a.Start(context.Background()); !errors.Is(err, ieee20305.ErrClosed) {
		t.Errorf("Start after Run: %v", err)
	}
}

func TestRunPermanentPollError(t *testing.T) {
	f := newFake(t)
	calls := 0
	a := newTestAgent(t, f, Config{PollRate: 1, OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
		calls++
		if calls == 3 {
			return fmt.Errorf("%w: bad", ieee20305.ErrConfiguration)
		}
		if calls == 2 {
			return errors.New("transient")
		}
		return nil
	}})
	if err := a.Run(context.Background()); !errors.Is(err, ieee20305.ErrConfiguration) {
		t.Fatalf("Run: %v", err)
	}
	if calls != 3 {
		t.Errorf("%d polls", calls)
	}
}

func TestRunRetriesStart(t *testing.T) {
	f := newFake(t)
	f.failTimes("GET /dcap", http.StatusServiceUnavailable, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newTestAgent(t, f, Config{PollRate: 1, OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
		cancel()
		return nil
	}})
	if err := a.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if a.State().Polls < 1 || f.count("GET /dcap") < 3 {
		t.Errorf("polls %d, DeviceCapability read %d times", a.State().Polls, f.count("GET /dcap"))
	}
}

func TestDeviceResources(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	a := newTestAgent(t, f, Config{})
	calls := map[string]func() error{
		"Log": func() error {
			_, err := a.Log(ctx, &ieee20305.LogEvent{})
			return err
		},
		"PutDeviceStatus": func() error { return a.PutDeviceStatus(ctx, &ieee20305.DeviceStatus{}) },
		"PutPowerStatus":  func() error { return a.PutPowerStatus(ctx, &ieee20305.PowerStatus{}) },
		"PutFileStatus":   func() error { return a.PutFileStatus(ctx, &ieee20305.FileStatus{}) },
		"RequestFlowReservation": func() error {
			_, err := a.RequestFlowReservation(ctx, &ieee20305.FlowReservationRequest{})
			return err
		},
		"Mirror": func() error {
			_, err := a.Mirror(ctx, &ieee20305.MirrorUsagePoint{})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrNotStarted) {
			t.Errorf("%s before Start: %v", name, err)
		}
	}
	// Registered without the links.
	f.set("/dcap", `<DeviceCapability `+ns+` href="/dcap"><TimeLink href="/tm"/><EndDeviceListLink href="/edev" all="1"/></DeviceCapability>`)
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("%s without its link: %v", name, err)
		}
	}
	// With them.
	f.set("/dcap", `<DeviceCapability `+ns+` href="/dcap"><TimeLink href="/tm"/><EndDeviceListLink href="/edev" all="1"/>
		<MirrorUsagePointListLink href="/mup" all="0"/></DeviceCapability>`)
	f.setEndDevice(`<DeviceStatusLink href="/edev/1/ds"/><FileStatusLink href="/edev/1/fs"/>
		<LogEventListLink href="/edev/1/lel" all="0"/><PowerStatusLink href="/edev/1/ps"/>
		<FlowReservationRequestListLink href="/edev/1/frq" all="0"/><RegistrationLink href="/edev/1/rg"/>`)
	f.location("POST /edev/1/lel", "/edev/1/lel/1")
	f.location("POST /edev/1/frq", "/edev/1/frq/1")
	f.location("POST /mup", "/mup/1")
	b := newTestAgent(t, f, Config{})
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a = b
	for name, call := range calls {
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, key := range []string{"POST /edev/1/lel", "PUT /edev/1/ds", "PUT /edev/1/ps", "PUT /edev/1/fs", "POST /edev/1/frq", "POST /mup"} {
		if f.count(key) != 1 {
			t.Errorf("%s: %d requests", key, f.count(key))
		}
	}
	// A server that sends no Location for the mirror.
	f.location("POST /mup", "")
	if _, err := a.Mirror(ctx, &ieee20305.MirrorUsagePoint{}); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Mirror without a Location: %v", err)
	}
}

func TestPollDefault(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	var got []any
	a := newTestAgent(t, f, Config{OnDefaultDERControl: func(_ context.Context, d any) error {
		got = append(got, d)
		return nil
	}})
	// No DefaultDERControl yet (404).
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("handler called with %v", got)
	}
	f.set("/derp/1/dderc", `<DefaultDERControl `+ns+` href="/derp/1/dderc"><mRID>D1</mRID>
		<DERControlBase><opModConnect>true</opModConnect></DERControlBase></DefaultDERControl>`)
	for range 2 {
		if err := a.poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 1 {
		t.Fatalf("handler called %d times, want once", len(got))
	}
	if _, ok := got[0].(*ieee20305.DefaultDERControl); !ok {
		t.Errorf("handler got a %T", got[0])
	}
}

func TestIsNotFound(t *testing.T) {
	if isNotFound(errors.New("x")) || isNotFound(&ieee20305.StatusError{StatusCode: 500}) {
		t.Error("not a 404")
	}
	if !isNotFound(fmt.Errorf("w: %w", &ieee20305.StatusError{StatusCode: 404})) {
		t.Error("a 404")
	}
}
