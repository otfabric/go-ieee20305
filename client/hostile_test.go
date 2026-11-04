// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// edevPage is an EndDeviceList of n EndDevices from SFDI first, claiming all.
func edevPage(first, n int, all uint32) *ieee20305.EndDeviceList {
	list := &ieee20305.EndDeviceList{}
	for i := range n {
		var ed ieee20305.EndDevice
		ed.SFDI = ieee20305.SFDIType(first + i + 1)
		list.EndDevice = append(list.EndDevice, ed)
	}
	list.All, list.Results = ieee20305.UInt32(all), ieee20305.UInt32(n)
	return list
}

// A list that claims more items than it has, and sends the same page
// whatever s asks for, ends at MaxListItems.
func TestHostileListClaimsEndlessItems(t *testing.T) {
	var requests atomic.Int64
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeDoc(t, w, http.StatusOK, edevPage(0, 5, ^uint32(0)))
	})
	c := newClient(t, p, srv, client.Config{MaxListItems: 50})
	n := 0
	err := c.Iterate(context.Background(), "/edev", 5, func(any) error { n++; return nil })
	if !errors.Is(err, ieee20305.ErrProtocol) {
		t.Fatalf("Iterate of an endless list: %v", err)
	}
	if got := requests.Load(); got > 12 {
		t.Errorf("%d requests for a list capped at 50 items in pages of 5", got)
	}
	requests.Store(0)
	err = c.Walk(context.Background(), "/edev", client.WalkOptions{PageSize: 5}, func(client.Step) error { return nil })
	if !errors.Is(err, ieee20305.ErrProtocol) {
		t.Fatalf("Walk of an endless list: %v", err)
	}
	if got := requests.Load(); got > 13 {
		t.Errorf("Walk made %d requests of a list capped at 50 items", got)
	}
}

// A list whose all is zero, or smaller than its items, ends after its page.
func TestHostileListCountsLie(t *testing.T) {
	for _, all := range []uint32{0, 1} {
		t.Run(strconv.Itoa(int(all)), func(t *testing.T) {
			var requests atomic.Int64
			p := newPKI(t)
			srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				writeDoc(t, w, http.StatusOK, edevPage(0, 3, all))
			})
			c := newClient(t, p, srv, client.Config{})
			n := 0
			if err := c.Iterate(context.Background(), "/edev", 0, func(any) error { n++; return nil }); err != nil {
				t.Fatal(err)
			}
			if n != 3 || requests.Load() != 1 {
				t.Errorf("%d items in %d requests, want 3 in 1", n, requests.Load())
			}
		})
	}
}

// A page with more items than MaxListItems must not reach fn beyond it.
func TestHostilePageBeyondMaxListItems(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		writeDoc(t, w, http.StatusOK, edevPage(0, 300, 300))
	})
	c := newClient(t, p, srv, client.Config{MaxListItems: 10})
	n := 0
	err := c.Iterate(context.Background(), "/edev", 10, func(any) error { n++; return nil })
	if !errors.Is(err, ieee20305.ErrProtocol) {
		t.Fatalf("Iterate: %v", err)
	}
	if n > 10 {
		t.Errorf("fn saw %d items of a list capped at 10", n)
	}
}

// FindEndDevice pages through a list that claims 2^32-1 EndDevices and
// never holds the device: it must give up at MaxListItems, as Iterate does.
func TestHostileFindEndDeviceEndlessList(t *testing.T) {
	var requests atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 500 {
			cancel()
		}
		s, _ := strconv.Atoi(r.URL.Query().Get("s"))
		writeDoc(t, w, http.StatusOK, edevPage(1000+s, 100, ^uint32(0)))
	})
	c := newClient(t, p, srv, client.Config{MaxListItems: 1000})
	_, err := c.FindEndDevice(ctx, "/edev", client.IdentityOf(p.device.Leaf))
	if got := requests.Load(); got > 12 || !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("FindEndDevice of an endless list: %d requests, err %v; want ErrProtocol after about 10", got, err)
	}
}

// fanOutDcap answers every path with a DeviceCapability whose links lead to
// new paths, so that a walk never meets a resource twice.
func fanOutDcap(t *testing.T, requests *atomic.Int64) func(http.ResponseWriter, *http.Request) {
	var next atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		href := func() string { return fmt.Sprintf("/n/%d", next.Add(1)) }
		dc := ieee20305.DeviceCapability{
			EndDeviceListLink:        &ieee20305.EndDeviceListLink{},
			MirrorUsagePointListLink: &ieee20305.MirrorUsagePointListLink{},
			SelfDeviceLink:           &ieee20305.SelfDeviceLink{},
		}
		dc.TimeLink = &ieee20305.TimeLink{}
		dc.EndDeviceListLink.Href = href()
		dc.MirrorUsagePointListLink.Href = href()
		dc.SelfDeviceLink.Href = href()
		dc.TimeLink.Href = href()
		writeDoc(t, w, http.StatusOK, &dc)
	}
}

