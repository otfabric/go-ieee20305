// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
	"github.com/otfabric/go-ieee20305/security"
)

// events keeps the EventStatus of the events of the server (DERControls,
// EndDeviceControls, TextMessages, TimeTariffIntervals, ...) up to date: a
// Scheduler without randomization or supersession (which IEEE 2030.5-2023
// leaves to clients) moves them from Scheduled to Active, and the server
// writes the new currentStatus, which notifies the subscriptions. An event
// keeps the status Active after its end: IEEE 2030.5-2018 has no Completed.
type events struct {
	s     *Server
	sched *schedule.Scheduler
	stop  func()
	done  chan struct{}
}

// clockFunc is the schedule.Clock of a server: its Clock for the time, and
// timers of the operating system for the delays.
type clockFunc func() time.Time

func (c clockFunc) Now() time.Time { return c() }

func (clockFunc) AfterFunc(d time.Duration, f func()) schedule.Stopper {
	return time.AfterFunc(d, f)
}

func newEvents(s *Server) *events {
	clock := s.cfg.EventClock
	if clock == nil {
		clock = clockFunc(func() time.Time { return s.clock() })
	}
	e := &events{s: s, done: make(chan struct{})}
	e.sched = schedule.New(schedule.Options{Clock: clock, Conflicts: func(a, b schedule.Event) bool { return false }})
	ch, stop := e.sched.Subscribe()
	e.stop = stop
	go e.run(ch)
	return e
}

func (e *events) close() {
	e.sched.Close()
	<-e.done
}

// run writes the status of the events as they change.
func (e *events) run(ch <-chan schedule.Transition) {
	defer close(e.done)
	for t := range ch {
		if t.Received() || t.To == schedule.Completed {
			continue
		}
		if err := e.setStatus(context.Background(), t.Event.MRID, t.To, t.At); err != nil && !errors.Is(err, ErrNoNode) {
			e.s.log.Error("updating an event status", "event", t.Event.MRID, "err", err)
		}
	}
}

// setStatus sets the currentStatus of the event at href, if it differs.
func (e *events) setStatus(ctx context.Context, href string, to schedule.Status, at time.Time) error {
	n, err := e.s.store.Get(ctx, href)
	if err != nil {
		return err
	}
	st := eventStatus(n.Value)
	if st == nil || st.CurrentStatus == ieee20305.UInt8(to) {
		return nil
	}
	st.CurrentStatus, st.DateTime = ieee20305.UInt8(to), ieee20305.TimeType(at.Unix())
	n.Modified = e.s.clock()
	return e.s.store.Put(ctx, n)
}

// eventStatus returns the EventStatus of an event, or nil.
func eventStatus(v any) *ieee20305.EventStatus {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	f := rv.Elem().FieldByName("EventStatus")
	if !f.IsValid() || f.Type() != reflect.TypeOf(ieee20305.EventStatus{}) {
		return nil
	}
	return f.Addr().Interface().(*ieee20305.EventStatus)
}

// changed is told of a change of the node at href, or of its deletion.
func (e *events) changed(href string, node *Node, deleted bool) {
	if deleted {
		for _, ev := range e.sched.Events() {
			if within(ev.MRID, href) {
				e.sched.Remove(ev.MRID)
			}
		}
		return
	}
	if node == nil || node.Value == nil {
		return
	}
	ev, err := schedule.FromEvent(node.Value, 0, node.Parent)
	if err != nil {
		return
	}
	ev.MRID = href // unique on the server, unlike mRIDs
	e.sched.Upsert(ev)
}

// changed is told of every change of the store.
func (s *Server) changed(ctx context.Context, href string, node *Node, deleted bool) {
	s.events.changed(href, node, deleted)
	s.notifier.changed(ctx, href, node, deleted)
}

// CancelEvent cancels the event at href, as of now: with randomization
// when randomized, so that clients end an active event after a random
// delay.
func (a *Admin) CancelEvent(ctx context.Context, href string, randomized bool) error {
	n, err := a.s.store.Get(ctx, href)
	if err != nil {
		return err
	}
	st := eventStatus(n.Value)
	if st == nil {
		return fmt.Errorf("%w: %s is no event", ieee20305.ErrInvalid, href)
	}
	st.CurrentStatus = ieee20305.UInt8(schedule.Cancelled)
	if randomized {
		st.CurrentStatus = ieee20305.UInt8(schedule.CancelledRandom)
	}
	st.DateTime = ieee20305.TimeType(a.s.clock().Unix())
	n.Modified = a.s.clock()
	return a.s.store.Put(ctx, n)
}

