// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// subscribingFake returns a fake server whose EndDevice has a
// SubscriptionList.
func subscribingFake(t *testing.T) *fakeServer {
	f := newFake(t)
	f.setEndDevice(`<RegistrationLink href="/edev/1/rg"/><FunctionSetAssignmentsListLink href="/edev/1/fsa" all="1"/>
		<SubscriptionListLink href="/edev/1/sub" all="0"/>`)
	return f
}

// notify posts a Notification to the NotificationHandler of a.
func notify(t *testing.T, a *Agent, resource, sub string, status int) {
	t.Helper()
	if code := post(a, resource, sub, status); code != http.StatusCreated {
		t.Fatalf("notification answered %d", code)
	}
}

// post posts a Notification to the NotificationHandler of a and returns
// the status of the answer.
func post(a *Agent, resource, sub string, status int, header ...string) int {
	body := `<Notification ` + ns + `><subscribedResource>` + resource + `</subscribedResource><status>` +
		string(rune('0'+status)) + `</status><subscriptionURI>` + sub + `</subscriptionURI></Notification>`
	r := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	r.Header.Set("Content-Type", client.MediaType)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	a.NotificationHandler().ServeHTTP(w, r)
	return w.Code
}

// woken reports whether a was woken, and takes the wake-up.
func woken(a *Agent) bool {
	select {
	case <-a.wake:
		return true
	default:
		return false
	}
}

func TestSubscribe(t *testing.T) {
	ctx := context.Background()
	f := subscribingFake(t)
	f.location("POST /edev/1/sub", "/edev/1/sub/1")
	a := newTestAgent(t, f, Config{NotificationURI: "https://device.test/notify"})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// The FunctionSetAssignmentsList, the DERProgramList and the
	// DERControlList.
	if n := f.count("POST /edev/1/sub"); n != 3 {
		t.Fatalf("%d subscriptions, want 3", n)
	}
	if !a.subscribed() || a.subscribedPollRate() != 4*time.Minute {
		t.Errorf("subscribed %v, poll rate %v", a.subscribed(), a.subscribedPollRate())
	}
	// What changed while the agent subscribed is read at once.
	if !woken(a) {
		t.Error("not woken after subscribing")
	}

	// A notification wakes the agent, once however many come.
	notify(t, a, "/derp", "/edev/1/sub/1", 0)
	notify(t, a, "/derp", "/edev/1/sub/2", 0)
	if !woken(a) || woken(a) {
		t.Error("a notification wakes the agent once")
	}
	// A cancellation of another subscription keeps this one.
	notify(t, a, "/derp", "/edev/1/sub/other", 1)
	if a.subs["/derp"] != "/edev/1/sub/1" {
		t.Errorf("subscriptions %v", a.subs)
	}
	// A cancellation makes the agent subscribe again at its next poll.
	notify(t, a, "/edev/1/fsa", "/edev/1/sub/1", 1)
	if _, ok := a.subs["/edev/1/fsa"]; ok {
		t.Fatal("the cancelled subscription is kept")
	}
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.count("POST /edev/1/sub"); n != 4 {
		t.Errorf("%d subscriptions, want 4", n)
	}
	woken(a)

	// The agent unsubscribes as it stops, whether the server takes it or
	// not.
	f.failWith("DELETE /edev/1/sub/1", 500)
	a.unsubscribe()
	if n := f.count("DELETE /edev/1/sub/1"); n != 3 {
		t.Errorf("%d unsubscriptions, want 3", n)
	}
	if a.subscribed() {
		t.Error("subscribed after unsubscribing")
	}
	a.unsubscribe()
}

func TestSubscribeRefused(t *testing.T) {
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusForbidden} {
		f := subscribingFake(t)
		f.failWith("POST /edev/1/sub", code)
		a := newTestAgent(t, f, Config{NotificationURI: "https://device.test/notify", SubscribedPollRate: time.Second})
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := a.poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		// Asked once, then never again.
		if n := f.count("POST /edev/1/sub"); n != 1 || a.subscribed() || woken(a) {
			t.Errorf("%d: %d subscriptions, subscribed %v", code, n, a.subscribed())
		}
		if a.subscribedPollRate() != time.Second {
			t.Errorf("subscribed poll rate %v", a.subscribedPollRate())
		}
	}
}

