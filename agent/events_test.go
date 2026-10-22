// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
)

func TestFunctionSetString(t *testing.T) {
	for fs, want := range map[FunctionSet]string{
		DER: "DER", DRLC: "DRLC", Messaging: "Messaging", Pricing: "Pricing", Prepayment: "Prepayment",
		FlowReservation: "FlowReservation", functionSets: "FunctionSet(?)", FunctionSet(99): "FunctionSet(?)",
	} {
		if got := fs.String(); got != want {
			t.Errorf("%d: %q, want %q", int(fs), got, want)
		}
	}
}

func TestEventSets(t *testing.T) {
	if hrefOf(42) != "" {
		t.Error("hrefOf of a non-resource")
	}
	// Messages, prepayment overrides and flow reservations never conflict;
	// tariff intervals do within their rate component.
	for _, fs := range []FunctionSet{Messaging, Prepayment, FlowReservation} {
		if eventSets[fs].conflicts(schedule.Event{Program: "a"}, schedule.Event{Program: "a"}) {
			t.Errorf("%v events conflict", fs)
		}
	}
	if !eventSets[Pricing].conflicts(schedule.Event{Program: "a"}, schedule.Event{Program: "a"}) ||
		eventSets[Pricing].conflicts(schedule.Event{Program: "a"}, schedule.Event{Program: "b"}) {
		t.Error("Pricing conflicts")
	}
	if l, _, ok, err := eventSets[FlowReservation].events(context.Background(), nil, nil); l != nil || ok || err != nil {
		t.Error("FlowReservation has no programs")
	}
	if eventSets[Prepayment].response != nil {
		t.Error("Prepayment has no Responses")
	}
	if _, ok := eventSets[FlowReservation].response().(*ieee20305.FlowReservationResponseResponse); !ok {
		t.Error("FlowReservation Response")
	}
	var m Metrics = NopMetrics{}
	m.OnPoll(time.Second, nil)
	m.OnEvent(DER, schedule.Transition{})
	m.OnResponse(DER, 0, nil)
	if a := (&Agent{}); a.Active(-1) != nil || a.Events(functionSets) != nil {
		t.Error("Active or Events of no function set")
	}
}

// derControl is a DERControl of the fake server, starting in an hour.
func derControl(mrid, extra string) string {
	return fmt.Sprintf(`<DERControl href="/derp/1/derc/%s" %s><mRID>%s</mRID><creationTime>0</creationTime>
		<EventStatus><currentStatus>0</currentStatus><dateTime>0</dateTime><potentiallySuperseded>false</potentiallySuperseded></EventStatus>
		<interval><duration>3600</duration><start>%d</start></interval><DERControlBase/></DERControl>`,
		mrid, extra, mrid, time.Now().Add(time.Hour).Unix())
}

func derControls(items ...string) string {
	s := fmt.Sprintf(`<DERControlList `+ns+` href="/derp/1/derc" all="%d" results="%d">`, len(items), len(items))
	for _, it := range items {
		s += it
	}
	return s + `</DERControlList>`
}

