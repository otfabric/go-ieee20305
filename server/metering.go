// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"path"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// UsagePointListHref is where the server keeps the UsagePoints of the
// mirrors devices post, unless the DeviceCapability links another.
const UsagePointListHref = "/upt"

// createMirror is the POST of a MirrorUsagePoint: the server keeps it, and
// a UsagePoint it mirrors to, with the MeterReadings of the
// MirrorMeterReadings it carries. A MirrorUsagePoint with the mRID of one
// the device has updates that one, and gets its href.
func (s *Server) createMirror(ctx context.Context, r *Request) (string, error) {
	mup, ok := r.Doc.(*ieee20305.MirrorUsagePoint)
	if !ok {
		return s.createDefault(ctx, r)
	}
	lfdi, err := s.deviceOf(r.Principal, mup.DeviceLFDI)
	if err != nil {
		return "", err
	}
	if !s.canPost(r.Principal, r.Node) {
		return "", httpError(http.StatusForbidden, "%s is not the client's", r.Node.Href)
	}
	// One mirror per mRID and device: creations in the list are serialized,
	// and so are the readings of a device.
	defer s.listLocks.lock(r.Node.Href)()
	defer s.listLocks.lock("mirror:" + lfdi)()
	readings := mup.MirrorMeterReading
	mup.MirrorMeterReading = nil
	members, err := s.store.Members(ctx, r.Node.Href)
	if err != nil {
		return "", err
	}
	var node *Node
	for _, m := range members {
		if old, ok := m.Value.(*ieee20305.MirrorUsagePoint); ok && m.Owner == lfdi && bytes.Equal(old.MRID, mup.MRID) {
			node = m
			keepLinks(mup, old)
			mup.SetHref(m.Href)
			node.Value, node.Modified = mup, s.clock()
			if err := s.store.Put(ctx, node); err != nil {
				return "", err
			}
		}
	}
	if node == nil {
		href, err := s.createItem(ctx, r.Node, mup, lfdi)
		if err != nil {
			return "", err
		}
		up, err := s.mirroredUsagePoint(ctx, mup, lfdi)
		if err != nil {
			return "", err
		}
		mup.UsagePointLink = &ieee20305.UsagePointLink{}
		mup.UsagePointLink.Href = up
		node = &Node{Href: href, Resource: "MirrorUsagePoint", Value: mup, Owner: lfdi, Parent: r.Node.Href, Modified: s.clock()}
		if err := s.store.Put(ctx, node); err != nil {
			return "", err
		}
	}
	for i := range readings {
		if _, err := s.mirrorReading(ctx, node, &readings[i]); err != nil {
			return "", err
		}
	}
	return node.Href, nil
}

// deviceOf returns the LFDI of the device of a document, which p must act
// for.
func (s *Server) deviceOf(p Principal, raw []byte) (string, error) {
	var lfdi security.LFDI
	if len(raw) != len(lfdi) {
		return "", &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest, Err: errors.New("no device LFDI")}
	}
	copy(lfdi[:], raw)
	if !s.acts(p, lfdi.String()) {
		return "", httpError(http.StatusForbidden, "device %s is not the client's", lfdi)
	}
	return lfdi.String(), nil
}

// mirroredUsagePoint creates the UsagePoint a MirrorUsagePoint mirrors to,
// in the UsagePointList (created if there is none), and returns its href.
func (s *Server) mirroredUsagePoint(ctx context.Context, mup *ieee20305.MirrorUsagePoint, lfdi string) (string, error) {
	list, err := s.store.Get(ctx, UsagePointListHref)
	if errors.Is(err, ErrNoNode) {
		if err := s.Admin().Root(ctx, "UsagePointList", UsagePointListHref, &ieee20305.UsagePointList{}); err != nil {
			return "", err
		}
		list, err = s.store.Get(ctx, UsagePointListHref)
	}
	if err != nil {
		return "", err
	}
	up := &ieee20305.UsagePoint{UsagePointBase: mup.UsagePointBase}
	up.Href = nil
	device := mup.DeviceLFDI
	up.DeviceLFDI = &device
	return s.createItem(ctx, list, up, lfdi)
}