// activeLists are the lists of the events of a program that are active,
// which the server computes from the list of all of its events.
var activeLists = []string{"ActiveDERControlList", "ActiveEndDeviceControlList", "ActiveTextMessageList",
	"ActiveTimeTariffIntervalList", "ActiveSupplyInterruptionOverrideList"}

// getActive answers an Active*List: the items of the sibling list of all
// events that are active now.
func (s *Server) getActive(ctx context.Context, r *Request) (any, error) {
	source := path.Join(path.Dir(r.Node.Href), s.partPath(strings.TrimPrefix(r.Node.Resource, "Active")))
	now := s.clock().Unix()
	return s.listOf(ctx, r, source, func(n *Node) bool {
		if st := eventStatus(n.Value); st != nil {
			if st.CurrentStatus != ieee20305.UInt8(schedule.Active) {
				return false
			}
		}
		start, ok := eventStart(n.Value)
		if !ok {
			return true
		}
		end := start + int64(eventDuration(n.Value))
		return start <= now && now < end
	})
}

// eventDuration returns the duration of the interval of an event.
func eventDuration(v any) uint32 {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0
	}
	f := rv.Elem().FieldByName("Interval")
	if !f.IsValid() || f.Type() != reflect.TypeOf(ieee20305.DateTimeInterval{}) {
		return 0
	}
	return uint32(f.Interface().(ieee20305.DateTimeInterval).Duration)
}

// getCurrentDERProgram answers the CurrentDERProgram of a DER: of the
// programs of its AssociatedDERProgramList the client reads, the one of
// highest precedence (lowest primacy).
func (s *Server) getCurrentDERProgram(ctx context.Context, r *Request) (any, error) {
	assoc := path.Join(path.Dir(r.Node.Href), s.partPath("AssociatedDERProgramList"))
	members, err := s.store.Members(ctx, assoc)
	if err != nil && !errors.Is(err, ErrNoNode) {
		return nil, err
	}
	var best *ieee20305.DERProgram
	for _, m := range members {
		p, ok := m.Value.(*ieee20305.DERProgram)
		if ok && s.canRead(r.Principal, m) && (best == nil || p.Primacy < best.Primacy) {
			best = p
		}
	}
	if best == nil {
		return nil, httpError(http.StatusNotFound, "%s: no program", r.Node.Href)
	}
	return best, nil
}

// responseMu serializes the deduplication of Responses.
var responseMu sync.Mutex

// createResponse is the POST of a Response (of any subtype) to a
// ResponseList: of a device the client acts for, once per subject, device
// and status — a repeated Response gets the href of the first.
func (s *Server) createResponse(ctx context.Context, r *Request) (string, error) {
	resp := responseOf(r.Doc)
	if resp == nil {
		return s.createDefault(ctx, r)
	}
	var lfdi security.LFDI
	if len(resp.EndDeviceLFDI) != len(lfdi) {
		return "", &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest, Err: errors.New("endDeviceLFDI is no LFDI")}
	}
	copy(lfdi[:], resp.EndDeviceLFDI)
	if !s.acts(r.Principal, lfdi.String()) {
		return "", httpError(http.StatusForbidden, "a Response of device %s", lfdi)
	}
	responseMu.Lock()
	defer responseMu.Unlock()
	members, err := s.store.Members(ctx, r.Node.Href)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if old := responseOf(m.Value); old != nil && sameResponse(old, resp) {
			return m.Href, nil
		}
	}
	href, err := s.createItem(ctx, r.Node, r.Doc, lfdi.String())
	if err != nil {
		return "", err
	}
	if s.cfg.OnResponse != nil {
		s.cfg.OnResponse(ctx, href, r.Doc)
	}
	return href, nil
}

// responseOf returns the Response a document is or embeds, or nil.
func responseOf(v any) *ieee20305.Response {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil
	}
	if r, ok := v.(*ieee20305.Response); ok {
		return r
	}
	f := rv.Elem().FieldByName("Response")
	if !f.IsValid() || f.Type() != reflect.TypeOf(ieee20305.Response{}) {
		return nil
	}
	return f.Addr().Interface().(*ieee20305.Response)
}

func sameResponse(a, b *ieee20305.Response) bool {
	sa, sb := -1, -1
	if a.Status != nil {
		sa = int(*a.Status)
	}
	if b.Status != nil {
		sb = int(*b.Status)
	}
	return bytes.Equal(a.Subject, b.Subject) && bytes.Equal(a.EndDeviceLFDI, b.EndDeviceLFDI) && sa == sb
}
