// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"reflect"
	"strings"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

var listLinkType = reflect.TypeOf(ieee20305.ListLink{})

// dressLinks prepares the links of a document the server sends to p:
//
//   - a ListLink gets its all attribute, the number of items of the list p
//     reads, which IEEE 2030.5 requires of a local link;
//   - a link to a resource that does not exist yet, which p may not create
//     with PUT (the DefaultDERControl of a program without one, say), is
//     left out: a client would GET it in vain.
//
// It goes through the document, its lists' items and nested structures.
func (s *Server) dressLinks(ctx context.Context, p Principal, doc any) {
	v := reflect.ValueOf(doc)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	s.dress(ctx, p, v.Elem(), 0)
}

func (s *Server) dress(ctx context.Context, p Principal, v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			s.dress(ctx, p, v.Elem(), depth+1)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Struct || v.Type().Elem().Kind() == reflect.Pointer {
			for i := 0; i < v.Len(); i++ {
				s.dress(ctx, p, v.Index(i), depth+1)
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := v.Field(i)
			if strings.HasSuffix(f.Name, "Link") && f.Name != "Link" && f.Name != "ListLink" {
				s.dressLink(ctx, p, fv)
				continue
			}
			switch fv.Kind() {
			case reflect.Struct, reflect.Pointer, reflect.Slice:
				s.dress(ctx, p, fv, depth+1)
			default:
			}
		}
	default:
	}
}

// dressLink prepares one link field (a struct, or a pointer to one, that
// embeds Link or ListLink).
func (s *Server) dressLink(ctx context.Context, p Principal, f reflect.Value) {
	link := f
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return
		}
		link = f.Elem()
	}
	if link.Kind() != reflect.Struct {
		return
	}
	href := linkHref(link)
	if href == "" || !strings.HasPrefix(href, "/") {
		return
	}
	n, err := s.store.Get(ctx, href)
	if err != nil {
		return
	}
	if n.Value == nil && s.hooks[n.Resource].Get == nil && !s.canChange(p, n) {
		if f.Kind() == reflect.Pointer && f.CanSet() {
			f.Set(reflect.Zero(f.Type()))
		}
		return
	}
	ll := link.FieldByName("ListLink")
	if !ll.IsValid() || ll.Type() != listLinkType || !ll.CanAddr() {
		return
	}
	all := ieee20305.UInt32(s.count(ctx, p, n))
	ll.Addr().Interface().(*ieee20305.ListLink).All = &all
}

// linkHref returns the href of a link struct.
func linkHref(link reflect.Value) string {
	for _, name := range []string{"ListLink", "Link"} {
		f := link.FieldByName(name)
		if !f.IsValid() || !f.CanAddr() {
			continue
		}
		switch l := f.Addr().Interface().(type) {
		case *ieee20305.ListLink:
			return l.Href
		case *ieee20305.Link:
			return l.Href
		}
	}
	return ""
}

// count returns the number of items of the list node that p reads.
func (s *Server) count(ctx context.Context, p Principal, n *Node) int {
	if h := s.hooks[n.Resource].Get; h != nil {
		doc, err := h(ctx, &Request{Principal: p, Node: n, Spec: s.resources[n.Resource], Query: Query{HasLimit: true}})
		if err != nil {
			return 0
		}
		if l, ok := doc.(ieee20305.Lister); ok {
			return int(listAll(l))
		}
		return 0
	}
	if c, ok := s.base().(memberCounter); ok {
		n, err := c.countMembers(n.Href, func(owner string, hasValue bool) bool {
			return hasValue && s.canRead(p, &Node{Owner: owner})
		})
		if err == nil {
			return n
		}
	}
	members, err := s.store.Members(ctx, n.Href)
	if err != nil && !errors.Is(err, ErrNoNode) {
		return 0
	}
	c := 0
	for _, m := range members {
		if m.Value != nil && s.canRead(p, m) {
			c++
		}
	}
	return c
}

// listAll returns the all attribute of a list document.
func listAll(l ieee20305.Lister) uint32 {
	v := reflect.ValueOf(l)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return 0
	}
	if f := v.Elem().FieldByName("All"); f.IsValid() && f.CanUint() {
		return uint32(f.Uint())
	}
	return 0
}

// base returns the store the server wraps.
func (s *Server) base() Store {
	if w, ok := s.store.(*watchStore); ok {
		return w.Store
	}
	return s.store
}

// memberCounter is a store that counts the items of a list without copying
// them.
type memberCounter interface {
	countMembers(list string, keep func(owner string, hasValue bool) bool) (int, error)
}

func (s *memStore) countMembers(list string, keep func(owner string, hasValue bool) bool) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.nodes[list]; !ok {
		return 0, ErrNoNode
	}
	c := 0
	for _, m := range s.members[list] {
		if n, ok := s.nodes[m]; ok && keep(n.Owner, n.Value != nil) {
			c++
		}
	}
	return c, nil
}