// TestPollEvents has the agent read programs of every function set, some
// without events, events that are not events, duplicates and events that
// go away.
func TestPollEvents(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.set("/edev/1/fsa", `<FunctionSetAssignmentsList `+ns+` href="/edev/1/fsa" all="2" results="2">
		<FunctionSetAssignments href="/fsa/1">
			<DemandResponseProgramListLink href="/drp" all="1"/>
			<DERProgramListLink href="/derp" all="1"/>
			<MessagingProgramListLink href="/msg" all="1"/>
			<PrepaymentListLink href="/ppy" all="1"/>
			<TariffProfileListLink href="/tp" all="1"/>
			<mRID>F1</mRID>
		</FunctionSetAssignments>
		<FunctionSetAssignments href="/fsa/2">
			<DERProgramListLink href="/derp" all="1"/>
			<UsagePointListLink href="/upt" all="0"/>
			<mRID>F2</mRID>
		</FunctionSetAssignments>
	</FunctionSetAssignmentsList>`)
	f.set("/drp", `<DemandResponseProgramList `+ns+` href="/drp" all="1" results="1">
		<DemandResponseProgram href="/drp/1"><mRID>A2</mRID><primacy>1</primacy></DemandResponseProgram></DemandResponseProgramList>`)
	f.set("/msg", `<MessagingProgramList `+ns+` href="/msg" all="1" results="1">
		<MessagingProgram href="/msg/1"><mRID>A3</mRID><locale>en</locale><primacy>1</primacy></MessagingProgram></MessagingProgramList>`)
	f.set("/ppy", `<PrepaymentList `+ns+` href="/ppy" all="1" results="1">
		<Prepayment href="/ppy/1"><mRID>A4</mRID><prepayMode>0</prepayMode></Prepayment></PrepaymentList>`)
	f.set("/tp", `<TariffProfileList `+ns+` href="/tp" all="1" results="1">
		<TariffProfile href="/tp/1"><mRID>A5</mRID><primacy>1</primacy></TariffProfile></TariffProfileList>`)
	// A list of what are not events: skipped.
	f.set("/derp", `<DERProgramList `+ns+` href="/derp" all="2" results="2">
		<DERProgram href="/derp/1"><mRID>A1</mRID><DERControlListLink href="/derp/1/derc" all="2"/><primacy>2</primacy></DERProgram>
		<DERProgram href="/derp/2"><mRID>A6</mRID><DERControlListLink href="/derp/2/derc" all="1"/><primacy>1</primacy></DERProgram>
	</DERProgramList>`)
	f.set("/derp/2/derc", `<FileList `+ns+` href="/derp/2/derc" all="1" results="1"><File href="/f/1"><fileURI>x</fileURI>
		<mfID>1</mfID><mfModel>m</mfModel><mfVer>1</mfVer><size>1</size><type>0000</type></File></FileList>`)
	// The same event twice.
	f.set("/derp/1/derc", derControls(derControl("B1", ""), derControl("B1", ""), derControl("B2", "")))

	var mu sync.Mutex
	var got []schedule.Transition
	a := newTestAgent(t, f, Config{OnDERControl: func(_ context.Context, tr schedule.Transition) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, tr)
		return errors.New("the handler fails")
	}})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(a.Events(DER)); n != 2 {
		t.Fatalf("%d DER events, want 2", n)
	}
	waitFor(t, "the receipt of the events", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 2
	})
	if p, _ := a.loops[DER].best.(*ieee20305.DERProgram); p == nil || p.GetHref() != "/derp/2" {
		t.Errorf("best DERProgram %v", a.loops[DER].best)
	}

	// An event the server no longer lists goes.
	f.set("/derp/1/derc", derControls(derControl("B2", "")))
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if evs := a.Events(DER); len(evs) != 1 || evs[0].MRID != "B2" {
		t.Errorf("events %+v", evs)
	}

	// A list of programs that are not programs.
	f.set("/drp", `<FileList `+ns+` href="/drp" all="1" results="1"><File href="/f/1"><fileURI>x</fileURI>
		<mfID>1</mfID><mfModel>m</mfModel><mfVer>1</mfVer><size>1</size><type>0000</type></File></FileList>`)
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}

	// The rate components of a tariff profile fail.
	f.set("/tp", `<TariffProfileList `+ns+` href="/tp" all="1" results="1">
		<TariffProfile href="/tp/1"><mRID>A5</mRID><primacy>1</primacy><RateComponentListLink href="/tp/1/rc" all="1"/></TariffProfile></TariffProfileList>`)
	f.failWith("GET /tp/1/rc", 500)
	var se *ieee20305.StatusError
	if err := a.poll(ctx); !errors.As(err, &se) || se.StatusCode != 500 {
		t.Errorf("poll with failing rate components: %v", err)
	}
}

// recordMetrics records the Responses of an agent.
type recordMetrics struct {
	NopMetrics
	mu        sync.Mutex
	responses []error
}

func (m *recordMetrics) OnResponse(_ FunctionSet, _ uint8, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, err)
}

