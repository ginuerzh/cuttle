package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/go-gost/llm-gw/internal/health"
	"github.com/go-gost/llm-gw/internal/stats"
)

type eventSnapshot struct {
	Type    string             `json:"type"`
	Since   string             `json:"since"`
	To      string             `json:"to"`
	Metrics stats.GraphMetrics `json:"metrics"`
	Health  []health.Route     `json:"health"`
}

func newEventsAdmin(t *testing.T) (*Server, *recordingReloader, *httptest.Server) {
	t.Helper()
	s, rel, _, ts := newTestAdmin(t)
	t.Cleanup(s.Close)
	s.wsInterval = 20 * time.Millisecond
	s.wsPctInterval = 20 * time.Millisecond
	s.wsMaxAge = 2 * time.Hour
	return s, rel, ts
}

func dialEvents(t *testing.T, tsURL string, jar http.CookieJar, query, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(tsURL, "http") + "/api/events" + query
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	conn, resp, err := (&websocket.Dialer{Jar: jar}).Dial(u, h)
	if conn != nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func readSnapshot(t *testing.T, conn *websocket.Conn) eventSnapshot {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var msg eventSnapshot
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		switch msg.Type {
		case "snapshot":
			return msg
		case "error":
			t.Fatalf("server error: %+v", msg)
		}
	}
}

func waitForRequests(t *testing.T, conn *websocket.Conn, want int64) eventSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := int64(0)
	for {
		snap := readSnapshot(t, conn)
		got := snap.Metrics.Nodes["provider:p"].Requests
		if got < last {
			t.Fatalf("requests decreased: %d -> %d", last, got)
		}
		last = got
		if got == want {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("requests = %d, want %d", got, want)
		}
	}
}

func insertRequest(t *testing.T, rel *recordingReloader, id string, ts time.Time) {
	t.Helper()
	if err := rel.store.Insert(context.Background(), stats.Request{
		ID: id, TS: ts, Provider: "p", Status: 200, DurationMS: 10,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEventsCarriesRouteHealth(t *testing.T) {
	_, rel, ts := newEventsAdmin(t)
	rel.health = []health.Route{{
		Name:        "r",
		MaxFails:    1,
		FailTimeout: "10s",
		Nodes:       []health.Node{{Name: "n", FailCount: 1, Dead: true}},
	}}
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", "")
	if err != nil {
		t.Fatal(err)
	}
	snap := readSnapshot(t, conn)
	if len(snap.Health) != 1 || !snap.Health[0].Nodes[0].Dead {
		t.Fatalf("health = %+v", snap.Health)
	}
}

func TestEventsRequiresAuth(t *testing.T) {
	_, _, ts := newEventsAdmin(t)
	conn, resp, err := dialEvents(t, ts.URL, nil, "?range=1h", "")
	if conn != nil {
		t.Fatalf("unexpected connection")
	}
	if err != websocket.ErrBadHandshake {
		t.Fatalf("err = %v, want ErrBadHandshake", err)
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestEventsRejectsCrossOrigin(t *testing.T) {
	_, _, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)

	conn, resp, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", "http://evil.example")
	if conn != nil {
		t.Fatalf("unexpected connection")
	}
	if err != websocket.ErrBadHandshake {
		t.Fatalf("err = %v, want ErrBadHandshake", err)
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}

	conn, _, err = dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readSnapshot(t, conn)
}

func TestEventsSnapshotIsMonotonicAndCatchesLateRows(t *testing.T) {
	_, rel, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	first := readSnapshot(t, conn)
	if got := first.Metrics.Nodes["provider:p"].Requests; got != 0 {
		t.Fatalf("initial requests = %d, want 0", got)
	}

	insertRequest(t, rel, "first", time.Now())
	waitForRequests(t, conn, 1)

	// The row started before the previous tick but is inserted late.
	insertRequest(t, rel, "late", time.Now().Add(-time.Minute))
	waitForRequests(t, conn, 2)
}

func TestEventsResetReconnectsAtZero(t *testing.T) {
	_, rel, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	insertRequest(t, rel, "before-reset", time.Now())
	waitForRequests(t, conn, 1)
	conn.Close()

	conn, _, err = dialEvents(t, ts.URL, client.Jar, "?reset=1", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	snap := readSnapshot(t, conn)
	if got := snap.Metrics.Nodes["provider:p"].Requests; got != 0 {
		t.Fatalf("reset requests = %d, want 0", got)
	}
	since, err := time.Parse(time.RFC3339, snap.Since)
	if err != nil {
		t.Fatal(err)
	}
	if delta := time.Since(since); delta < -5*time.Second || delta > 10*time.Second {
		t.Fatalf("reset since = %v, delta = %v", since, delta)
	}

	insertRequest(t, rel, "after-reset", time.Now())
	waitForRequests(t, conn, 1)
}

func TestEventsClosesOnLogout(t *testing.T) {
	_, _, ts := newEventsAdmin(t)
	client, csrf := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readSnapshot(t, conn)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/logout", "", csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", resp.StatusCode)
	}
	expectClosed(t, conn)
}

func TestEventsClosesOnSessionExpiry(t *testing.T) {
	s, _, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readSnapshot(t, conn)

	s.mu.Lock()
	s.sessions = make(map[string]time.Time)
	s.mu.Unlock()
	expectClosed(t, conn)
}

func TestEventsClosesOnServerClose(t *testing.T) {
	s, _, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readSnapshot(t, conn)

	s.Close()
	expectClosed(t, conn)
}

func TestEventsClosesWhenSessionExceedsMaxAge(t *testing.T) {
	s, _, ts := newEventsAdmin(t)
	client, _ := login(t, ts.URL)
	conn, _, err := dialEvents(t, ts.URL, client.Jar, "?range=1h", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readSnapshot(t, conn)
	s.wsMaxAge = 50 * time.Millisecond
	expectClosed(t, conn)
}

func expectClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func TestParseSubscription(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		query url.Values
		want  time.Time
		ok    bool
	}{
		{"range", url.Values{"range": {"1h"}}, now.Add(-time.Hour), true},
		{"reset", url.Values{"reset": {"1"}}, now, true},
		{"since", url.Values{"since": {now.Add(-2 * time.Hour).Format(time.RFC3339)}}, now.Add(-2 * time.Hour), true},
		{"future since clamps", url.Values{"since": {now.Add(time.Hour).Format(time.RFC3339)}}, now, true},
		{"old since clamps", url.Values{"since": {now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)}}, now.Add(-7 * 24 * time.Hour), true},
		{"range wins default", url.Values{}, now.Add(-time.Hour), true},
		{"invalid range", url.Values{"range": {"nope"}}, time.Time{}, false},
		{"range too short", url.Values{"range": {"30s"}}, time.Time{}, false},
		{"range too long", url.Values{"range": {"25h"}}, time.Time{}, false},
		{"invalid since", url.Values{"since": {"bad"}}, time.Time{}, false},
		{"since wins over range", url.Values{"range": {"1h"}, "since": {now.Add(-2 * time.Hour).Format(time.RFC3339)}}, now.Add(-2 * time.Hour), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSubscription(tc.query, now)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				if !got.Equal(tc.want) {
					t.Fatalf("since = %v, want %v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("since = %v, want error", got)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"empty", "", true},
		{"match", "http://example.com", true},
		{"mismatch", "http://evil.example", false},
		{"port mismatch", "http://example.com:8080", false},
		{"invalid", "://bad", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.com/api/events", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}
