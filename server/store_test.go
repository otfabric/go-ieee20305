// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"errors"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/server"
)

func TestMemStore(t *testing.T) {
	ctx := context.Background()
	s := server.NewMemStore(nil)
	put := func(href, parent string) {
		t.Helper()
		if err := s.Put(ctx, &server.Node{Href: href, Resource: "EndDevice", Value: examples.EndDevice(0), Parent: parent}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, nil); err == nil {
		t.Error("Put of nil")
	}
	if err := s.Put(ctx, &server.Node{}); err == nil {
		t.Error("Put without an href")
	}
	put("/l", "")
	put("/l/1", "/l")
	put("/l/1/sub", "/l/1")
	put("/other", "")
	for _, m := range []string{"/l/1", "/l/1/sub", "/l/1"} {
		if err := s.AddMember(ctx, "/l", m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMember(ctx, "/other", "/l/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, "/missing", "/l/1"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("AddMember to a missing list: %v", err)
	}
	if err := s.AddMember(ctx, "/l", "/missing"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("AddMember of a missing node: %v", err)
	}
	if ms, _ := s.Members(ctx, "/l"); len(ms) != 2 {
		t.Errorf("%d members, want 2 (no duplicates)", len(ms))
	}
	// The store hands out copies.
	n, _ := s.Get(ctx, "/l/1")
	n.Value.(*ieee20305.EndDevice).SFDI = 99
	if again, _ := s.Get(ctx, "/l/1"); again.Value.(*ieee20305.EndDevice).SFDI == 99 {
		t.Error("Get shares the value it holds")
	}
	if err := s.RemoveMember(ctx, "/l", "/l/1/sub"); err != nil {
		t.Error(err)
	}
	if err := s.RemoveMember(ctx, "/l", "/l/1/sub"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("RemoveMember twice: %v", err)
	}
	// Deleting a node deletes what was created in it, and its memberships.
	if err := s.Delete(ctx, "/l/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "/l/1/sub"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("a child outlived its parent: %v", err)
	}
	if ms, _ := s.Members(ctx, "/other"); len(ms) != 0 {
		t.Errorf("a deleted node is still a member: %d", len(ms))
	}
	if err := s.Delete(ctx, "/l/1"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("Delete twice: %v", err)
	}
	if _, err := s.Members(ctx, "/missing"); !errors.Is(err, server.ErrNoNode) {
		t.Errorf("Members of a missing list: %v", err)
	}
	if hrefs, _ := s.Hrefs(ctx); len(hrefs) != 2 || hrefs[0] != "/l" || hrefs[1] != "/other" {
		t.Errorf("Hrefs = %v", hrefs)
	}
}

// A node put again with another Parent goes with its new parent, not its
// old; a deleted list leaves its members, which may join lists again.
func TestMemStoreParentsAndLists(t *testing.T) {
	ctx := context.Background()
	s := server.NewMemStore(nil)
	put := func(href, parent string) {
		t.Helper()
		if err := s.Put(ctx, &server.Node{Href: href, Resource: "EndDevice", Value: examples.EndDevice(0), Parent: parent}); err != nil {
			t.Fatal(err)
		}
	}
	put("/a", "")
	put("/b", "")
	put("/x", "/a")
	put("/x/1", "/x")
	put("/x", "/b")
	if err := s.Delete(ctx, "/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "/x/1"); err != nil {
		t.Errorf("a node re-parented went with its old parent: %v", err)
	}
	if err := s.AddMember(ctx, "/b", "/x/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "/b"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"/x", "/x/1"} {
		if _, err := s.Get(ctx, h); !errors.Is(err, server.ErrNoNode) {
			t.Errorf("%s outlived its parent: %v", h, err)
		}
	}
	put("/l", "")
	put("/m", "")
	put("/i", "")
	for _, l := range []string{"/l", "/m"} {
		if err := s.AddMember(ctx, l, "/i"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(ctx, "/l"); err != nil {
		t.Fatal(err)
	}
	// /i is in no deleted list now: adding it to a new /l works, and
	// deleting /i empties /m and /l.
	put("/l", "")
	if err := s.AddMember(ctx, "/l", "/i"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Members(ctx, "/l"); len(ms) != 1 {
		t.Errorf("%d members of a new list", len(ms))
	}
	if err := s.RemoveMember(ctx, "/m", "/i"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, "/m", "/i"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "/i"); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"/l", "/m"} {
		if ms, _ := s.Members(ctx, l); len(ms) != 0 {
			t.Errorf("%s keeps a deleted member", l)
		}
	}
}
