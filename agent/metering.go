// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"fmt"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Mirror creates the MirrorUsagePoint mup of the device in the server's
// MirrorUsagePointList, or updates the one of its mRID, and returns its
// href. The agent sets its deviceLFDI.
func (a *Agent) Mirror(ctx context.Context, mup *ieee20305.MirrorUsagePoint) (string, error) {
	dc := a.State().DeviceCapability
	if dc == nil || dc.MirrorUsagePointListLink == nil {
		return "", fmt.Errorf("%w: the server has no MirrorUsagePointList", ieee20305.ErrProtocol)
	}
	mup.DeviceLFDI = ieee20305.HexBinary160(a.cfg.Identity.LFDI[:])
	href, err := a.c.Post(ctx, dc.MirrorUsagePointListLink.Href, mup)
	if err == nil && href == "" {
		err = fmt.Errorf("%w: the server sent no Location for the MirrorUsagePoint", ieee20305.ErrProtocol)
	}
	return href, err
}

// PostReadings posts MirrorMeterReadings to the MirrorUsagePoint at mup.
func (a *Agent) PostReadings(ctx context.Context, mup string, readings ...ieee20305.MirrorMeterReading) error {
	switch len(readings) {
	case 0:
		return nil
	case 1:
		_, err := a.c.Post(ctx, mup, &readings[0])
		return err
	}
	l := &ieee20305.MirrorMeterReadingList{MirrorMeterReading: readings}
	l.All, l.Results = ieee20305.UInt32(len(readings)), ieee20305.UInt32(len(readings))
	_, err := a.c.Post(ctx, mup, l)
	return err
}

// mirror runs the metering of the agent: it creates its MirrorUsagePoint
// and posts the readings of Config.Readings at the postRate, until ctx
// ends.
func (a *Agent) mirror(ctx context.Context) {
	if a.cfg.MirrorUsagePoint == nil || a.cfg.Readings == nil {
		return
	}
	delay := a.cfg.RetryDelay
	var href string
	for href == "" {
		h, err := a.Mirror(ctx, a.cfg.MirrorUsagePoint.Clone())
		if err == nil {
			href = h
			break
		}
		a.log.Warn("creating the MirrorUsagePoint failed", "err", err, "retry", delay)
		if sleep(ctx, delay) != nil {
			return
		}
		delay = min(2*delay, a.cfg.MaxRetryDelay)
	}
	a.mu.Lock()
	a.state.MirrorUsagePoint = href
	a.mu.Unlock()
	rate := a.cfg.PostRate
	if rate <= 0 {
		rate = 900 * time.Second
		if pr := a.cfg.MirrorUsagePoint.PostRate; pr != nil && *pr > 0 {
			rate = time.Duration(*pr) * time.Second
		}
	}
	for {
		readings, err := a.cfg.Readings(ctx)
		if err == nil {
			err = a.PostReadings(ctx, href, readings...)
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log.Warn("posting readings failed", "err", err)
		} else {
			a.mu.Lock()
			a.state.Posts++
			a.mu.Unlock()
		}
		if sleep(ctx, rate) != nil {
			return
		}
	}
}

// pollFiles passes the Files of the assignments the agent has not seen to
// OnFile.
func (a *Agent) pollFiles(ctx context.Context, fsas []*ieee20305.FunctionSetAssignments) error {
	if a.cfg.OnFile == nil {
		return nil
	}
	lists := map[string]bool{}
	current := map[string]bool{}
	for _, fsa := range fsas {
		if fsa.FileListLink == nil || lists[fsa.FileListLink.Href] {
			continue
		}
		lists[fsa.FileListLink.Href] = true
		var files []*ieee20305.File
		if err := a.c.Iterate(ctx, fsa.FileListLink.Href, 0, func(it any) error {
			if f, ok := it.(*ieee20305.File); ok {
				files = append(files, f)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, f := range files {
			key := f.GetHref() + "|" + string(f.MfVer)
			current[key] = true
			a.mu.Lock()
			seen := a.files[key]
			a.mu.Unlock()
			if seen {
				continue
			}
			if err := a.cfg.OnFile(ctx, f); err != nil {
				return err
			}
			a.mu.Lock()
			a.files[key] = true
			a.mu.Unlock()
		}
	}
	// The agent remembers the Files the server still lists, no more.
	a.mu.Lock()
	for key := range a.files {
		if !current[key] {
			delete(a.files, key)
		}
	}
	a.mu.Unlock()
	return nil
}

// PutFileStatus puts the FileStatus of the device.
func (a *Agent) PutFileStatus(ctx context.Context, st *ieee20305.FileStatus) error {
	ed := a.State().EndDevice
	if ed == nil || ed.FileStatusLink == nil {
		return fmt.Errorf("%w: the device has no FileStatus", ieee20305.ErrProtocol)
	}
	return a.c.Put(ctx, ed.FileStatusLink.Href, st)
}

// RequestFlowReservation posts a FlowReservationRequest of the device and
// returns its href. The server's FlowReservationResponse comes as an event
// of the FlowReservation function set (OnFlowReservation).
func (a *Agent) RequestFlowReservation(ctx context.Context, req *ieee20305.FlowReservationRequest) (string, error) {
	ed := a.State().EndDevice
	if ed == nil || ed.FlowReservationRequestListLink == nil {
		return "", fmt.Errorf("%w: the device has no FlowReservationRequestList", ieee20305.ErrProtocol)
	}
	return a.c.Post(ctx, ed.FlowReservationRequestListLink.Href, req)
}
