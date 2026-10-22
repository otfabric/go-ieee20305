// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Admin manages the resources of a server from Go, as its operator: it
// bypasses access control and the conformance of methods, and goes through
// the same store. Tests and applications seed a server with it.
type Admin struct{ s *Server }

// Admin returns the administration of the server.
func (s *Server) Admin() *Admin { return &Admin{s: s} }

// Get returns the node at href.
func (a *Admin) Get(ctx context.Context, href string) (*Node, error) {
	return a.s.store.Get(ctx, href)
}

// Put stores n, setting the href of its value. The EventStatus of an event
// stays the server's, unless n cancels it (use CancelEvent).
func (a *Admin) Put(ctx context.Context, n *Node) error {
	if _, ok := a.s.resources[n.Resource]; !ok {
		return fmt.Errorf("%w: %s is no resource", ieee20305.ErrInvalid, n.Resource)
	}
	if n.Value != nil {
		setHref(n.Value, n.Href)
	}
	if n.Modified.IsZero() {
		n.Modified = a.s.clock()
	}
	return a.s.putNode(ctx, n)
}

// Delete deletes the node at href and those created in it.
func (a *Admin) Delete(ctx context.Context, href string) error {
	return a.s.store.Delete(ctx, href)
}

// Create creates an item of the list at list, owned by owner (an LFDI, or
// empty for every client), and returns its href.
func (a *Admin) Create(ctx context.Context, list string, v any, owner string) (string, error) {
	if rv := reflect.ValueOf(v); !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return "", fmt.Errorf("%w: no document to create in %s", ieee20305.ErrInvalid, list)
	}
	n, err := a.s.store.Get(ctx, list)
	if err != nil {
		return "", fmt.Errorf("%s: %w", list, err)
	}
	if !isList(n.Value) {
		return "", fmt.Errorf("%w: %s is not a list", ieee20305.ErrInvalid, list)
	}
	return a.s.createItem(ctx, n, v, owner)
}

// AddMember makes the resource at member an item of the list at list too.
func (a *Admin) AddMember(ctx context.Context, list, member string) error {
	return a.s.store.AddMember(ctx, list, member)
}

// Root creates a top-level resource of every client, such as the
// EndDeviceList at /edev, and links it from DeviceCapability. v is its
// document: for a list, the list document whose attributes (pollRate)
// the server keeps.
func (a *Admin) Root(ctx context.Context, resource, href string, v any) error {
	if err := a.Put(ctx, &Node{Href: href, Resource: resource, Value: v}); err != nil {
		return err
	}
	dcap, err := a.s.store.Get(ctx, "/dcap")
	if err != nil {
		return err
	}
	if err := setLink(dcap.Value, resource+"Link", href); err != nil {
		return err
	}
	return a.s.store.Put(ctx, dcap)
}

// NewList creates an empty list resource at href, an item or a part of the
// resource parent, owned by owner, and links it from the field link of the
// document of parent (such as "FunctionSetAssignmentsListLink"), when link
// is not empty.
func (a *Admin) NewList(ctx context.Context, resource, href, parent, link, owner string) error {
	spec, ok := a.s.resources[resource]
	if !ok {
		return fmt.Errorf("%w: %s is no resource", ieee20305.ErrInvalid, resource)
	}
	var elem string
	for _, m := range spec.Methods {
		if m.Method == "GET" {
			elem = m.Response
		}
	}
	v := a.s.profile.NewElement(qname(elem))
	if v == nil {
		return fmt.Errorf("%w: no document element %s for %s", ieee20305.ErrInvalid, elem, resource)
	}
	if err := a.Put(ctx, &Node{Href: href, Resource: resource, Value: v, Owner: owner, Parent: parent}); err != nil {
		return err
	}
	if link == "" {
		return nil
	}
	pn, err := a.s.store.Get(ctx, parent)
	if err != nil {
		return err
	}
	if err := setLink(pn.Value, link, href); err != nil {
		return err
	}
	return a.s.store.Put(ctx, pn)
}

// setLink sets the link field name (such as "EndDeviceListLink") of the
// document v to href, creating the link.
func setLink(v any, name, href string) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: no document to link from", ieee20305.ErrInvalid)
	}
	f := rv.Elem().FieldByName(name)
	if !f.IsValid() || !f.CanSet() {
		return fmt.Errorf("%w: %T has no link %s", ieee20305.ErrInvalid, v, name)
	}
	var link reflect.Value
	switch f.Kind() {
	case reflect.Pointer:
		link = reflect.New(f.Type().Elem())
		f.Set(link)
	case reflect.Struct:
		link = f.Addr()
	default:
		return fmt.Errorf("%w: %T.%s is no link", ieee20305.ErrInvalid, v, name)
	}
	h := link.Elem().FieldByName("Href")
	if !h.IsValid() || h.Kind() != reflect.String {
		return fmt.Errorf("%w: %T.%s is no link", ieee20305.ErrInvalid, v, name)
	}
	h.SetString(href)
	return nil
}

// keepLinks sets the links of the document dst (its fields named *Link,
// through embedding) to those of src, a document of the same type: the
// links a client sends are not its to make.
func keepLinks(dst, src any) {
	d, s := reflect.ValueOf(dst), reflect.ValueOf(src)
	if d.Kind() != reflect.Pointer || s.Kind() != reflect.Pointer || d.IsNil() || s.IsNil() || d.Type() != s.Type() {
		return
	}
	copyLinks(d.Elem(), s.Elem())
}

func copyLinks(d, s reflect.Value) {
	if d.Kind() != reflect.Struct {
		return
	}
	t := d.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		switch {
		case f.Anonymous:
			copyLinks(d.Field(i), s.Field(i))
		case f.IsExported() && strings.HasSuffix(f.Name, "Link") && (f.Type.Kind() == reflect.Pointer || f.Type.Kind() == reflect.Struct):
			d.Field(i).Set(s.Field(i))
		}
	}
}

// Document returns a new, empty document of the WADL resource, such as an
// *ieee20305.DemandResponseProgramList for DemandResponseProgramList, or
// nil.
func (a *Admin) Document(resource string) any { return a.s.newList(resource) }

// Members returns the items of the list at list.
func (a *Admin) Members(ctx context.Context, list string) ([]*Node, error) {
	return a.s.store.Members(ctx, list)
}

// Now returns the time of the server's Clock.
func (a *Admin) Now() time.Time { return a.s.clock() }
