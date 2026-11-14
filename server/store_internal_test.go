// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
)

// model is a store that does what the documentation of Store says in the
// plainest way: Delete looks at every node until no node is left whose
// parent is gone, and at every list.
type model struct {
	nodes   map[string]Node
	members map[string][]string
}

func (m *model) put(href, parent, owner string) {
	m.nodes[href] = Node{Href: href, Parent: parent, Owner: owner}
}

func (m *model) delete(href string) bool {
	if _, ok := m.nodes[href]; !ok {
		return false
	}
	doomed := map[string]bool{href: true}
	for changed := true; changed; {
		changed = false
		for h, n := range m.nodes {
			if !doomed[h] && doomed[n.Parent] {
				doomed[h], changed = true, true
			}
		}
	}
	for h := range doomed {
		delete(m.nodes, h)
		delete(m.members, h)
	}
	for list, ms := range m.members {
		m.members[list] = slices.DeleteFunc(ms, func(h string) bool { return doomed[h] })
	}
	return true
}

func (m *model) addMember(list, member string) bool {
	if _, ok := m.nodes[list]; !ok {
		return false
	}
	if _, ok := m.nodes[member]; !ok {
		return false
	}
	if !slices.Contains(m.members[list], member) {
		m.members[list] = append(m.members[list], member)
	}
	return true
}

func (m *model) removeMember(list, member string) bool {
	i := slices.Index(m.members[list], member)
	if i < 0 {
		return false
	}
	m.members[list] = slices.Delete(m.members[list], i, i+1)
	return true
}

// same fails t unless s holds what m holds, and the indexes of s are
// those of its nodes and lists.
func (m *model) same(t *testing.T, s *memStore, step string) {
	t.Helper()
	ctx := context.Background()
	hrefs, _ := s.Hrefs(ctx)
	if want := slices.Sorted(maps.Keys(m.nodes)); !slices.Equal(hrefs, want) {
		t.Fatalf("%s: nodes %v, want %v", step, hrefs, want)
	}
	owned := map[string]int{}
	kids, lists := map[string][]string{}, map[string][]string{}
	for href, want := range m.nodes {
		n, err := s.Get(ctx, href)
		if err != nil || n.Parent != want.Parent || n.Owner != want.Owner {
			t.Fatalf("%s: node %s = %+v, %v; want %+v", step, href, n, err, want)
		}
		if want.Owner != "" {
			owned[want.Owner]++
		}
		if want.Parent != "" {
			kids[want.Parent] = append(kids[want.Parent], href)
		}
		ms, err := s.Members(ctx, href)
		if err != nil {
			t.Fatalf("%s: Members(%s): %v", step, href, err)
		}
		var got []string
		for _, mn := range ms {
			got = append(got, mn.Href)
		}
		if !slices.Equal(got, m.members[href]) {
			t.Fatalf("%s: members of %s: %v, want %v", step, href, got, m.members[href])
		}
		for _, member := range m.members[href] {
			lists[member] = append(lists[member], href)
		}
	}
	for _, owner := range []string{"", "A", "B", "C"} {
		if n, err := s.CountOwned(ctx, owner); err != nil || n != owned[owner] {
			t.Fatalf("%s: CountOwned(%q) = %d, %v; want %d", step, owner, n, err, owned[owner])
		}
	}
	if !maps.Equal(s.owned, owned) {
		t.Fatalf("%s: owner index %v, want %v", step, s.owned, owned)
	}
	sameSets(t, step+": children", s.kids, kids)
	sameSets(t, step+": lists of the members", s.lists, lists)
}

func sameSets(t *testing.T, what string, got map[string]map[string]struct{}, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d keys, want %d: %v, %v", what, len(got), len(want), got, want)
	}
	for key, set := range got {
		if g, w := slices.Sorted(maps.Keys(set)), slices.Sorted(slices.Values(want[key])); !slices.Equal(g, w) {
			t.Fatalf("%s of %s: %v, want %v", what, key, g, w)
		}
	}
}

