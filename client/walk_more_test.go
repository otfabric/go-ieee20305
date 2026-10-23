// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// newPagedClient is a client of a server whose DeviceCapability links:
//   - /edev, an EndDeviceList of 3 that pages by s and l, one item without l;
//   - /lazy, a MirrorUsagePointList of 2 that sends no item without l;
//   - /gone, a link to nothing; and timeLink, when not empty.
//
// The server also has /broken, a list of 2 whose second page fails, /tm, a
// Time 90 s ahead, /junk, a body that is no document, and /down, which
// fails.
func newPagedClient(t *testing.T, cfg client.Config, timeLink string) *client.Client {
	t.Helper()
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s, _ := strconv.Atoi(q.Get("s"))
		l, hasL := 0, q.Has("l")
		if hasL {
			l, _ = strconv.Atoi(q.Get("l"))
		}
		switch r.URL.Path {
		case "/dcap":
			dc := ieee20305.DeviceCapability{EndDeviceListLink: &ieee20305.EndDeviceListLink{}}
			dc.EndDeviceListLink.Href = "/edev"
			dc.SelfDeviceLink = &ieee20305.SelfDeviceLink{}
			dc.SelfDeviceLink.Href = "/gone"
			dc.MirrorUsagePointListLink = &ieee20305.MirrorUsagePointListLink{}
			dc.MirrorUsagePointListLink.Href = "/lazy"
			if timeLink != "" {
				dc.TimeLink = &ieee20305.TimeLink{}
				dc.TimeLink.Href = timeLink
			}
			writeDoc(t, w, http.StatusOK, &dc)
		case "/edev":
			if !hasL {
				l = 1
			}
			list := ieee20305.EndDeviceList{}
			for i := s; i < 3 && i < s+l; i++ {
				var ed ieee20305.EndDevice
				ed.SFDI = ieee20305.SFDIType(i + 1)
				list.EndDevice = append(list.EndDevice, ed)
			}
			list.All, list.Results = 3, ieee20305.UInt32(len(list.EndDevice))
			writeDoc(t, w, http.StatusOK, &list)
		case "/lazy":
			list := ieee20305.MirrorUsagePointList{}
			if hasL {
				for i := s; i < 2 && i < s+l; i++ {
					var m ieee20305.MirrorUsagePoint
					m.MRID = ieee20305.MRIDType{byte(i + 1)}
					list.MirrorUsagePoint = append(list.MirrorUsagePoint, m)
				}
			}
			list.All, list.Results = 2, ieee20305.UInt32(len(list.MirrorUsagePoint))
			writeDoc(t, w, http.StatusOK, &list)
		case "/broken":
			if s > 0 {
				http.Error(w, "down", http.StatusInternalServerError)
				return
			}
			var ed ieee20305.EndDevice
			list := ieee20305.EndDeviceList{EndDevice: []ieee20305.EndDevice{ed}}
			list.All, list.Results = 2, 1
			writeDoc(t, w, http.StatusOK, &list)
		case "/tm":
			writeDoc(t, w, http.StatusOK, &ieee20305.Time{CurrentTime: ieee20305.TimeType(time.Now().Add(90 * time.Second).Unix()), Quality: 7})
		case "/junk":
			w.Header().Set("Content-Type", client.MediaType)
			_, _ = w.Write([]byte("not a document"))
		case "/down":
			http.Error(w, "down", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	return newClient(t, p, srv, cfg)
}

func TestHead(t *testing.T) {
	c := newPagedClient(t, client.Config{}, "")
	ctx := context.Background()
	if err := c.Head(ctx, "/dcap"); err != nil {
		t.Errorf("Head of /dcap: %v", err)
	}
	if err := c.Head(ctx, "/gone"); !errors.Is(err, ieee20305.ErrNotFound) {
		t.Errorf("Head of nothing: %v", err)
	}
}

func TestFetchListAndIterate(t *testing.T) {
	ctx := context.Background()
	c := newPagedClient(t, client.Config{}, "")
	if _, err := c.FetchList(ctx, "/junk", client.ListOptions{}); err == nil {
		t.Error("FetchList of a body that is no document")
	}
	var sfdis []int
	if err := c.Iterate(ctx, "/edev", 2, func(it any) error {
		sfdis = append(sfdis, int(it.(*ieee20305.EndDevice).SFDI))
		return nil
	}); err != nil || len(sfdis) != 3 || sfdis[2] != 3 {
		t.Errorf("Iterate by pages of 2: %v, %v", sfdis, err)
	}
	// More items than Config.MaxListItems: the server may be lying about all.
	short := newPagedClient(t, client.Config{MaxListItems: 2}, "")
	if err := short.Iterate(ctx, "/edev", 1, func(any) error { return nil }); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Iterate past MaxListItems: %v", err)
	}
}

func TestWalkLists(t *testing.T) {
	ctx := context.Background()
	c := newPagedClient(t, client.Config{}, "")
	got := map[string]int{}
	err := c.Walk(ctx, "/dcap", client.WalkOptions{PageSize: 1}, func(s client.Step) error {
		if l, ok := s.Doc.(ieee20305.Lister); ok {
			got[s.Href] = len(l.ListItems())
		} else {
			got[s.Href] = -1
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// /edev: its first page of 1 and the rest; /lazy: no item until asked
	// by pages; /gone (404 below the root): skipped.
	if got["/edev"] != 3 || got["/lazy"] != 2 || got["/dcap"] != -1 {
		t.Errorf("Walk saw %v", got)
	}
	if _, ok := got["/gone"]; ok {
		t.Error("Walk visited a link to nothing")
	}
	if err := c.Walk(ctx, "/broken", client.WalkOptions{}, func(client.Step) error { return nil }); err == nil {
		t.Error("Walk of a list whose second page fails")
	}
	if err := c.Walk(ctx, "/down", client.WalkOptions{}, func(client.Step) error { return nil }); err == nil {
		t.Error("Walk from a failing root")
	}
}

func TestTime(t *testing.T) {
	ctx := context.Background()
	c := newPagedClient(t, client.Config{}, "/tm")
	ts, err := c.Time(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Time == nil || ts.Offset < 80*time.Second || ts.Offset > 100*time.Second {
		t.Errorf("offset %v", ts.Offset)
	}
	if d := time.Until(ts.Now()); d < 80*time.Second || d > 100*time.Second {
		t.Errorf("Now is %v ahead", d)
	}
	gone := newPagedClient(t, client.Config{}, "/gone")
	if _, err := gone.Time(ctx); !errors.Is(err, ieee20305.ErrNotFound) {
		t.Errorf("Time of a missing Time: %v", err)
	}
	down, err := client.New(client.Config{Server: "https://127.0.0.1:1/dcap", HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := down.Time(ctx); err == nil {
		t.Error("Time of an unreachable server")
	}
}