// postMirrorReadings is the POST of a MirrorMeterReading or a
// MirrorMeterReadingList to a MirrorUsagePoint.
func (s *Server) postMirrorReadings(ctx context.Context, r *Request) (string, error) {
	if !s.canChange(r.Principal, r.Node) {
		return "", httpError(http.StatusForbidden, "%s is not the client's", r.Node.Href)
	}
	defer s.listLocks.lock("mirror:" + r.Node.Owner)()
	switch doc := r.Doc.(type) {
	case *ieee20305.MirrorMeterReading:
		return s.mirrorReading(ctx, r.Node, doc)
	case *ieee20305.MirrorMeterReadingList:
		for i := range doc.MirrorMeterReading {
			if _, err := s.mirrorReading(ctx, r.Node, &doc.MirrorMeterReading[i]); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	return "", &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest, Err: errors.New("a MirrorUsagePoint takes MirrorMeterReadings")}
}

// mirrorReading stores a MirrorMeterReading of the MirrorUsagePoint mup in
// its UsagePoint: the MeterReading of its mRID (created if new) with its
// ReadingType, its latest Reading, and its MirrorReadingSets as
// ReadingSets with their Readings. It returns the href of the
// MeterReading. The caller holds the mirror lock of the device.
func (s *Server) mirrorReading(ctx context.Context, mup *Node, mmr *ieee20305.MirrorMeterReading) (string, error) {
	m, ok := mup.Value.(*ieee20305.MirrorUsagePoint)
	if !ok {
		return "", fmt.Errorf("%w: %s is no MirrorUsagePoint", ieee20305.ErrInvalid, mup.Href)
	}
	var mrList *Node
	var err error
	if m.UsagePointLink != nil {
		mrList, err = s.store.Get(ctx, path.Join(m.UsagePointLink.Href, s.partPath("MeterReadingList")))
	}
	if m.UsagePointLink == nil || errors.Is(err, ErrNoNode) {
		// A mirror without a UsagePoint, as the operator may make one.
		up, err := s.mirroredUsagePoint(ctx, m, mup.Owner)
		if err != nil {
			return "", err
		}
		if err := s.linkFrom(ctx, mup.Href, "UsagePointLink", up); err != nil {
			return "", err
		}
		mrList, err = s.store.Get(ctx, path.Join(up, s.partPath("MeterReadingList")))
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	mr, err := s.findOrCreate(ctx, mrList, mmr.MRID, func() any {
		return &ieee20305.MeterReading{MeterReadingBase: mmr.MeterReadingBase}
	}, mup.Owner)
	if err != nil {
		return "", err
	}
	if mmr.ReadingType != nil {
		rt := path.Join(mr, s.partPath("ReadingType"))
		if err := s.Admin().Put(ctx, &Node{Href: rt, Resource: "ReadingType", Value: mmr.ReadingType, Owner: mup.Owner, Parent: mr}); err != nil {
			return "", err
		}
	}
	if mmr.Reading != nil {
		href := path.Join(mr, "r")
		if err := s.Admin().Put(ctx, &Node{Href: href, Resource: "Reading", Value: mmr.Reading, Owner: mup.Owner, Parent: mr}); err != nil {
			return "", err
		}
		if err := s.linkFrom(ctx, mr, "ReadingLink", href); err != nil {
			return "", err
		}
	}
	for i := range mmr.MirrorReadingSet {
		set := &mmr.MirrorReadingSet[i]
		rsList, err := s.store.Get(ctx, path.Join(mr, s.partPath("ReadingSetList")))
		if err != nil {
			return "", err
		}
		rs, err := s.findOrCreate(ctx, rsList, set.MRID, func() any {
			return &ieee20305.ReadingSet{ReadingSetBase: set.ReadingSetBase}
		}, mup.Owner)
		if err != nil {
			return "", err
		}
		rList, err := s.store.Get(ctx, path.Join(rs, s.partPath("ReadingList")))
		if err != nil {
			return "", err
		}
		for j := range set.Reading {
			reading := set.Reading[j]
			reading.Href = nil
			if _, err := s.createItem(ctx, rList, &reading, mup.Owner); err != nil {
				return "", err
			}
		}
		s.trim(ctx, rList.Href)
	}
	return mr, nil
}

// findOrCreate returns the href of the item of list with the mRID mrid,
// or creates one from create.
func (s *Server) findOrCreate(ctx context.Context, list *Node, mrid ieee20305.MRIDType, create func() any, owner string) (string, error) {
	members, err := s.store.Members(ctx, list.Href)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if id, ok := mridOf(m.Value); ok && bytes.Equal(id, mrid) {
			return m.Href, nil
		}
	}
	v := create()
	setHref(v, "")
	return s.createItem(ctx, list, v, owner)
}

// mridOf returns the mRID of a document.
func mridOf(v any) (ieee20305.MRIDType, bool) {
	switch v := v.(type) {
	case *ieee20305.MeterReading:
		return v.MRID, true
	case *ieee20305.ReadingSet:
		return v.MRID, true
	}
	return nil, false
}

// linkFrom sets the link field of the document at href.
func (s *Server) linkFrom(ctx context.Context, href, field, to string) error {
	n, err := s.store.Get(ctx, href)
	if err != nil {
		return err
	}
	if err := setLink(n.Value, field, to); err != nil {
		return err
	}
	n.Modified = s.clock()
	return s.store.Put(ctx, n)
}

// FlowPolicy decides on the FlowReservationRequest of the device lfdi:
// the FlowReservationResponse the server grants, or nil for none (the
// device gets no response). The server sets the href, mRID, subject,
// creationTime and EventStatus of the response.
type FlowPolicy func(ctx context.Context, lfdi string, req *ieee20305.FlowReservationRequest) (*ieee20305.FlowReservationResponse, error)

// GrantAll is the FlowPolicy that grants every request in full.
func GrantAll(_ context.Context, _ string, req *ieee20305.FlowReservationRequest) (*ieee20305.FlowReservationResponse, error) {
	resp := &ieee20305.FlowReservationResponse{EnergyAvailable: req.EnergyRequested, PowerAvailable: req.PowerRequested}
	resp.Interval = req.IntervalRequested
	return resp, nil
}

// createFlowReservation is the POST of a FlowReservationRequest: the
// server stores it and answers it with the FlowReservationResponse of its
// FlowPolicy, an event in the FlowReservationResponseList of the device.
func (s *Server) createFlowReservation(ctx context.Context, r *Request) (string, error) {
	req, ok := r.Doc.(*ieee20305.FlowReservationRequest)
	if !ok {
		return s.createDefault(ctx, r)
	}
	href, err := s.createDefault(ctx, r)
	if err != nil {
		return "", err
	}
	policy := s.cfg.FlowPolicy
	if policy == nil {
		policy = GrantAll
	}
	resp, err := policy(ctx, r.Node.Owner, req)
	if err != nil || resp == nil {
		return href, err
	}
	responses, err := s.store.Get(ctx, path.Join(path.Dir(r.Node.Href), s.partPath("FlowReservationResponseList")))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(href))
	resp.MRID = ieee20305.MRIDType(sum[:16])
	resp.Subject = req.MRID
	resp.CreationTime = ieee20305.TimeType(s.clock().Unix())
	resp.EventStatus = ieee20305.EventStatus{DateTime: resp.CreationTime}
	if _, err := s.createItem(ctx, responses, resp, responses.Owner); err != nil {
		return "", err
	}
	return href, nil
}
