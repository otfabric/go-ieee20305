// SPDX-License-Identifier: MIT

package discovery

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/internal/dnsmsg"
	"github.com/otfabric/go-ieee20305/internal/leakcheck"
)

// sendFails is a transport that cannot send once fail is set, and tells
// of each packet it did not send.
type sendFails struct {
	*node
	fail   atomic.Bool
	failed chan struct{}
}

func (s *sendFails) WritePacket(data []byte, dst netip.AddrPort) error {
	if !s.fail.Load() {
		return s.node.WritePacket(data, dst)
	}
	select {
	case s.failed <- struct{}{}:
	default:
	}
	return errors.New("no route")
}

// brokenLogger is a Logger none of whose methods returns normally.
type brokenLogger struct{}

func (brokenLogger) Debugf(string, ...any) { panic("ended by the test") }
func (brokenLogger) Infof(string, ...any)  { panic("ended by the test") }
func (brokenLogger) Warnf(string, ...any)  { panic("ended by the test") }
func (brokenLogger) Errorf(string, ...any) { panic("ended by the test") }

// TestBrowseLoggerDoesNotReturnNormally: the entries of a Logger that does
// not return normally are lost, and the browser goes on: after a query it
// could not send, which it logs, it delivers the service whose records
// come.
func TestBrowseLoggerDoesNotReturnNormally(t *testing.T) {
	leakcheck.Check(t)
	b := newBus()
	r := responder{t, b.join()}
	tr := &sendFails{node: b.join(), failed: make(chan struct{}, 16)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := Browse(ctx, BrowseOptions{Transport: tr, Logger: brokenLogger{}})
	if err != nil {
		t.Fatal(err)
	}
	tr.fail.Store(true)
	inst := instanceName("x")
	// The browser asks for the records of the instance, and cannot.
	r.respond(rec(typeName, 4500, false, &dnsmsg.PTR{Target: inst}))
	select {
	case <-tr.failed:
	case <-time.After(10 * time.Second):
		t.Fatal("the browser did not try to send a query")
	}
	r.respond(
		rec(inst, 120, true, &dnsmsg.SRV{Port: 80, Target: "x.local"}),
		rec(inst, 4500, true, &dnsmsg.TXT{Strings: []string{"dcap=/d"}}),
		rec("x.local", 120, true, &dnsmsg.A{Addr: netip.MustParseAddr("192.0.2.200")}),
	)
	if s := next(t, ch); s.Instance != "x" || s.Removed {
		t.Fatalf("delivered %+v", s)
	}
	cancel()
	for range ch {
	}
}