func TestRespond(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	m := &recordMetrics{}
	a := newTestAgent(t, f, Config{Metrics: m})
	replyTo := "/rsp"
	received := func(v any) schedule.Transition {
		return schedule.Transition{Event: schedule.Event{MRID: "B1", Value: v}, At: time.Now()}
	}
	control := func(flags ieee20305.HexBinary8) *ieee20305.DERControl {
		c := &ieee20305.DERControl{}
		c.ReplyTo, c.ResponseRequired, c.MRID = &replyTo, &flags, ieee20305.MRIDType{0xB1}
		return c
	}
	der := &eventSets[DER]

	// What asks for no Response.
	for name, v := range map[string]any{
		"nil":              (*ieee20305.DERControl)(nil),
		"not a pointer":    ieee20305.DERControl{},
		"no replyTo":       &struct{ X int }{},
		"no flags":         control(nil),
		"not on receipt":   control(ieee20305.HexBinary8{schedule.RespondSpecific}),
		"empty replyTo":    &ieee20305.DERControl{},
		"unexported field": &struct{ replyTo *string }{&replyTo},
	} {
		if err := a.respond(ctx, der, received(v)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Response documents without a Response.
	for _, doc := range []func() any{
		func() any { return &struct{ X int }{} },
		func() any { return &struct{ Response int }{} },
	} {
		set := &eventSet{fs: DER, response: doc}
		if err := a.respond(ctx, set, received(control(ieee20305.HexBinary8{schedule.RespondReceived}))); err != nil {
			t.Error(err)
		}
	}
	if n := f.count("POST /rsp"); n != 0 {
		t.Fatalf("%d Responses posted", n)
	}

	// A Response.
	if err := a.respond(ctx, der, received(control(ieee20305.HexBinary8{schedule.RespondReceived}))); err != nil {
		t.Fatal(err)
	}
	if f.count("POST /rsp") != 1 || a.State().Responses != 1 {
		t.Fatalf("Response not posted")
	}

	// Refused for good: dropped.
	f.failWith("POST /rsp", 400)
	if err := a.respond(ctx, der, received(control(ieee20305.HexBinary8{schedule.RespondReceived}))); err == nil {
		t.Fatal("a refused Response")
	}
	// Failing: kept, and posted at the next poll.
	f.failWith("POST /rsp", 503)
	if err := a.respond(ctx, der, received(control(ieee20305.HexBinary8{schedule.RespondReceived}))); err == nil {
		t.Fatal("a failed Response")
	}
	if len(a.pending) != 1 {
		t.Fatalf("%d Responses kept, want 1", len(a.pending))
	}
	f.failWith("POST /rsp", 0)
	a.retryResponses(ctx)
	if len(a.pending) != 0 || a.State().Responses != 2 {
		t.Fatalf("pending %d, Responses %d", len(a.pending), a.State().Responses)
	}

	// No more than maxPending kept.
	f.failWith("POST /rsp", 503)
	a.pending = make([]pendingResponse, maxPending)
	if err := a.respond(ctx, der, received(control(ieee20305.HexBinary8{schedule.RespondReceived}))); err == nil {
		t.Fatal("a failed Response")
	}
	if len(a.pending) != maxPending {
		t.Errorf("%d Responses kept", len(a.pending))
	}
	a.pending = nil

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) != 5 {
		t.Errorf("%d Responses in the metrics, want 5", len(m.responses))
	}
}

// TestFlowReservationResponses has the agent follow the
// FlowReservationResponses of its EndDevice.
func TestFlowReservationResponses(t *testing.T) {
	f := newFake(t)
	f.setEndDevice(`<RegistrationLink href="/edev/1/rg"/><FunctionSetAssignmentsListLink href="/edev/1/fsa" all="1"/>
		<FlowReservationResponseListLink href="/edev/1/frp" all="0"/>`)
	f.set("/edev/1/frp", `<FlowReservationResponseList `+ns+` href="/edev/1/frp" all="0" results="0"/>`)
	a := newTestAgent(t, f, Config{})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("GET /edev/1/frp") != 1 {
		t.Error("the FlowReservationResponseList was not read")
	}
	f.failWith("GET /edev/1/frp", 500)
	if err := a.poll(context.Background()); err == nil {
		t.Error("poll with a failing FlowReservationResponseList")
	}
}

// TestBestProgramWithoutControls checks that a DERProgram without a
// DERControlListLink keeps its primacy in the choice of the program of
// highest precedence, whose DefaultDERControl the agent passes on.
func TestBestProgramWithoutControls(t *testing.T) {
	f := newFake(t)
	f.set("/derp", `<DERProgramList `+ns+` href="/derp" all="2" results="2">
		<DERProgram href="/derp/1"><mRID>A1</mRID><DefaultDERControlLink href="/derp/1/dderc"/>
			<DERControlListLink href="/derp/1/derc" all="0"/><primacy>1</primacy></DERProgram>
		<DERProgram href="/derp/2"><mRID>A2</mRID><DefaultDERControlLink href="/derp/2/dderc"/><primacy>5</primacy></DERProgram>
	</DERProgramList>`)
	a := newTestAgent(t, f, Config{})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.loops[DER].best.(*ieee20305.DERProgram); p == nil || p.GetHref() != "/derp/1" {
		t.Errorf("best DERProgram %v, want /derp/1 (primacy 1 over 5)", hrefOf(a.loops[DER].best))
	}
}
