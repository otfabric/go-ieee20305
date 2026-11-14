// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
)

// listControls makes the DERControlList of the fake server list the
// controls mrids, in that order.
func listControls(f *fakeServer, mrids ...string) {
	items := make([]string, len(mrids))
	for i, mrid := range mrids {
		items[i] = derControl(mrid, "")
	}
	f.set("/derp/1/derc", derControls(items...))
}

// kept returns the mRIDs of the DER events of a, sorted.
func kept(a *Agent) string {
	var ids []string
	for _, ev := range a.Events(DER) {
		ids = append(ids, ev.MRID)
	}
	slices.Sort(ids)
	return strings.Join(ids, " ")
}

// TestMaxEvents: of the events a server lists beyond Config.MaxEvents, the
// agent keeps those it has and takes the new ones in the order of the
// list until it has MaxEvents; it counts and logs the others at each poll,
// and the poll succeeds.
func TestMaxEvents(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	log := &entries{}
	a := newTestAgent(t, f, Config{MaxEvents: 3, Logger: log})
	for i, step := range []struct {
		list    []string
		kept    string
		refused uint64
	}{
		// max-1, max: all kept.
		{[]string{"B1", "B2"}, "B1 B2", 0},
		{[]string{"B1", "B2", "B3"}, "B1 B2 B3", 0},
		// max+1, max+2: the first of the list stay, the others are counted.
		{[]string{"B1", "B2", "B3", "B4"}, "B1 B2 B3", 1},
		{[]string{"B1", "B2", "B3", "B4", "B5"}, "B1 B2 B3", 3},
		// An event the server no longer lists makes room for the first new
		// one of the list.
		{[]string{"B1", "B3", "B4", "B5"}, "B1 B3 B4", 4},
		// The events the agent has stay, wherever the server lists them.
		{[]string{"B9", "B8", "B7", "B1", "B3", "B4"}, "B1 B3 B4", 7},
		// The same event twice counts once.
		{[]string{"B1", "B3", "B4", "B6", "B6"}, "B1 B3 B4", 8},
		{nil, "", 8},
	} {
		listControls(f, step.list...)
		var err error
		if i == 0 {
			err = a.Start(ctx)
		} else {
			err = a.poll(ctx)
		}
		if err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		if got := kept(a); got != step.kept {
			t.Fatalf("poll %d of %v: events %q, want %q", i, step.list, got, step.kept)
		}
		if st := a.EventStats(DER); st.Refused != step.refused {
			t.Fatalf("poll %d of %v: %d events refused, want %d", i, step.list, st.Refused, step.refused)
		}
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.warns) != 5 || !strings.Contains(log.warns[1], "DER: the server lists 2 events beyond the 3 the agent keeps") {
		t.Fatalf("Warn entries %q", log.warns)
	}
}

// TestMaxEventsDefault: an agent keeps schedule.DefaultMaxEvents events of
// a function set by default, and every event with a negative MaxEvents.
func TestMaxEventsDefault(t *testing.T) {
	// One control after the other, none of which overlap.
	controls := make([]string, schedule.DefaultMaxEvents+1)
	for i := range controls {
		controls[i] = connect(fmt.Sprintf("B%05d", i), i, 100*i, 60)
	}
	for _, tt := range []struct {
		maxEvents int
		keeps     int
		refused   uint64
	}{
		{0, schedule.DefaultMaxEvents, 1},
		{schedule.DefaultMaxEvents + 1, schedule.DefaultMaxEvents + 1, 0},
		{-1, schedule.DefaultMaxEvents + 1, 0},
	} {
		f := newFake(t)
		f.set("/derp/1/derc", derControls(controls...))
		a := newTestAgent(t, f, Config{MaxEvents: tt.maxEvents})
		if err := a.Start(context.Background()); err != nil {
			t.Fatalf("MaxEvents %d: %v", tt.maxEvents, err)
		}
		if n, st := len(a.Events(DER)), a.EventStats(DER); n != tt.keeps || st.Refused != tt.refused {
			t.Errorf("MaxEvents %d: %d events, %d refused; want %d, %d", tt.maxEvents, n, st.Refused, tt.keeps, tt.refused)
		}
	}
	if st := (&Agent{}).EventStats(functionSets); st != (schedule.Stats{}) {
		t.Errorf("EventStats of no function set: %+v", st)
	}
}

