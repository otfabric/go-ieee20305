// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
)

// TestPartCount: the parts the server counts for a new item, against the
// cap of a device, are the nodes it makes with the item: for every resource
// that has parts, for the parts of a profile that are resources, and for
// the items of an AssociatedDERProgramList, which get none.
func TestPartCount(t *testing.T) {
	ctx := context.Background()
	p := StandardProfile()
	p.Parts = map[string][]string{"LogEvent": {"NoSuchResource"}, "UsagePoint": {"NoSuchResource"}}
	s, err := New(Config{Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := s.Admin()
	lists := []string{"LogEventList", "ReadingList", "AssociatedDERProgramList"}
	for _, resource := range slices.Sorted(maps.Keys(itemParts)) {
		lists = append(lists, resource+"List")
	}
	for i, list := range lists {
		if _, ok := s.resources[list]; !ok {
			continue // a resource whose items are in no list of its name
		}
		href := fmt.Sprintf("/l%d", i)
		if err := a.Put(ctx, &Node{Href: href, Resource: list, Value: a.Document(list)}); err != nil {
			t.Fatalf("%s: %v", list, err)
		}
		before, _ := s.store.Hrefs(ctx)
		if _, err := a.Create(ctx, href, a.Document(itemResource(list)), ""); err != nil {
			t.Fatalf("%s: %v", list, err)
		}
		after, _ := s.store.Hrefs(ctx)
		if got, want := len(after)-len(before), 1+s.partCount(list); got != want {
			t.Errorf("an item of a %s is %d nodes, and counts as %d", list, got, want)
		}
	}
	for list, want := range map[string]int{"EndDeviceList": 15, "DERList": 7, "DERProgramList": 4, "UsagePointList": 1,
		"LogEventList": 0, "AssociatedDERProgramList": 0, "MirrorUsagePointList": 0} {
		if got := s.partCount(list); got != want {
			t.Errorf("partCount(%s) = %d, want %d", list, got, want)
		}
	}
	// A part of a profile that is a resource counts.
	p = StandardProfile()
	p.Parts = map[string][]string{"LogEvent": {"DeviceStatus", "NoSuchResource"}}
	s2, err := New(Config{Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if got := s2.partCount("LogEventList"); got != 1 {
		t.Errorf("partCount(LogEventList) with a part of the profile = %d, want 1", got)
	}
}
