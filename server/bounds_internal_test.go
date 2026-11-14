// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"slices"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
)

func TestHrefIndex(t *testing.T) {
	x := newHrefIndex()
	for _, h := range []string{"/a", "/a/1", "/a/1/b", "/c", "/a/1"} {
		x.add(h)
	}
	within := func(base string) []string {
		got := x.within(base)
		slices.Sort(got)
		return got
	}
	if got := within("/a/1/"); !slices.Equal(got, []string{"/a/1", "/a/1/b"}) {
		t.Errorf("within /a/1: %v", got)
	}
	if got := within("/c"); !slices.Equal(got, []string{"/c"}) {
		t.Errorf("within /c: %v", got)
	}
	if got := within("/d"); got != nil {
		t.Errorf("within /d: %v", got)
	}
	if got := within("/"); len(got) != 4 {
		t.Errorf("within /: %v", got)
	}
	x.remove("/a/1/b")
	x.remove("/a/1/b")
	if got := within("/a/1"); !slices.Equal(got, []string{"/a/1"}) {
		t.Errorf("within /a/1 after remove: %v", got)
	}
	x.remove("/a/1")
	x.remove("/a")
	x.remove("/c")
	if len(x.set) != 0 || len(x.below) != 0 {
		t.Errorf("left %v %v", x.set, x.below)
	}
}

// The counters of a deleted node and of the nodes below it go; that of
// its list stays, so its href is not given out again.
func TestCountersForgotten(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	lfdi := security.LFDI{1}
	ed, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: lfdi})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admin().Create(ctx, ed+"/lel", &ieee20305.LogEvent{}, lfdi.String()); err != nil {
		t.Fatal(err)
	}
	count := func(h string) (int, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		n, ok := s.counters[h]
		return n, ok
	}
	if _, ok := count(ed + "/lel"); !ok {
		t.Fatal("no counter of the LogEventList")
	}
	if err := s.Admin().RemoveDevice(ctx, lfdi); err != nil {
		t.Fatal(err)
	}
	if _, ok := count(ed + "/lel"); ok {
		t.Error("the counter of a deleted list stays")
	}
	if n, _ := count(EndDeviceListHref); n != 1 {
		t.Errorf("the counter of the EndDeviceList is %d", n)
	}
	if ed2, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: lfdi}); err != nil || ed2 == ed {
		t.Errorf("a new EndDevice at %q: %v", ed2, err)
	}
}

// Deleting a program removes its events from the scheduler, and only
// them.
func TestEventsOfDeletedNodes(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	a := s.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	var progs []string
	for i := range 2 {
		prog, err := a.Create(ctx, "/derp", &ieee20305.DERProgram{}, "")
		if err != nil {
			t.Fatal(err)
		}
		c := &ieee20305.DERControl{}
		c.MRID = ieee20305.MRIDType{byte(i + 1)}
		c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(time.Now().Unix() + 3600), Duration: 60}
		if _, err := a.Create(ctx, prog+"/derc", c, ""); err != nil {
			t.Fatal(err)
		}
		progs = append(progs, prog)
	}
	if err := a.Delete(ctx, progs[0]); err != nil {
		t.Fatal(err)
	}
	evs := s.events.sched.Events()
	if len(evs) != 1 || evs[0].MRID != progs[1]+"/derc/1" {
		t.Errorf("events %v", evs)
	}
	if err := a.Delete(ctx, progs[1]+"/derc/1"); err != nil {
		t.Fatal(err)
	}
	if evs := s.events.sched.Events(); len(evs) != 0 {
		t.Errorf("events %v", evs)
	}
}

// A transition queued before a cancellation does not undo it.
func TestCancelledStatusStays(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	c := &ieee20305.DERControl{}
	c.EventStatus.CurrentStatus = ieee20305.UInt8(schedule.Cancelled)
	if err := s.store.Put(ctx, &Node{Href: "/c", Resource: "DERControl", Value: c}); err != nil {
		t.Fatal(err)
	}
	status := func() ieee20305.UInt8 {
		n, err := s.store.Get(ctx, "/c")
		if err != nil {
			t.Fatal(err)
		}
		return n.Value.(*ieee20305.DERControl).EventStatus.CurrentStatus
	}
	if err := s.events.setStatus(ctx, "/c", schedule.Active, time.Now()); err != nil || status() != ieee20305.UInt8(schedule.Cancelled) {
		t.Errorf("Active over Cancelled: %d %v", status(), err)
	}
	if err := s.events.setStatus(ctx, "/c", schedule.CancelledRandom, time.Now()); err != nil || status() != ieee20305.UInt8(schedule.CancelledRandom) {
		t.Errorf("CancelledRandom over Cancelled: %d %v", status(), err)
	}
}

// trim of a store that does not filter members.
type plainStore struct{ Store }

func TestTrimPlainStore(t *testing.T) {
	s, err := New(Config{Store: plainStore{NewMemStore(nil)}, MaxListItems: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if err := s.Admin().Put(ctx, &Node{Href: "/lel", Resource: "LogEventList", Value: &ieee20305.LogEventList{}}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"A", "B", "A", "A"} {
		if _, err := s.Admin().Create(ctx, "/lel", &ieee20305.LogEvent{}, o); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.store.Get(ctx, "/lel")
	s.trim(ctx, list, "A")
	ms, _ := s.store.Members(ctx, "/lel")
	if len(ms) != 2 || ms[0].Owner != "B" || ms[1].Href != "/lel/4" {
		t.Errorf("after trim: %d", len(ms))
	}
}