// A server whose every resource links to new ones makes Walk fetch 4^8
// resources (and queue them all) at the default MaxDepth: Walk needs a
// bound on the resources it visits, as Iterate has on items.
func TestHostileWalkFanOut(t *testing.T) {
	var requests atomic.Int64
	p := newPKI(t)
	srv := server(t, p, fanOutDcap(t, &requests))
	c := newClient(t, p, srv, client.Config{MaxListItems: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := c.Walk(ctx, "/dcap", client.WalkOptions{}, func(client.Step) error {
		if requests.Load() > 5000 {
			cancel()
			return errors.New("walk still going after 5000 resources")
		}
		return nil
	})
	if !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Walk of an endless tree: %v after %d requests, want ErrProtocol", err, requests.Load())
	}
}

// MaxDepth bounds a walk of an endless tree when the fan-out is one.
func TestHostileWalkDepth(t *testing.T) {
	var requests atomic.Int64
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var ed ieee20305.EndDevice
		ed.ConfigurationLink = &ieee20305.ConfigurationLink{}
		ed.ConfigurationLink.Href = r.URL.Path + "/x"
		writeDoc(t, w, http.StatusOK, &ed)
	})
	c := newClient(t, p, srv, client.Config{})
	depth := 0
	if err := c.Walk(context.Background(), "/edev/1", client.WalkOptions{MaxDepth: 3}, func(s client.Step) error {
		depth = max(depth, s.Depth)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if depth != 3 || requests.Load() != 4 {
		t.Errorf("depth %d in %d requests, want 3 in 4", depth, requests.Load())
	}
}

// Redirect loops stop, and redirects off the server are refused.
func TestHostileRedirects(t *testing.T) {
	var requests atomic.Int64
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop?n="+strconv.FormatInt(requests.Load(), 10), http.StatusFound)
		case "/off":
			http.Redirect(w, r, "https://127.0.0.2:1/dcap", http.StatusTemporaryRedirect)
		case "/scheme":
			http.Redirect(w, r, "http://"+r.Host+"/dcap", http.StatusMovedPermanently)
		}
	})
	c := newClient(t, p, srv, client.Config{})
	var dc ieee20305.DeviceCapability
	if err := c.Get(context.Background(), "/loop", &dc); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("redirect loop: %v", err)
	}
	if got := requests.Load(); got != 10 {
		t.Errorf("%d requests of a redirect loop, want 10", got)
	}
	for _, path := range []string{"/off", "/scheme"} {
		requests.Store(0)
		if err := c.Get(context.Background(), path, &dc); !errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("%s: %v", path, err)
		}
		if requests.Load() != 1 {
			t.Errorf("%s: %d requests", path, requests.Load())
		}
	}
}

// A body that never ends is cut at MaxResponseBytes, and one that stalls
// ends with the context.
func TestHostileBodies(t *testing.T) {
	stall := make(chan struct{})
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", client.MediaType)
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/endless":
			chunk := []byte(strings.Repeat("<a>", 1024))
			for r.Context().Err() == nil {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		case "/stall":
			_, _ = w.Write([]byte("<DeviceCapability"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-stall:
			}
		}
	})
	t.Cleanup(func() { close(stall) })
	c := newClient(t, p, srv, client.Config{MaxResponseBytes: 4096})
	var dc ieee20305.DeviceCapability
	if err := c.Get(context.Background(), "/endless", &dc); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("endless body: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.Get(ctx, "/stall", &dc); !errors.Is(err, ieee20305.ErrTimeout) {
		t.Errorf("stalled body: %v", err)
	}
}

// Content types that are no XML document are refused; a deeply nested
// document fails or decodes, without exhausting the stack.
func TestHostileDocuments(t *testing.T) {
	deep := `<DeviceCapability xmlns="urn:ieee:std:2030.5:ns" href="/dcap">` +
		strings.Repeat("<x>", 20000) + strings.Repeat("</x>", 20000) + `</DeviceCapability>`
	deepKnown := strings.Repeat(`<DeviceCapability xmlns="urn:ieee:std:2030.5:ns">`, 50000)
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		ct, body := client.MediaType, ""
		switch r.URL.Path {
		case "/html":
			ct, body = "text/html", "<html/>"
		case "/exi":
			ct = "application/sep-exi"
		case "/bad":
			ct = "application/sep+xml; =="
		case "/deep":
			body = deep
		case "/deepknown":
			body = deepKnown
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write([]byte(body))
	})
	c := newClient(t, p, srv, client.Config{MaxResponseBytes: 3 << 20})
	var dc ieee20305.DeviceCapability
	for _, path := range []string{"/html", "/exi", "/bad"} {
		if err := c.Get(context.Background(), path, &dc); !errors.Is(err, ieee20305.ErrProtocol) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if err := c.Get(context.Background(), "/deep", &dc); err != nil && !errors.Is(err, ieee20305.ErrProtocol) {
		t.Logf("deep document: %v", err)
	}
	if _, err := c.Fetch(context.Background(), "/deepknown"); err == nil {
		t.Error("an unterminated nest of 50000 documents decoded")
	}
}
