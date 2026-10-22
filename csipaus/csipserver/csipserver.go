// SPDX-License-Identifier: MIT

// Package csipserver configures a server of package server for CSIP-AUS: the
// documents of package csipaus, the ConnectionPoint of each EndDevice, and
// the site limits (dynamic operating envelopes) of DERControls and
// DefaultDERControls.
//
//	srv, err := server.New(csipserver.Configure(server.Config{}))
//	// The limits of a program when no control applies:
//	err = csipserver.SetDefaultLimits(ctx, srv.Admin(), program,
//		csipaus.Limits{Export: csipserver.Watts(5000)})
//	// An envelope of 1.5 kW export for the next 30 minutes:
//	env := csipserver.Envelope(mrid, time.Now(), 30*time.Minute,
//		csipaus.Limits{Export: csipserver.Watts(1500)})
//	_, err = srv.Admin().Create(ctx, program+"/derc", env, "")
package csipserver

import (
	"context"
	"encoding/xml"
	"path"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/server"
)

// ConnectionPoint is the resource of CSIP-AUS that holds the connection
// point (NMI) of a site, linked from its EndDevice; the device puts it.
var ConnectionPoint = ieee20305.ResourceSpec{
	ID:         "ConnectionPoint",
	SamplePath: "/edev/{id1}/cp",
	Methods: []ieee20305.MethodSpec{
		{Method: "GET", Conformance: ieee20305.ConformanceMandatory, Response: "ConnectionPoint", Statuses: []int{200}},
		{Method: "HEAD", Conformance: ieee20305.ConformanceMandatory, Response: "ConnectionPoint", Statuses: []int{200}},
		{Method: "PUT", Conformance: ieee20305.ConformanceMandatory, Request: "ConnectionPoint", Statuses: []int{201, 204}},
		{Method: "POST", Conformance: ieee20305.ConformanceError},
		{Method: "DELETE", Conformance: ieee20305.ConformanceOptional, Statuses: []int{204}},
	},
}

// Profile returns the server profile of CSIP-AUS: the documents of
// csipaus, which profiles IEEE 2030.5-2018, and the ConnectionPoint of each
// EndDevice.
func Profile() *server.Profile {
	return &server.Profile{
		Codec: func(ieee20305.Edition) ieee20305.Codec { return csipaus.Codec{} },
		NewElement: func(name xml.Name) any {
			if v := csipaus.NewElement(name); v != nil {
				return v
			}
			return ieee20305.NewElement(name)
		},
		Clone: func(v any) any {
			if c := csipaus.Clone(v); c != nil {
				return c
			}
			return ieee20305.Clone(v)
		},
		ElementName: func(v any) (xml.Name, bool) {
			if n, ok := csipaus.ElementName(v); ok {
				return n, true
			}
			return ieee20305.ElementName(v)
		},
		Derives: func(elem, of string) bool {
			return csipaus.Derives(elem, of) || ieee20305.Derives(elem, of)
		},
		Resources: []ieee20305.ResourceSpec{ConnectionPoint},
		Parts:     map[string][]string{"EndDevice": {"ConnectionPoint"}},
	}
}

// Configure returns cfg with the profile of CSIP-AUS and the edition it
// profiles, IEEE 2030.5-2018.
func Configure(cfg server.Config) server.Config {
	cfg.Profile = Profile()
	cfg.DefaultEdition = ieee20305.Edition2018
	return cfg
}

// Watts returns an ActivePower of w watts.
func Watts(w int16) *ieee20305.ActivePower { return &ieee20305.ActivePower{Value: ieee20305.Int16(w)} }

// Envelope returns a DERControl of mRID mrid that sets the limits from start
// for d, created now by the local clock (set its CreationTime to that of the
// server's Admin.Now when the two differ: it orders events of the same
// primacy).
func Envelope(mrid ieee20305.MRIDType, start time.Time, d time.Duration, l csipaus.Limits) *csipaus.DERControl {
	c := &csipaus.DERControl{}
	c.MRID = mrid
	c.CreationTime = ieee20305.TimeType(time.Now().Unix())
	c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(start.Unix()), Duration: ieee20305.UInt32(d / time.Second)}
	l.Apply(&c.DERControlBase)
	return c
}

// SetDefaultLimits puts the DefaultDERControl of the DERProgram at program
// with the limits l: what applies when no DERControl does.
func SetDefaultLimits(ctx context.Context, a *server.Admin, program string, l csipaus.Limits) error {
	n, err := a.Get(ctx, path.Join(program, "dderc"))
	if err != nil {
		return err
	}
	d, ok := n.Value.(*csipaus.DefaultDERControl)
	if !ok {
		d = &csipaus.DefaultDERControl{}
	}
	l.Apply(&d.DERControlBase)
	now := ieee20305.TimeType(a.Now().Unix())
	d.UpdatedTime = &now
	n.Value = d
	return a.Put(ctx, n)
}
