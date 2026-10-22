// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// The scheduler of the server forgets events that ended longer than
// EventRetention ago, at most once a minute.
func TestEventRetention(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	s, err := New(Config{Clock: clock, EventRetention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	a := s.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{}, "")
	if err != nil {
		t.Fatal(err)
	}
	event := func(mrid byte, start int64) {
		c := &ieee20305.DERControl{}
		c.MRID = ieee20305.MRIDType{mrid}
		c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(start), Duration: 600}
		if _, err := a.Create(ctx, prog+"/derc", c, ""); err != nil {
			t.Fatal(err)
		}
	}
	ids := func() []string {
		var out []string
		for _, ev := range s.events.sched.Events() {
			out = append(out, ev.MRID)
		}
		return out
	}
	// The first change prunes: the event that ended over an hour ago goes.
	event(1, now.Unix()-3*3600)
	event(2, now.Unix())
	if got := ids(); len(got) != 1 || got[0] != prog+"/derc/2" {
		t.Fatalf("events %v", got)
	}
	// The status of the second is written in the background: wait for it, as
	// writing it again would schedule it again.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, err := a.Get(ctx, prog+"/derc/2")
		if err == nil && n.Value.(*ieee20305.DERControl).EventStatus.CurrentStatus == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second event did not become active")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// After two hours the second goes.
	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	event(3, now.Unix())
	if got := ids(); len(got) != 1 || got[0] != prog+"/derc/3" {
		t.Errorf("events %v", got)
	}
}
