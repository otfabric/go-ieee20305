// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// ErrNoNode is returned by a Store for an href it does not hold. It
// matches ieee20305.ErrNotFound.
var ErrNoNode = fmt.Errorf("%w: server: no such resource", ieee20305.ErrNotFound)

// Node is a resource of the server: its href, its WADL resource, its value
// and who may read it.
type Node struct {
	// Href is the path of the resource, such as "/edev/3".
	Href string
	// Resource is the ID of its WADL resource, such as "EndDevice": see
	// ieee20305.LookupResource.
	Resource string
	// Value is a pointer to the document of the resource, such as
	// *ieee20305.EndDevice; for a list, the list document whose attributes
	// (pollRate, subscribable) the server keeps, without items. Nil for a
	// resource that is linked to but not created yet, such as the
	// DERSettings of a DER before its device puts them: GET answers 404,
	// and PUT creates it.
	Value any
	// Owner is the LFDI (upper-case hexadecimal) of the device the resource
	// belongs to, or empty for a resource every client may read.
	Owner string
	// Parent is the href of the resource or list the resource was created
	// in, if any. Deleting a node deletes the nodes whose Parent it is.
	Parent string
	// Modified is when the resource last changed.
	Modified time.Time
}

// copyNode returns a copy of n whose value is a deep copy made by clone.
func copyNode(n *Node, clone func(any) any) *Node {
	c := *n
	if n.Value != nil {
		if v := clone(n.Value); v != nil {
			c.Value = v
		}
	}
	return &c
}

// Store holds the resources of a server. Its methods are safe for
// concurrent use, and it never hands out a value it holds itself: Get and
// Members return copies, and Put stores a copy.
type Store interface {
	// Get returns the node at href, or an error matching ErrNoNode.
	Get(ctx context.Context, href string) (*Node, error)
	// Put inserts or replaces the node at n.Href.
	Put(ctx context.Context, n *Node) error
	// Delete removes the node at href, the nodes created in it
	// (recursively), and its membership in lists.
	Delete(ctx context.Context, href string) error
	// AddMember makes the node at member an item of the list at list; a
	// member may belong to several lists.
	AddMember(ctx context.Context, list, member string) error
	// RemoveMember removes member from the list at list.
	RemoveMember(ctx context.Context, list, member string) error
	// Members returns the items of the list at list, in the order they were
	// added.
	Members(ctx context.Context, list string) ([]*Node, error)
	// Hrefs returns the hrefs of every node, sorted.
	Hrefs(ctx context.Context) ([]string, error)
}

// OwnerCounter is a Store that knows how many nodes an owner has without
// reading them, as the store of NewMemStore does. A server counts the
// resources of a device with it (Config.MaxDeviceResources). With a Store
// that is no OwnerCounter the server counts them from Hrefs and Get: a
// pass over the store for each resource a client creates.
type OwnerCounter interface {
	// CountOwned returns the number of nodes whose Owner is owner.
	CountOwned(ctx context.Context, owner string) (int, error)
}

// NewMemStore returns a Store that keeps everything in memory. clone makes
// the deep copies of values: ieee20305.Clone when nil, or the Clone of a
// profile package, such as csipaus.Clone, for its documents. It is an
// OwnerCounter.
func NewMemStore(clone func(any) any) Store {
	if clone == nil {
		clone = ieee20305.Clone
	}
	return &memStore{nodes: map[string]*Node{}, members: map[string][]string{}, clone: clone,
		kids: map[string]map[string]struct{}{}, lists: map[string]map[string]struct{}{}, owned: map[string]int{}}
}

type memStore struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	members map[string][]string
	clone   func(any) any
	// kids are the hrefs of the nodes created in a node (those whose
	// Parent it is), and lists those of the lists a node is an item of,
	// so that Delete looks only at the nodes it removes and the lists
	// they are in. owned counts the nodes of each owner.
	kids  map[string]map[string]struct{}
	lists map[string]map[string]struct{}
	owned map[string]int
	// visited counts the nodes Delete looked at, for the tests.
	visited uint64
}