func TestSubscribeFails(t *testing.T) {
	f := subscribingFake(t)
	f.failWith("POST /edev/1/sub", 500)
	a := newTestAgent(t, f, Config{NotificationURI: "https://device.test/notify"})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Asked again at each poll.
	if n := f.count("POST /edev/1/sub"); n != 6 || a.subscribed() {
		t.Errorf("%d subscriptions, subscribed %v", n, a.subscribed())
	}
}

// TestRunWokenByNotification has Run poll at once on a notification, and
// unsubscribe as it returns.
func TestRunWokenByNotification(t *testing.T) {
	f := subscribingFake(t)
	f.location("POST /edev/1/sub", "/edev/1/sub/1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polls := make(chan struct{}, 10)
	a := newTestAgent(t, f, Config{
		PollRate: time.Hour, NotificationURI: "https://device.test/notify",
		OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
			polls <- struct{}{}
			return nil
		},
	})
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	<-polls // Start
	<-polls // at once after subscribing
	notify(t, a, "/derp", "/edev/1/sub/1", 0)
	<-polls // on the notification
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if n := f.count("DELETE /edev/1/sub/1"); n != 3 {
		t.Errorf("%d unsubscriptions, want 3", n)
	}
}

// Only the agent's own subscriptions wake it or cancel, and
// NotificationAuthorize decides who may notify.
func TestNotificationsOfOtherSubscriptions(t *testing.T) {
	ctx := context.Background()
	f := subscribingFake(t)
	f.location("POST /edev/1/sub", "/edev/1/sub/1")
	a := newTestAgent(t, f, Config{
		NotificationURI: "https://device.test/notify",
		NotificationAuthorize: func(r *http.Request) error {
			if r.Header.Get("X-Peer") != "server" {
				return errors.New("another peer")
			}
			return nil
		},
	})
	if a.NotificationHandler() != a.NotificationHandler() {
		t.Error("a new handler at each call")
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	woken(a)
	if code := post(a, "/derp", "/edev/1/sub/1", 0); code != http.StatusForbidden || woken(a) {
		t.Errorf("unauthorized: %d", code)
	}
	for _, sub := range []string{"/edev/1/sub/9", "", "https://other.example/edev/1/sub/1", "%zz"} {
		if code := post(a, "/derp", sub, 0, "X-Peer", "server"); code != http.StatusCreated || woken(a) {
			t.Errorf("subscription %q: %d, woken", sub, code)
		}
	}
	// A cancellation of a subscription that is not the agent's keeps the
	// agent's subscription of the resource.
	if code := post(a, "/derp", "/edev/1/sub/9", 1, "X-Peer", "server"); code != http.StatusCreated || a.subs["/derp"] != "/edev/1/sub/1" {
		t.Errorf("%d, subscriptions %v", code, a.subs)
	}
	// The agent's own, by path or by URL of the server.
	if code := post(a, "/derp", f.ts.URL+"/edev/1/sub/1", 1, "X-Peer", "server"); code != http.StatusCreated || !woken(a) {
		t.Errorf("own subscription: %d", code)
	}
	if _, ok := a.subs["/derp"]; ok {
		t.Errorf("the cancelled subscription is kept: %v", a.subs)
	}
}

// A notification does not end the wait after a failed poll.
func TestRunBackoffNotEndedByNotification(t *testing.T) {
	f := subscribingFake(t)
	f.location("POST /edev/1/sub", "/edev/1/sub/1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polls := make(chan struct{}, 10)
	calls := 0
	a := newTestAgent(t, f, Config{
		PollRate: time.Hour, NotificationURI: "https://device.test/notify",
		Backoff: ieee20305.ExponentialBackoff{Initial: time.Hour, Max: time.Hour},
		OnAssignments: func(context.Context, []*ieee20305.FunctionSetAssignments) error {
			calls++
			polls <- struct{}{}
			if calls == 2 {
				return errors.New("transient")
			}
			return nil
		},
	})
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	<-polls // Start
	<-polls // at once after subscribing: fails
	notify(t, a, "/derp", "/edev/1/sub/1", 0)
	select {
	case <-polls:
		t.Error("a notification ended the backoff")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
}