// TestMemStoreAgainstModel runs the same puts, deletes and memberships on
// the memory store and on the model: nodes that change their parent or
// owner, parents that do not exist, are their own or form a cycle, lists
// that are their own members, and deletions of lists, items and both.
func TestMemStoreAgainstModel(t *testing.T) {
	ctx := context.Background()
	hrefs := []string{"/a", "/b", "/c", "/a/1", "/a/2", "/a/1/x", "/b/1", "/b/1/y", "/b/1/y/z", "/c/1", "/d", "/e"}
	parents := append([]string{"", "", "/none"}, hrefs...)
	owners := []string{"", "A", "B", "C"}
	for seed := range uint64(8) {
		rng := rand.New(rand.NewPCG(seed, 20305))
		s := NewMemStore(nil).(*memStore)
		m := &model{nodes: map[string]Node{}, members: map[string][]string{}}
		pick := func(from []string) string { return from[rng.IntN(len(from))] }
		for i := range 4000 {
			var step string
			switch op := rng.IntN(10); {
			case op < 4:
				href, parent, owner := pick(hrefs), pick(parents), pick(owners)
				step = fmt.Sprintf("seed %d, step %d: Put(%s, parent %s, owner %q)", seed, i, href, parent, owner)
				if err := s.Put(ctx, &Node{Href: href, Parent: parent, Owner: owner}); err != nil {
					t.Fatalf("%s: %v", step, err)
				}
				m.put(href, parent, owner)
			case op < 6:
				href := pick(hrefs)
				step = fmt.Sprintf("seed %d, step %d: Delete(%s)", seed, i, href)
				if err := s.Delete(ctx, href); (err == nil) != m.delete(href) || (err != nil && !errors.Is(err, ErrNoNode)) {
					t.Fatalf("%s: %v", step, err)
				}
			case op < 9:
				list, member := pick(hrefs), pick(hrefs)
				step = fmt.Sprintf("seed %d, step %d: AddMember(%s, %s)", seed, i, list, member)
				if err := s.AddMember(ctx, list, member); (err == nil) != m.addMember(list, member) || (err != nil && !errors.Is(err, ErrNoNode)) {
					t.Fatalf("%s: %v", step, err)
				}
			default:
				list, member := pick(hrefs), pick(hrefs)
				step = fmt.Sprintf("seed %d, step %d: RemoveMember(%s, %s)", seed, i, list, member)
				if err := s.RemoveMember(ctx, list, member); (err == nil) != m.removeMember(list, member) || (err != nil && !errors.Is(err, ErrNoNode)) {
					t.Fatalf("%s: %v", step, err)
				}
			}
			m.same(t, s, step)
		}
	}
}

// TestMemStoreDeleteCost: Delete looks at the nodes it removes, not at the
// store: an item of a list at its cap goes at the same cost in a store of
// a few nodes and in one of thousands, as when the server trims a list.
func TestMemStoreDeleteCost(t *testing.T) {
	ctx := context.Background()
	for _, others := range []int{0, 5000} {
		s := NewMemStore(nil).(*memStore)
		put := func(href, parent string) {
			t.Helper()
			if err := s.Put(ctx, &Node{Href: href, Parent: parent, Owner: "A"}); err != nil {
				t.Fatal(err)
			}
		}
		put("/l", "")
		for i := range 1000 {
			item := fmt.Sprintf("/l/%d", i)
			put(item, "/l")
			put(item+"/part", item)
			if err := s.AddMember(ctx, "/l", item); err != nil {
				t.Fatal(err)
			}
		}
		for i := range others {
			put(fmt.Sprintf("/other/%d", i), "/other")
		}
		for i := range 10 {
			before := s.visited
			if err := s.Delete(ctx, fmt.Sprintf("/l/%d", i)); err != nil {
				t.Fatal(err)
			}
			if got := s.visited - before; got != 2 {
				t.Fatalf("with %d other nodes: Delete of an item and its part looked at %d nodes, want 2", others, got)
			}
		}
		if ms, err := s.Members(ctx, "/l"); err != nil || len(ms) != 990 || ms[0].Href != "/l/10" {
			t.Fatalf("with %d other nodes: %d members left, %v", others, len(ms), err)
		}
		if n, _ := s.CountOwned(ctx, "A"); n != 1+2*990+others {
			t.Fatalf("with %d other nodes: %d nodes of the owner, want %d", others, n, 1+2*990+others)
		}
		// The list goes with its items and their parts, each looked at once.
		before := s.visited
		if err := s.Delete(ctx, "/l"); err != nil {
			t.Fatal(err)
		}
		if got := s.visited - before; got != 1+2*990 {
			t.Fatalf("with %d other nodes: Delete of the list looked at %d nodes, want %d", others, got, 1+2*990)
		}
	}
}