func (s *memStore) Get(_ context.Context, href string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[href]
	if !ok {
		return nil, ErrNoNode
	}
	return copyNode(n, s.clone), nil
}

func (s *memStore) Put(_ context.Context, n *Node) error {
	if n == nil || n.Href == "" {
		return fmt.Errorf("%w: server: a node needs an href", ieee20305.ErrInvalid)
	}
	c := copyNode(n, s.clone)
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.nodes[c.Href]; ok {
		s.unlink(old)
	}
	s.nodes[c.Href] = c
	if c.Parent != "" {
		add(s.kids, c.Parent, c.Href)
	}
	if c.Owner != "" {
		s.owned[c.Owner]++
	}
	return nil
}

// unlink takes n out of the nodes of its parent and of its owner. The
// caller holds the lock.
func (s *memStore) unlink(n *Node) {
	if n.Parent != "" {
		remove(s.kids, n.Parent, n.Href)
	}
	if n.Owner != "" {
		if s.owned[n.Owner]--; s.owned[n.Owner] <= 0 {
			delete(s.owned, n.Owner)
		}
	}
}

// add adds v to the set of key in sets.
func add(sets map[string]map[string]struct{}, key, v string) {
	set := sets[key]
	if set == nil {
		set = map[string]struct{}{}
		sets[key] = set
	}
	set[v] = struct{}{}
}

// remove removes v from the set of key in sets, and the set when it is
// empty.
func remove(sets map[string]map[string]struct{}, key, v string) {
	set := sets[key]
	delete(set, v)
	if len(set) == 0 {
		delete(sets, key)
	}
}

func (s *memStore) Delete(_ context.Context, href string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[href]; !ok {
		return ErrNoNode
	}
	// The node and what was created in it, a parent before its children.
	doomed := []string{href}
	seen := map[string]struct{}{href: {}}
	for i := 0; i < len(doomed); i++ {
		for kid := range s.kids[doomed[i]] {
			if _, ok := seen[kid]; !ok {
				seen[kid] = struct{}{}
				doomed = append(doomed, kid)
			}
		}
	}
	for _, h := range doomed {
		s.visited++
		s.unlink(s.nodes[h])
		delete(s.nodes, h)
		// Its items are in one list fewer: a list goes before the items
		// created in it, which then have no membership left to undo.
		for _, m := range s.members[h] {
			remove(s.lists, m, h)
		}
		delete(s.members, h)
		for list := range s.lists[h] {
			ms := s.members[list]
			if i := slices.Index(ms, h); i >= 0 {
				s.members[list] = append(ms[:i:i], ms[i+1:]...)
			}
		}
		delete(s.lists, h)
	}
	return nil
}

func (s *memStore) AddMember(_ context.Context, list, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[list]; !ok {
		return ErrNoNode
	}
	if _, ok := s.nodes[member]; !ok {
		return ErrNoNode
	}
	if _, ok := s.lists[member][list]; ok {
		return nil
	}
	s.members[list] = append(s.members[list], member)
	add(s.lists, member, list)
	return nil
}

func (s *memStore) RemoveMember(_ context.Context, list, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.members[list]
	for i, m := range ms {
		if m == member {
			s.members[list] = append(ms[:i:i], ms[i+1:]...)
			remove(s.lists, member, list)
			return nil
		}
	}
	return ErrNoNode
}

// CountOwned returns the number of nodes of owner.
func (s *memStore) CountOwned(_ context.Context, owner string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owned[owner], nil
}

func (s *memStore) Members(_ context.Context, list string) ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.nodes[list]; !ok {
		return nil, ErrNoNode
	}
	out := make([]*Node, 0, len(s.members[list]))
	for _, m := range s.members[list] {
		if n, ok := s.nodes[m]; ok {
			out = append(out, copyNode(n, s.clone))
		}
	}
	return out, nil
}

func (s *memStore) Hrefs(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.nodes))
	for h := range s.nodes {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}
