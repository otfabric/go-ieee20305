// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
)

func notification(t *testing.T, sub string, primacy ieee20305.PrimacyType, created ieee20305.TimeType) []byte {
	t.Helper()
	p := &ieee20305.DERProgram{Primacy: primacy}
	n := &ieee20305.Notification{SubscriptionURI: sub, CreatedDateTime: &created, Resource: &ieee20305.AnyResource{Value: p}}
	n.SubscribedResource = "/derp/1"
	out, err := ieee20305.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNotificationHandler(t *testing.T) {
	var got []*ieee20305.Notification
	fail := false
	h := &client.NotificationHandler{
		Handle: func(_ context.Context, n *ieee20305.Notification) error {
			if fail {
				return errors.New("busy")
			}
			got = append(got, n)
			return nil
		},
		Authorize: func(r *http.Request) error {
			if r.Header.Get("X-Deny") != "" {
				return errors.New("denied")
			}
			return nil
		},
		MaxBytes: 4096,
	}
	post := func(body []byte, header ...string) int {
		r := httptest.NewRequest(http.MethodPost, "/ntfy", bytes.NewReader(body))
		r.Header.Set("Content-Type", client.MediaType)
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := post(notification(t, "/edev/1/sub/1", 1, 10)); code != http.StatusCreated || len(got) != 1 {
		t.Fatalf("first: %d, %d handled", code, len(got))
	}
	// The same content sent again, later, is acknowledged and not handled.
	if code := post(notification(t, "/edev/1/sub/1", 1, 20)); code != http.StatusCreated || len(got) != 1 {
		t.Errorf("duplicate: %d, %d handled", code, len(got))
	}
	if code := post(notification(t, "/edev/1/sub/1", 2, 30)); code != http.StatusCreated || len(got) != 2 {
		t.Errorf("new content: %d, %d handled", code, len(got))
	}
	if code := post(notification(t, "/edev/1/sub/2", 2, 30)); code != http.StatusCreated || len(got) != 3 {
		t.Errorf("another subscription: %d, %d handled", code, len(got))
	}
	fail = true
	if code := post(notification(t, "/edev/1/sub/2", 3, 30)); code != http.StatusInternalServerError {
		t.Errorf("failing handler: %d", code)
	}
	fail = false
	if code := post(notification(t, "/edev/1/sub/2", 3, 30)); code != http.StatusCreated || len(got) != 4 {
		t.Errorf("after a failure, the same content: %d, %d handled", code, len(got))
	}
	for _, c := range []struct {
		name   string
		body   []byte
		header []string
		want   int
	}{
		{"denied", notification(t, "/s", 1, 1), []string{"X-Deny", "1"}, http.StatusForbidden},
		{"text", notification(t, "/s", 1, 1), []string{"Content-Type", "text/plain"}, http.StatusUnsupportedMediaType},
		{"garbage", []byte("<x"), nil, http.StatusBadRequest},
		{"not a notification", []byte(`<Time xmlns="urn:ieee:std:2030.5:ns"/>`), nil, http.StatusBadRequest},
		{"too large", []byte(strings.Repeat(" ", 5000)), nil, http.StatusRequestEntityTooLarge},
	} {
		if code := post(c.body, c.header...); code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ntfy", nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestSubscribeWithoutLocation(t *testing.T) {
	p := newPKI(t)
	srv := server(t, p, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	c := newClient(t, p, srv, client.Config{})
	if _, err := c.Subscribe(context.Background(), "/edev/1/sub", "/edev/1", "https://x/ntfy", 1); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("Subscribe = %v", err)
	}
	if err := c.Unsubscribe(context.Background(), "/edev/1/sub/1"); err != nil {
		t.Errorf("Unsubscribe = %v", err)
	}
}
