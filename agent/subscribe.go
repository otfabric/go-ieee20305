// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"net/http"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

// NotificationHandler returns the http.Handler of the notifications of the
// agent's subscriptions, to serve at Config.NotificationURI (over TLS that
// verifies the server's certificate). A notification makes the agent poll
// at once; a cancellation, subscribe again at its next poll.
func (a *Agent) NotificationHandler() http.Handler {
	h, err := client.NewNotificationHandler(func(_ context.Context, n *ieee20305.Notification) error {
		if n.Status != 0 {
			a.mu.Lock()
			if a.subs[n.SubscribedResource] == n.SubscriptionURI {
				delete(a.subs, n.SubscribedResource)
			}
			a.mu.Unlock()
		}
		select {
		case a.wake <- struct{}{}:
		default:
		}
		return nil
	}, client.NotificationConfig{Codec: a.c.Codec()})
	if err != nil {
		panic(err) // the handle function is never nil
	}
	return h
}

// watch records a list the poll read, to subscribe to.
func (a *Agent) watch(href string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.watched != nil {
		a.watched[href] = true
	}
}

// subscribe subscribes to the lists the last poll read that the agent has
// no subscription of, when it has a NotificationURI. A server that takes no
// subscriptions (405 or 403) is not asked again.
func (a *Agent) subscribe(ctx context.Context) {
	ed := a.State().EndDevice
	a.mu.Lock()
	if a.cfg.NotificationURI == "" || a.noSubscribe || ed == nil || ed.SubscriptionListLink == nil {
		a.mu.Unlock()
		return
	}
	var todo []string
	for href := range a.watched {
		if _, ok := a.subs[href]; !ok {
			todo = append(todo, href)
		}
	}
	a.mu.Unlock()
	created := false
	defer func() {
		// What changed between the read of a list and the subscription to
		// it is notified to no one: read again at once.
		if created {
			select {
			case a.wake <- struct{}{}:
			default:
			}
		}
	}()
	for _, href := range todo {
		sub, err := a.c.Subscribe(ctx, ed.SubscriptionListLink.Href, href, a.cfg.NotificationURI, 255)
		var se *ieee20305.StatusError
		switch {
		case err == nil:
			created = true
			a.mu.Lock()
			a.subs[href] = sub
			a.mu.Unlock()
		case errors.As(err, &se) && (se.StatusCode == http.StatusMethodNotAllowed || se.StatusCode == http.StatusForbidden):
			a.log.Infof("the server takes no subscriptions (%v): polling only", err)
			a.mu.Lock()
			a.noSubscribe = true
			a.mu.Unlock()
			return
		default:
			a.log.Warnf("subscribing to %s: %v", href, err)
		}
	}
}

// subscribed reports whether the agent has subscriptions.
func (a *Agent) subscribed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.subs) > 0
}

// subscribedPollRate is the poll interval while subscribed.
func (a *Agent) subscribedPollRate() time.Duration {
	if a.cfg.SubscribedPollRate > 0 {
		return a.cfg.SubscribedPollRate
	}
	return 4 * a.pollRate()
}

// unsubscribe deletes the agent's subscriptions, as it stops.
func (a *Agent) unsubscribe() {
	a.mu.Lock()
	subs := a.subs
	a.subs = map[string]string{}
	a.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, sub := range subs {
		if err := a.c.Unsubscribe(ctx, sub); err != nil {
			a.log.Debugf("unsubscribing %s: %v", sub, err)
		}
	}
}

// sleepOrWake waits for d, a notification, or the end of ctx.
func (a *Agent) sleepOrWake(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.wake:
		return nil
	case <-t.C:
		return nil
	}
}
