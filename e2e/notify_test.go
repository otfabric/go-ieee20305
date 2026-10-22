// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// inbox collects the notifications of a NotificationHandler.
type inbox struct {
	mu    sync.Mutex
	notes []*ieee20305.Notification
}

func (b *inbox) handle(_ context.Context, n *ieee20305.Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notes = append(b.notes, n)
	return nil
}

func (b *inbox) all() []*ieee20305.Notification {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*ieee20305.Notification(nil), b.notes...)
}

func (b *inbox) last() *ieee20305.Notification {
	all := b.all()
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

// device provisions a device and returns its client and EndDevice.
func (e *env) provisioned(t *testing.T, name string, edition ieee20305.Edition) (*client.Client, string, security.LFDI) {
	t.Helper()
	dev := e.pki.device(t, name)
	lfdi := security.CertificateLFDI(dev.Leaf)
	ed, err := e.srv.Admin().ProvisionDevice(context.Background(), server.DeviceSpec{LFDI: lfdi, Edition: edition})
	if err != nil {
		t.Fatal(err)
	}
	return e.client(t, dev, edition.Codec()), ed, lfdi
}

func TestNotifications(t *testing.T) {
	for _, edition := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		t.Run(edition.String(), func(t *testing.T) {
			env := newEnv(t, server.Config{})
			ctx := context.Background()
			a := env.srv.Admin()
			if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
				t.Fatal(err)
			}
			c, ed, lfdi := env.provisioned(t, "device-1", edition)
			box := &inbox{}
			url := env.listen(t, &client.NotificationHandler{Handle: box.handle, Codec: edition.Codec()})
			sub, err := c.Subscribe(ctx, ed+"/sub", "/derp", url+"/ntfy", 10)
			if err != nil {
				t.Fatal(err)
			}
			fsaSub, err := c.Subscribe(ctx, ed+"/sub", ed+"/fsa", url+"/ntfy", 10)
			if err != nil {
				t.Fatal(err)
			}
			// A new program, and a change of it.
			prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, "the notification of the new program", func() bool {
				n := box.last()
				if n == nil || n.SubscriptionURI != sub || n.Resource == nil {
					return false
				}
				l, ok := n.Resource.Value.(*ieee20305.DERProgramList)
				return ok && len(l.DERProgram) == 1
			})
			node, err := a.Get(ctx, prog)
			if err != nil {
				t.Fatal(err)
			}
			node.Value.(*ieee20305.DERProgram).Primacy = 77
			if err := a.Put(ctx, node); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the notification of the changed program", func() bool {
				n := box.last()
				if n == nil || n.Resource == nil {
					return false
				}
				l, ok := n.Resource.Value.(*ieee20305.DERProgramList)
				return ok && len(l.DERProgram) == 1 && l.DERProgram[0].Primacy == 77
			})
			// An assignment notifies the FSA subscription.
			if _, err := a.AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0)); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the notification of the assignment", func() bool {
				n := box.last()
				return n != nil && n.SubscriptionURI == fsaSub
			})
			// After Unsubscribe, no notifications.
			if err := c.Unsubscribe(ctx, sub); err != nil {
				t.Fatal(err)
			}
			before := len(box.all())
			if _, err := a.Create(ctx, "/derp", examples.DERProgram(0), ""); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			for _, n := range box.all()[before:] {
				if n.SubscriptionURI == sub {
					t.Error("a notification after Unsubscribe")
				}
			}
			// Deleting the subscribed resource ends the subscription.
			if err := a.Delete(ctx, ed+"/fsa"); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the notification of the deletion", func() bool {
				n := box.last()
				return n != nil && n.SubscriptionURI == fsaSub && n.Status == server.NotificationResourceDeleted
			})
			eventually(t, "the subscription to go", func() bool {
				_, err := a.Get(ctx, fsaSub)
				return errors.Is(err, server.ErrNoNode)
			})
		})
	}
}

