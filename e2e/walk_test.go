// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	examples "github.com/otfabric/go-ieee20305/internal/examples/ieee20305"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
)

// tree seeds a server with a device's resource tree: DeviceCapability,
// EndDeviceList, the EndDevice of owner, its FunctionSetAssignments with a
// DERProgramList, a DERProgram with DERControls, and a MirrorUsagePointList;
// and the parts the server creates with them, such as the lists of the
// EndDevice.
func tree(t *testing.T, srv *server.Server, owner string) {
	t.Helper()
	ctx := context.Background()
	a := srv.Admin()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Root(ctx, "EndDeviceList", "/edev", &ieee20305.EndDeviceList{}))
	must(a.Root(ctx, "MirrorUsagePointList", "/mup", &ieee20305.MirrorUsagePointList{}))
	must(a.Root(ctx, "DERProgramList", "/derp", &ieee20305.DERProgramList{}))
	ed, err := a.Create(ctx, "/edev", examples.EndDevice(0), owner)
	must(err)
	must(a.NewList(ctx, "FunctionSetAssignmentsList", ed+"/fsa", ed, "FunctionSetAssignmentsListLink", owner))
	fsa, err := a.Create(ctx, ed+"/fsa", examples.FunctionSetAssignments(0), owner)
	must(err)
	must(a.NewList(ctx, "DERProgramList", fsa+"/derp", fsa, "DERProgramListLink", owner))
	prog, err := a.Create(ctx, "/derp", examples.DERProgram(0), "")
	must(err)
	must(a.AddMember(ctx, fsa+"/derp", prog))
	must(a.NewList(ctx, "DERControlList", prog+"/derc", prog, "DERControlListLink", ""))
	for i := 0; i < 3; i++ {
		_, err := a.Create(ctx, prog+"/derc", examples.DERControl(0), "")
		must(err)
	}
}

func TestWalk(t *testing.T) {
	for _, e := range []ieee20305.Edition{ieee20305.Edition2023, ieee20305.Edition2018} {
		t.Run(e.String(), func(t *testing.T) {
			env := newEnv(t, server.Config{DefaultEdition: e})
			dev := env.pki.device(t, "device-1")
			tree(t, env.srv, security.CertificateLFDI(dev.Leaf).String())
			c := env.client(t, dev, e.Codec())
			seen := map[string]int{}
			items := map[string]bool{}
			err := c.Walk(context.Background(), client.DefaultDeviceCapabilityPath, client.WalkOptions{PageSize: 1}, func(s client.Step) error {
				seen[s.Href]++
				if l, ok := s.Doc.(ieee20305.Lister); ok {
					for _, it := range l.ListItems() {
						for _, h := range hrefsOf(it) {
							items[h] = true
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Lists come with their items, which Walk does not fetch again;
			// it follows their links.
			for _, href := range []string{"/dcap", "/tm", "/edev", "/edev/1/fsa", "/edev/1/fsa/1/derp", "/derp/1/derc", "/mup", "/derp"} {
				if seen[href] != 1 {
					t.Errorf("visited %s %d times", href, seen[href])
				}
			}
			for href, n := range seen {
				if n != 1 {
					t.Errorf("visited %s %d times", href, n)
				}
			}
			for _, href := range []string{"/edev/1", "/edev/1/fsa/1", "/derp/1", "/derp/1/derc/1", "/derp/1/derc/3"} {
				if !items[href] {
					t.Errorf("no item %s in the lists walked", href)
				}
			}
			// Another device sees the public tree only.
			other := env.client(t, env.pki.device(t, "device-2"), e.Codec())
			n := 0
			if err := other.Walk(context.Background(), "/dcap", client.WalkOptions{}, func(s client.Step) error {
				if s.Href == "/edev/1" {
					t.Error("another device walked into the EndDevice")
				}
				n++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Error("nothing visited")
			}
		})
	}
}

func TestTime(t *testing.T) {
	now := time.Now().Add(42 * time.Minute)
	env := newEnv(t, server.Config{Clock: func() time.Time { return now }})
	c := env.client(t, env.pki.device(t, "device-1"), nil)
	ts, err := c.Time(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := ts.Offset - 42*time.Minute; d > 2*time.Second || d < -2*time.Second {
		t.Errorf("offset %v, want about 42m", ts.Offset)
	}
	if d := time.Until(ts.Now()) - 42*time.Minute; d > 2*time.Second || d < -2*time.Second {
		t.Errorf("Now is %v off", d)
	}
	if err := c.Head(context.Background(), "/tm"); err != nil {
		t.Errorf("Head: %v", err)
	}
}

// hrefsOf returns the href of a document, if it has one.
func hrefsOf(v any) []string {
	if r, ok := v.(ieee20305.Hrefer); ok {
		return []string{r.GetHref()}
	}
	return nil
}
