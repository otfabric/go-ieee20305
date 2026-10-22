// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"path"
	"strings"
)

// itemParts are the resources the server creates with a resource, by WADL
// resource ID: its lists, its computed resources (such as the
// ActiveDERControlList of a DERProgram) and the resources a client creates
// with PUT later (no value yet). Each part is at the last segment of its
// WADL sample path below the resource, linked from the field <ID>Link.
var itemParts = map[string][]string{
	"EndDevice": {"FunctionSetAssignmentsList", "DeviceInformation", "DeviceStatus", "PowerStatus",
		"Configuration", "FileStatus", "LogEventList", "IPInterfaceList", "LoadShedAvailabilityList",
		"SubscriptionList", "DERList", "FlowReservationRequestList", "FlowReservationResponseList"},
	"DER": {"DERCapability", "DERSettings", "DERStatus", "DERAvailability", "AssociatedDERProgramList",
		"CurrentDERProgram", "AssociatedUsagePoint"},
	"DERProgram":            {"DERControlList", "ActiveDERControlList", "DERCurveList", "DefaultDERControl"},
	"DemandResponseProgram": {"EndDeviceControlList", "ActiveEndDeviceControlList"},
	"MessagingProgram":      {"TextMessageList", "ActiveTextMessageList"},
	"TariffProfile":         {"RateComponentList"},
	"RateComponent":         {"TimeTariffIntervalList", "ActiveTimeTariffIntervalList"},
	"ResponseSet":           {"ResponseList"},
	"Prepayment": {"AccountBalance", "PrepayOperationStatus", "CreditRegisterList", "ActiveCreditRegisterList",
		"SupplyInterruptionOverrideList", "ActiveSupplyInterruptionOverrideList"},
}

// partPath returns the path segment of a part: the last segment of its
// WADL sample path.
func (s *Server) partPath(resource string) string {
	return path.Base(s.resources[resource].SamplePath)
}

// newParts sets the links of the document v of a new resource at href to
// its parts, and returns the parts to store, owned by owner.
func (s *Server) newParts(resource, href string, v any, owner string) ([]*Node, error) {
	var parts []*Node
	now := s.clock()
	for _, p := range itemParts[resource] {
		if _, ok := s.resources[p]; !ok {
			continue // not a resource of the profile
		}
		h := path.Join(href, s.partPath(p))
		n := &Node{Href: h, Resource: p, Owner: owner, Parent: href, Modified: now}
		if strings.HasSuffix(p, "List") {
			n.Value = s.newList(p)
			setHref(n.Value, h)
		}
		if err := setLink(v, p+"Link", h); err != nil {
			return nil, err
		}
		parts = append(parts, n)
	}
	return parts, nil
}

// createItem stores v as a new item of the list node, owned by owner, with
// its parts, and returns its href.
func (s *Server) createItem(ctx context.Context, list *Node, v any, owner string) (string, error) {
	href := s.newHref(list.Href)
	resource := itemResource(list.Resource)
	parts, err := s.newParts(resource, href, v, owner)
	if err != nil {
		return "", err
	}
	setHref(v, href)
	n := &Node{Href: href, Resource: resource, Value: v, Owner: owner, Parent: list.Href, Modified: s.clock()}
	if err := s.store.Put(ctx, n); err != nil {
		return "", err
	}
	for _, p := range parts {
		if err := s.store.Put(ctx, p); err != nil {
			return "", err
		}
	}
	if err := s.store.AddMember(ctx, list.Href, href); err != nil {
		return "", err
	}
	return href, nil
}
