// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/xml"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// builtinHooks are the function sets the server implements itself.
func builtinHooks(s *Server) map[string]Hooks {
	return map[string]Hooks{
		"DeviceCapability": {Get: s.getDeviceCapability},
		"Time":             {Get: s.getTime},
		"EndDeviceList":    {Create: s.createEndDevice},
		"EndDevice":        {Update: s.updateEndDevice, Delete: s.deleteEndDevice},
		"Registration":     {Update: s.operatorOnly, Delete: s.operatorOnly},
	}
}

// getDeviceCapability answers DeviceCapability with its pollRate. (The codec
// of a 2018 client strips the links 2018 does not define.)
func (s *Server) getDeviceCapability(_ context.Context, r *Request) (any, error) {
	dc, ok := s.profile.Clone(r.Node.Value).(*ieee20305.DeviceCapability)
	if !ok {
		return r.Node.Value, nil
	}
	rate := ieee20305.UInt32(s.cfg.PollRate)
	dc.PollRate = &rate
	return dc, nil
}

// getTime answers Time from the clock of the server, in UTC (tzOffset and
// dstOffset 0) with the quality of a server that does not know better than
// its own clock (7: intentionally uncoordinated).
func (s *Server) getTime(_ context.Context, r *Request) (any, error) {
	t, ok := s.profile.Clone(r.Node.Value).(*ieee20305.Time)
	if !ok {
		t = &ieee20305.Time{}
	}
	now := s.clock()
	t.CurrentTime = ieee20305.TimeType(now.Unix())
	t.Quality = 7
	t.TzOffset = 0
	t.DstOffset = 0
	t.DstStartTime, t.DstEndTime = 0, 0
	local := ieee20305.TimeType(now.Unix())
	t.LocalTime = &local
	setHref(t, r.Node.Href)
	return t, nil
}

// qname returns the name of a document element of IEEE 2030.5.
func qname(local string) xml.Name {
	return xml.Name{Space: ieee20305.Namespace, Local: local}
}