// TestMaxDeviceResourcesDefault: the cap New derives from MaxDeviceResources
// and MaxListItems, at zero, one, negative values and the ends of int.
func TestMaxDeviceResourcesDefault(t *testing.T) {
	for _, tt := range []struct {
		resources, items int
		want             int // 0: no cap
	}{
		{0, 0, 100000},
		{0, 1, 100},
		{0, 7, 700},
		{0, -1, 0},
		{0, math.MinInt, 0},
		{0, math.MaxInt/100 + 1, math.MaxInt / 100 * 100},
		{0, math.MaxInt, math.MaxInt / 100 * 100},
		{1, 0, 1},
		{1, -1, 1},
		{17, 1000, 17},
		{math.MaxInt, 0, math.MaxInt},
		{-1, 0, 0},
		{-1, 7, 0},
		{math.MinInt, -1, 0},
	} {
		s, err := New(Config{MaxDeviceResources: tt.resources, MaxListItems: tt.items})
		if err != nil {
			t.Fatal(err)
		}
		if s.maxOwned != tt.want {
			t.Errorf("MaxDeviceResources %d, MaxListItems %d: cap %d, want %d", tt.resources, tt.items, s.maxOwned, tt.want)
		}
		_ = s.Close()
	}
}

// TestDeviceResourcesOfPlainStore: with a Store that does not count the
// nodes of an owner, the server counts them by reading it, the cap holds
// all the same, and an error of the store fails the request.
func TestDeviceResourcesOfPlainStore(t *testing.T) {
	ctx := context.Background()
	f := newFaultStore(0)
	if _, ok := Store(f).(OwnerCounter); ok {
		t.Fatal("the store of the test counts")
	}
	s, err := New(Config{Store: f, MaxDeviceResources: 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ed, err := s.Admin().ProvisionDevice(ctx, DeviceSpec{LFDI: testLFDI})
	if err != nil {
		t.Fatal(err)
	}
	owner := testLFDI.String()
	want, err := f.Store.(OwnerCounter).CountOwned(ctx, owner)
	if err != nil || want == 0 {
		t.Fatalf("the device owns %d nodes, %v", want, err)
	}
	if got, err := s.owned(ctx, owner); err != nil || got != want {
		t.Fatalf("owned = %d, %v; want %d", got, err, want)
	}
	list, err := s.store.Get(ctx, ed+"/lel")
	if err != nil {
		t.Fatal(err)
	}
	post := func() error {
		_, err := s.createDefault(ctx, &Request{Principal: devicePrincipal(), Node: list, Doc: examples.LogEvent(0)})
		return err
	}
	for i := want; i < 20; i++ {
		if err := post(); err != nil {
			t.Fatalf("POST with %d resources: %v", i, err)
		}
	}
	err = post()
	if he := asHTTPError(err); err == nil || he.Status != http.StatusForbidden || !errors.Is(err, ieee20305.ErrForbidden) {
		t.Fatalf("POST at the cap: %v", err)
	}
	if got, _ := s.owned(ctx, owner); got != 20 || s.Stats().RefusedResources != 1 {
		t.Fatalf("%d resources, %d refused; want 20, 1", got, s.Stats().RefusedResources)
	}
	// The count fails with the store: its first call lists the hrefs, its
	// second reads a node.
	for n := 1; n <= 2; n++ {
		f.arm(n)
		if _, err := s.owned(ctx, owner); !errors.Is(err, errFault) {
			t.Errorf("owned with a store that fails its call %d: %v", n, err)
		}
		f.arm(n)
		if err := post(); !errors.Is(err, errFault) {
			t.Errorf("POST with a store that fails its call %d: %v", n, err)
		}
	}
	f.arm(0)
	if s.Stats().RefusedResources != 1 {
		t.Errorf("%d refused after the store's failures, want 1", s.Stats().RefusedResources)
	}
}