// connect is a DERControl of the fake server that sets opModConnect, so
// that it conflicts with the others: created at created, it starts start
// seconds after an hour from now and lasts dur seconds.
func connect(mrid string, created, start, dur int) string {
	return fmt.Sprintf(`<DERControl href="/derp/1/derc/%s"><mRID>%s</mRID><creationTime>%d</creationTime>
		<EventStatus><currentStatus>0</currentStatus><dateTime>0</dateTime><potentiallySuperseded>false</potentiallySuperseded></EventStatus>
		<interval><duration>%d</duration><start>%d</start></interval>
		<DERControlBase><opModConnect>true</opModConnect></DERControlBase></DERControl>`,
		mrid, mrid, created, dur, time.Now().Add(time.Hour).Unix()+int64(start))
}

// states returns the DER events of a with their states, by mRID.
func states(a *Agent) string {
	var out []string
	for _, ev := range a.Events(DER) {
		out = append(out, ev.MRID+"="+ev.Status.String())
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// TestPollResolvesTogether: the events of a poll are resolved together,
// whatever the order of the list, and without those the server no longer
// lists.
func TestPollResolvesTogether(t *testing.T) {
	ctx := context.Background()
	// 0A overlaps 0B and 0B overlaps 0C; 0A does not overlap 0C. 0A
	// precedes 0B, which precedes 0C: 0B is superseded, and 0C is not.
	a, b, c := connect("0A", 3, 100, 60), connect("0B", 2, 150, 60), connect("0C", 1, 200, 60)
	for _, order := range [][]string{{a, b, c}, {c, b, a}, {b, c, a}} {
		f := newFake(t)
		f.set("/derp/1/derc", derControls(order...))
		ag := newTestAgent(t, f, Config{})
		if err := ag.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := states(ag), "0A=Scheduled 0B=Superseded 0C=Scheduled"; got != want {
			t.Errorf("events %s, want %s", got, want)
		}
	}
	// E1 precedes E2 and overlaps it. The poll that brings E2 no longer
	// lists E1: E2 is not superseded by an event that is gone.
	f := newFake(t)
	f.set("/derp/1/derc", derControls(connect("E1", 9, 100, 60)))
	ag := newTestAgent(t, f, Config{})
	if err := ag.Start(ctx); err != nil {
		t.Fatal(err)
	}
	f.set("/derp/1/derc", derControls(connect("E2", 1, 120, 60)))
	if err := ag.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := states(ag), "E2=Scheduled"; got != want {
		t.Errorf("events %s, want %s", got, want)
	}
}

// TestPollFails: after a poll that fails, the events read before the
// failure are scheduled, and none goes.
func TestPollFails(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.set("/derp", `<DERProgramList `+ns+` href="/derp" all="2" results="2">
		<DERProgram href="/derp/1"><mRID>A1</mRID><DERControlListLink href="/derp/1/derc" all="1"/><primacy>1</primacy></DERProgram>
		<DERProgram href="/derp/2"><mRID>A2</mRID><DERControlListLink href="/derp/2/derc" all="1"/><primacy>2</primacy></DERProgram>
	</DERProgramList>`)
	listControls(f, "B0")
	f.set("/derp/2/derc", `<DERControlList `+ns+` href="/derp/2/derc" all="0" results="0"/>`)
	a := newTestAgent(t, f, Config{})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	listControls(f, "B1")
	f.failWith("GET /derp/2/derc", 500)
	var se *ieee20305.StatusError
	if err := a.poll(ctx); !errors.As(err, &se) || se.StatusCode != 500 {
		t.Fatalf("poll: %v", err)
	}
	if got := kept(a); got != "B0 B1" {
		t.Fatalf("events after a poll that failed: %q, want B0 and B1", got)
	}
	f.failWith("GET /derp/2/derc", 0)
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := kept(a); got != "B1" {
		t.Fatalf("events after the next poll: %q, want B1", got)
	}
}