func TestSubscriptionErrors(t *testing.T) {
	env := newEnv(t, server.Config{})
	ctx := context.Background()
	c, ed, _ := env.provisioned(t, "device-1", ieee20305.Edition2023)
	other, otherEd, _ := env.provisioned(t, "device-2", ieee20305.Edition2023)
	for _, tc := range []struct {
		name, resource, uri string
		status              int
	}{
		{"http", ed + "/fsa", "http://127.0.0.1/ntfy", http.StatusBadRequest},
		{"no resource", "/nope", "https://127.0.0.1/ntfy", http.StatusBadRequest},
		{"another device's", otherEd + "/fsa", "https://127.0.0.1/ntfy", http.StatusBadRequest},
		{"not subscribable", "/tm", "https://127.0.0.1/ntfy", http.StatusBadRequest},
	} {
		_, err := c.Subscribe(ctx, ed+"/sub", tc.resource, tc.uri, 1)
		var se *ieee20305.StatusError
		if !errors.As(err, &se) || se.StatusCode != tc.status {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := other.Subscribe(ctx, ed+"/sub", otherEd+"/fsa", "https://127.0.0.1/ntfy", 1); err == nil {
		t.Error("a subscription in another device's list")
	}
	// EXI is not sent.
	sub := &ieee20305.Subscription{NotificationURI: "https://127.0.0.1/ntfy", Encoding: 1, Level: "+S1"}
	sub.SubscribedResource = ed + "/fsa"
	if _, err := c.Post(ctx, ed+"/sub", sub); err == nil {
		t.Error("an EXI subscription")
	}
}

// A server that cannot deliver cancels the subscription.
func TestUndeliverable(t *testing.T) {
	env := newEnv(t, server.Config{NotificationAttempts: 2, NotificationRetryDelay: time.Millisecond})
	ctx := context.Background()
	c, ed, lfdi := env.provisioned(t, "device-1", ieee20305.Edition2023)
	failing := env.listen(t, &client.NotificationHandler{Handle: func(context.Context, *ieee20305.Notification) error {
		return errors.New("full")
	}})
	sub, err := c.Subscribe(ctx, ed+"/sub", ed+"/fsa", failing, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.Admin().AssignFSA(ctx, lfdi, examples.FunctionSetAssignments(0)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the cancellation", func() bool {
		_, err := env.srv.Admin().Get(ctx, sub)
		return errors.Is(err, server.ErrNoNode)
	})
}

// Many devices subscribe to one program that changes many times: each
// subscription sees the changes in order, and its last notification is the
// last state. Nothing leaks after Close.
func TestNotificationStorm(t *testing.T) {
	const devices, perDevice, changes = 20, 10, 1000
	base := runtime.NumGoroutine()
	pki := newPKI(t)
	notifications := client.NewHTTPClient(pki.device(t, "server notifications"), pki.roots)
	env := newEnvPKI(t, pki, server.Config{NotificationWorkers: 32, NotificationClient: notifications})
	ctx := context.Background()
	a := env.srv.Admin()
	if err := a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}); err != nil {
		t.Fatal(err)
	}
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var disorder error
	url := env.listen(t, &client.NotificationHandler{Handle: func(_ context.Context, n *ieee20305.Notification) error {
		p, ok := n.Resource.Value.(*ieee20305.DERProgram)
		if !ok || p.Description == nil {
			return nil
		}
		v, err := strconv.Atoi(string(*p.Description))
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if last, ok := seen[n.SubscriptionURI]; ok && v < last {
			disorder = fmt.Errorf("%s: %d after %d", n.SubscriptionURI, v, last)
		}
		seen[n.SubscriptionURI] = v
		return nil
	}})
	var subs []string
	for d := 0; d < devices; d++ {
		c, ed, _ := env.provisioned(t, fmt.Sprintf("device-%d", d), ieee20305.Edition2023)
		for i := 0; i < perDevice; i++ {
			sub, err := c.Subscribe(ctx, ed+"/sub", prog, url, 1)
			if err != nil {
				t.Fatal(err)
			}
			subs = append(subs, sub)
		}
	}
	for i := 0; i < changes; i++ {
		node, err := a.Get(ctx, prog)
		if err != nil {
			t.Fatal(err)
		}
		desc := ieee20305.String32(strconv.Itoa(i))
		node.Value.(*ieee20305.DERProgram).Description = &desc
		if err := a.Put(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "every subscription to see the last change", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range subs {
			if seen[s] != changes-1 {
				return false
			}
		}
		return true
	})
	mu.Lock()
	if disorder != nil {
		t.Error(disorder)
	}
	mu.Unlock()
	if err := env.srv.Close(); err != nil {
		t.Fatal(err)
	}
	notifications.CloseIdleConnections()
	for _, c := range env.clients {
		c.CloseIdleConnections()
	}
	eventually(t, "the goroutines to end", func() bool {
		return runtime.NumGoroutine() <= base+10
	})
}
