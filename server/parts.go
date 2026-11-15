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
		"SubscriptionList", "DERList", "FlowReservationRequestList", "FlowReservationResponseList",
		"AggregatedDeviceList", "AggregationPriority"},
	"DER": {"DERCapability", "DERSettings", "DERStatus", "DERAvailability", "AssociatedDERProgramList",
		"CurrentDERProgram", "AssociatedUsagePoint"},
	"DERProgram":            {"DERControlList", "ActiveDERControlList", "DERCurveList", "DefaultDERControl"},
	"DemandResponseProgram": {"EndDeviceControlList", "ActiveEndDeviceControlList"},
	"MessagingProgram":      {"TextMessageList", "ActiveTextMessageList"},
	"TariffProfile":         {"RateComponentList"},
	"RateComponent":         {"TimeTariffIntervalList", "ActiveTimeTariffIntervalList"},
	"TimeTariffInterval":    {"ConsumptionTariffIntervalList"},
	"ResponseSet":           {"ResponseList"},
	"UsagePoint":            {"MeterReadingList"},
	"MeterReading":          {"ReadingSetList", "ReadingType"},
	"ReadingSet":            {"ReadingList"},
	"CustomerAccount":       {"CustomerAgreementList", "ServiceSupplier"},
	"CustomerAgreement": {"BillingPeriodList", "ActiveBillingPeriodList", "ProjectionReadingList",
		"ActiveProjectionReadingList", "TargetReadingList", "ActiveTargetReadingList", "HistoricalReadingList"},
	"Prepayment": {"AccountBalance", "PrepayOperationStatus", "CreditRegisterList", "ActiveCreditRegisterList",
		"SupplyInterruptionOverrideList", "ActiveSupplyInterruptionOverrideList"},
}

// partPath returns the path segment of a part: the last segment of its
// WADL sample path.
func (s *Server) partPath(resource string) string {
	return path.Base(s.resources[resource].SamplePath)
}

// hasParts reports whether the items of the list resource get parts: not
// those of an AssociatedDERProgramList, which are the DERPrograms a DER
// follows, not programs with lists of their own.
func hasParts(list string) bool { return list != "AssociatedDERProgramList" }

// partCount returns the number of parts newParts makes for an item of the
// list resource.
func (s *Server) partCount(list string) int {
	if !hasParts(list) {
		return 0
	}
	resource := itemResource(list)
	n := 0
	for _, p := range itemParts[resource] {
		if _, ok := s.resources[p]; ok {
			n++
		}
	}
	for _, p := range s.profile.Parts[resource] {
		if _, ok := s.resources[p]; ok {
			n++
		}
	}
	return n
}

// newParts sets the links of the document v of a new resource at href to
// its parts, and returns the parts to store, owned by owner.
func (s *Server) newParts(resource, href string, v any, owner string) ([]*Node, error) {
	var parts []*Node
	now := s.clock()
	for _, p := range append(itemParts[resource], s.profile.Parts[resource]...) {
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
// its parts, and returns its href. When capped, the item and its parts
// count against the resources of the owner, and the item is refused when
// they do not fit (room).
func (s *Server) createItem(ctx context.Context, list *Node, v any, owner string, capped bool) (string, error) {
	resource := itemResource(list.Resource)
	if capped && owner != "" {
		defer s.lockOwner(owner)()
		if err := s.room(ctx, owner, 1+s.partCount(list.Resource)); err != nil {
			return "", err
		}
	}
	href := s.newHref(list.Href)
	var parts []*Node
	if hasParts(list.Resource) {
		var err error
		if parts, err = s.newParts(resource, href, v, owner); err != nil {
			return "", err
		}
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
