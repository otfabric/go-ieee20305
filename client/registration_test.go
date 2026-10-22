// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// fakeServer is an EndDeviceList that devices register in.
type fakeServer struct {
	mu          sync.Mutex
	devices     []ieee20305.EndDevice
	honorFilter bool
	noLocation  bool
	pin         ieee20305.PINType
	pages       int
}

func (f *fakeServer) add(sfdi uint64, lfdi []byte) {
	var ed ieee20305.EndDevice
	ed.SFDI = ieee20305.SFDIType(sfdi)
	if lfdi != nil {
		l := ieee20305.HexBinary160(lfdi)
		ed.LFDI = &l
	}
	f.store(ed)
}

func (f *fakeServer) store(ed ieee20305.EndDevice) string {
	href := "/edev/" + strconv.Itoa(len(f.devices))
	ed.Href = &href
	ed.RegistrationLink = &ieee20305.RegistrationLink{}
	ed.RegistrationLink.Href = href + "/rg"
	f.devices = append(f.devices, ed)
	return href
}

func (f *fakeServer) handle(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path
		switch {
		case path == "/edev" && r.Method == http.MethodGet:
			f.pages++
			q := r.URL.Query()
			var match []ieee20305.EndDevice
			for _, ed := range f.devices {
				if f.honorFilter && q.Get("sFDI") != "" && q.Get("sFDI") != strconv.FormatUint(uint64(ed.SFDI), 10) {
					continue
				}
				match = append(match, ed)
			}
			s, _ := strconv.Atoi(q.Get("s"))
			l, _ := strconv.Atoi(q.Get("l"))
			if l == 0 {
				l = len(match)
			}
			end := min(s+l, len(match))
			s = min(s, end)
			var list ieee20305.EndDeviceList
			list.All, list.Results = ieee20305.UInt32(len(match)), ieee20305.UInt32(end-s)
			list.EndDevice = match[s:end]
			writeDoc(t, w, http.StatusOK, &list)
		case path == "/edev" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var ed ieee20305.EndDevice
			if err := ieee20305.Unmarshal(body, &ed); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			href := f.store(ed)
			if !f.noLocation {
				w.Header().Set("Location", href)
			}
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(path, "/rg"):
			writeDoc(t, w, http.StatusOK, &ieee20305.Registration{PIN: f.pin, DateTimeRegistered: 1700000000})
		case strings.HasPrefix(path, "/edev/"):
			i, err := strconv.Atoi(strings.TrimPrefix(path, "/edev/"))
			if err != nil || i >= len(f.devices) {
				http.NotFound(w, r)
				return
			}
			writeDoc(t, w, http.StatusOK, &f.devices[i])
		default:
			http.NotFound(w, r)
		}
	}
}

func TestRegisterCreatesThenFinds(t *testing.T) {
	p := newPKI(t)
	f := &fakeServer{honorFilter: true, pin: 111115}
	f.add(1, nil)
	c := newClient(t, p, server(t, p, f.handle(t)), client.Config{})
	id := client.IdentityOf(p.device.Leaf)
	ctx := context.Background()

	ed, created, err := c.Register(ctx, "/edev", id)
	if err != nil || !created {
		t.Fatalf("Register = %+v, %v, %v", ed, created, err)
	}
	if uint64(ed.SFDI) != uint64(id.SFDI) || ed.LFDI == nil || ed.LFDI.String() != id.LFDI.String() || ed.ChangedTime == 0 {
		t.Errorf("created EndDevice = %+v", ed)
	}
	again, created, err := c.Register(ctx, "/edev", id)
	if err != nil || created || *again.Href != *ed.Href {
		t.Errorf("second Register = %v, %v, %v", again, created, err)
	}
	if len(f.devices) != 2 {
		t.Errorf("the server has %d devices, want 2", len(f.devices))
	}

	reg, err := c.Registration(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckPIN(reg, 111115); err != nil {
		t.Error(err)
	}
	if err := client.CheckPIN(reg, 123455); !errors.Is(err, client.ErrPINMismatch) {
		t.Errorf("wrong PIN: %v", err)
	}
	if _, err := c.Registration(ctx, &ieee20305.EndDevice{}); !errors.Is(err, client.ErrNotRegistered) {
		t.Errorf("no RegistrationLink: %v", err)
	}
}

func TestFindEndDevicePagesWhenTheServerIgnoresTheFilter(t *testing.T) {
	p := newPKI(t)
	id := client.IdentityOf(p.device.Leaf)
	f := &fakeServer{}
	for i := 0; i < 250; i++ {
		f.add(uint64(1000+i), nil)
	}
	// Same SFDI, another LFDI: not this device.
	f.add(uint64(id.SFDI), make([]byte, 20))
	f.add(uint64(id.SFDI), id.LFDI[:])
	c := newClient(t, p, server(t, p, f.handle(t)), client.Config{})

	ed, err := c.FindEndDevice(context.Background(), "/edev", id)
	if err != nil {
		t.Fatal(err)
	}
	if ed == nil || *ed.Href != fmt.Sprintf("/edev/%d", 251) {
		t.Fatalf("found %+v", ed)
	}
	if f.pages != 3 {
		t.Errorf("read %d pages, want 3", f.pages)
	}

	other := client.Identity{SFDI: 42}
	if ed, err := c.FindEndDevice(context.Background(), "/edev", other); err != nil || ed != nil {
		t.Errorf("absent device: %+v, %v", ed, err)
	}
}

func TestRegisterWithoutLocation(t *testing.T) {
	p := newPKI(t)
	f := &fakeServer{honorFilter: true, noLocation: true}
	c := newClient(t, p, server(t, p, f.handle(t)), client.Config{})
	ed, created, err := c.Register(context.Background(), "/edev", client.IdentityOf(p.device.Leaf))
	if err != nil || !created || ed == nil {
		t.Errorf("Register = %+v, %v, %v", ed, created, err)
	}
}

func TestRegisterFailsWhenTheServerDropsTheDevice(t *testing.T) {
	p := newPKI(t)
	f := &fakeServer{honorFilter: true, noLocation: true}
	srv := server(t, p, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated) // created nothing
			return
		}
		f.handle(t)(w, r)
	})
	c := newClient(t, p, srv, client.Config{})
	if _, _, err := c.Register(context.Background(), "/edev", client.IdentityOf(p.device.Leaf)); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("err = %v, want ErrProtocol", err)
	}
}
