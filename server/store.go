// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// ErrNoNode is returned by a Store for an href it does not hold.
var ErrNoNode = errors.New("server: no such resource")

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

// NewMemStore returns a Store that keeps everything in memory. clone makes
// the deep copies of values: ieee20305.Clone when nil, or the Clone of a
// profile package, such as csipaus.Clone, for its documents.
func NewMemStore(clone func(any) any) Store {
	if clone == nil {
		clone = ieee20305.Clone
	}
	return &memStore{nodes: map[string]*Node{}, members: map[string][]string{}, clone: clone}
}

type memStore struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	members map[string][]string
	clone   func(any) any
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
		return errors.New("server: a node needs an href")
	}
	c := copyNode(n, s.clone)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[c.Href] = c
	return nil
}

func (s *memStore) Delete(_ context.Context, href string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[href]; !ok {
		return ErrNoNode
	}
	doomed := map[string]bool{href: true}
	for changed := true; changed; {
		changed = false
		for h, n := range s.nodes {
			if !doomed[h] && doomed[n.Parent] {
				doomed[h] = true
				changed = true
			}
		}
	}
	for h := range doomed {
		delete(s.nodes, h)
		delete(s.members, h)
	}
	for list, ms := range s.members {
		kept := ms[:0]
		for _, m := range ms {
			if !doomed[m] {
				kept = append(kept, m)
			}
		}
		s.members[list] = kept
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
	for _, m := range s.members[list] {
		if m == member {
			return nil
		}
	}
	s.members[list] = append(s.members[list], member)
	return nil
}

func (s *memStore) RemoveMember(_ context.Context, list, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.members[list]
	for i, m := range ms {
		if m == member {
			s.members[list] = append(ms[:i:i], ms[i+1:]...)
			return nil
		}
	}
	return ErrNoNode
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
