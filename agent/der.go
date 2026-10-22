// SPDX-License-Identifier: MIT

package agent

import (
	"bytes"
	"context"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
)

// offsetClock is the clock of the server as the agent knows it: the local
// clock corrected by the offset of the last time sync.
type offsetClock struct{ a *Agent }

func (c offsetClock) Now() time.Time { return time.Now().Add(c.a.State().Time.Offset) }

func (offsetClock) AfterFunc(d time.Duration, f func()) schedule.Stopper {
	return time.AfterFunc(d, f)
}

// program is a DERProgram of the device, as of the last poll.
type program struct {
	href string
	doc  *ieee20305.DERProgram
}

// pollDER reads the DERPrograms of the assignments, schedules their
// DERControls, and passes on the DefaultDERControl of the program of
// highest precedence when it changes.
func (a *Agent) pollDER(ctx context.Context, fsas []*ieee20305.FunctionSetAssignments) error {
	var programs []program
	seen := map[string]bool{}
	for _, fsa := range fsas {
		if fsa.DERProgramListLink == nil {
			continue
		}
		err := a.c.Iterate(ctx, fsa.DERProgramListLink.Href, 0, func(it any) error {
			if p, ok := it.(*ieee20305.DERProgram); ok && !seen[p.GetHref()] {
				seen[p.GetHref()] = true
				programs = append(programs, program{href: p.GetHref(), doc: p})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	events := map[string]bool{}
	var best *program
	for i, p := range programs {
		if best == nil || p.doc.Primacy < best.doc.Primacy {
			best = &programs[i]
		}
		if p.doc.DERControlListLink == nil {
			continue
		}
		err := a.c.Iterate(ctx, p.doc.DERControlListLink.Href, 0, func(it any) error {
			ctl, ok := it.(*ieee20305.DERControl)
			if !ok {
				return nil
			}
			ev, err := schedule.FromEvent(ctl, uint8(p.doc.Primacy), p.href)
			if err != nil {
				a.log.Warn("skipping a DERControl", "href", ctl.GetHref(), "err", err)
				return nil
			}
			events[ev.MRID] = true
			a.der.Upsert(ev)
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, ev := range a.der.Events() {
		if !events[ev.MRID] {
			a.der.Remove(ev.MRID)
		}
	}
	return a.pollDefault(ctx, best)
}

// pollDefault reads the DefaultDERControl of the program p and passes it
// on when it changed.
func (a *Agent) pollDefault(ctx context.Context, p *program) error {
	if a.cfg.OnDefaultDERControl == nil || p == nil || p.doc.DefaultDERControlLink == nil {
		return nil
	}
	var d ieee20305.DefaultDERControl
	if err := a.c.Get(ctx, p.doc.DefaultDERControlLink.Href, &d); err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	out, err := ieee20305.Marshal(&d)
	if err != nil {
		return err
	}
	a.mu.Lock()
	same := bytes.Equal(out, a.lastDefault)
	a.lastDefault = out
	a.mu.Unlock()
	if same {
		return nil
	}
	return a.cfg.OnDefaultDERControl(ctx, &d)
}

// runDER passes the transitions of the DERControls to the handler and
// posts the Responses they ask for, until the scheduler closes.
func (a *Agent) runDER(ch <-chan schedule.Transition) {
	defer close(a.derDone)
	for t := range ch {
		ctx := context.Background()
		if a.cfg.OnDERControl != nil {
			if err := a.cfg.OnDERControl(ctx, t); err != nil {
				a.log.Warn("DERControl handler failed", "mRID", t.Event.MRID, "err", err)
			}
		}
		if err := a.respond(ctx, t); err != nil {
			a.log.Warn("posting a Response failed", "mRID", t.Event.MRID, "err", err)
		}
	}
}

// respond posts the DERControlResponse the transition t calls for.
func (a *Agent) respond(ctx context.Context, t schedule.Transition) error {
	ctl, ok := t.Event.Value.(*ieee20305.DERControl)
	if !ok || ctl.ReplyTo == nil || *ctl.ReplyTo == "" {
		return nil
	}
	var flags []byte
	if ctl.ResponseRequired != nil {
		flags = *ctl.ResponseRequired
	}
	send, status := schedule.ResponseRequired(flags, t)
	if !send {
		return nil
	}
	created := ieee20305.TimeType(t.At.Unix())
	st := ieee20305.UInt8(status)
	r := &ieee20305.DERControlResponse{}
	r.CreatedDateTime, r.Status, r.Subject = &created, &st, ctl.MRID
	r.EndDeviceLFDI = ieee20305.HexBinary160(a.cfg.Identity.LFDI[:])
	_, err := a.c.Post(ctx, *ctl.ReplyTo, r)
	if err == nil {
		a.mu.Lock()
		a.state.Responses++
		a.mu.Unlock()
	}
	return err
}

// ActiveDERControls returns the DERControls active now, in order of
// precedence.
func (a *Agent) ActiveDERControls() []*ieee20305.DERControl {
	var out []*ieee20305.DERControl
	for _, ev := range a.der.Active(a.clock.Now()) {
		if ev.Status != schedule.Active {
			continue
		}
		if c, ok := ev.Value.(*ieee20305.DERControl); ok {
			out = append(out, c)
		}
	}
	return out
}
