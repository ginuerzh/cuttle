package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// attemptJSON mirrors attemptView on the wire, so the tests assert the JSON
// shape the console consumes rather than the Go type.
type attemptJSON struct {
	RequestID    string `json:"requestId"`
	Attempt      int    `json:"attempt"`
	Status       int    `json:"status"`
	Error        string `json:"error"`
	ErrorClass   string `json:"errorClass"`
	ProviderName string `json:"providerName"`
	NodeName     string `json:"nodeName"`
	RouteName    string `json:"routeName"`
}

// seedAttemptLabels names the entities the decoded attempt payloads refer to.
func seedAttemptLabels(s *Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Providers = []config.Provider{{ID: "p1", Name: "OpenAI"}}
	s.cfg.Nodes = []config.Node{
		{ID: "n1", Name: "Node One"},
		{ID: "n2", Name: "Node Two"},
	}
	s.cfg.Routes = []config.Route{{ID: "r1", Name: "Primary"}}
}

func TestAttemptsListFiltersAndLabels(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	seedAttemptLabels(s)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for _, row := range []stats.Request{
		{ID: "req1", TS: base, Route: "r1", Node: "n1", Provider: "p1", Status: 200, Retries: 1,
			Attempts: []stats.Attempt{
				{Attempt: 0, Node: "n1", Status: 429, Error: "rate limited", ErrorClass: "upstream_quota"},
				{Attempt: 1, Node: "n2", Status: 503, ErrorClass: "upstream_unavailable"},
			}},
		{ID: "req2", TS: base.Add(time.Minute), Route: "r1", Node: "n1", Provider: "p1", Status: 200, Retries: 1,
			Attempts: []stats.Attempt{
				{Attempt: 0, Node: "n1", Status: 401, ErrorClass: "upstream_auth"},
			}},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	get := func(v url.Values) []byte {
		t.Helper()
		u := ts.URL + "/api/attempts"
		if len(v) > 0 {
			u += "?" + v.Encode()
		}
		resp := doJSON(t, client, http.MethodGet, u, "", "")
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET %s = %d: %s", u, resp.StatusCode, b)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	list := func(v url.Values) []attemptJSON {
		t.Helper()
		var rows []attemptJSON
		if err := jsonDecode(bytes.NewReader(get(v)), &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	keys := func(rows []attemptJSON) []string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.RequestID+"/"+strconv.Itoa(row.Attempt)+" "+strconv.Itoa(row.Status))
		}
		return out
	}

	if got, want := strings.Join(keys(list(nil)), ","), "req2/0 401,req1/1 503,req1/0 429"; got != want {
		t.Fatalf("all attempts = %s, want %s", got, want)
	}

	all := list(nil)
	// Labels come from the current configuration, and an attempt inherits its
	// parent's ids, so the decorated payload names the entities.
	if all[0].NodeName != "Node One" || all[0].RouteName != "Primary" || all[0].ProviderName != "OpenAI" {
		t.Fatalf("labels = %+v", all[0])
	}
	if all[0].ErrorClass != "upstream_auth" || all[0].Error != "" {
		t.Fatalf("payload = %+v", all[0])
	}
	if all[2].Error != "rate limited" {
		t.Fatalf("attempt error = %+v, want the recorded message", all[2])
	}

	if got, want := strings.Join(keys(list(url.Values{"node": {"n2"}})), ","), "req1/1 503"; got != want {
		t.Fatalf("node=n2 attempts = %s, want %s", got, want)
	}
	if got := list(url.Values{"node": {"n2"}}); got[0].NodeName != "Node Two" {
		t.Fatalf("node label = %+v", got[0])
	}
	if got, want := strings.Join(keys(list(url.Values{"status": {"429"}})), ","), "req1/0 429"; got != want {
		t.Fatalf("status=429 attempts = %s, want %s", got, want)
	}
	// The filter applies only to a positive status, so 0 passes through as "no
	// status filter" rather than selecting dial failures.
	if got, want := strings.Join(keys(list(url.Values{"status": {"0"}})), ","), "req2/0 401,req1/1 503,req1/0 429"; got != want {
		t.Fatalf("status=0 attempts = %s, want %s", got, want)
	}
	if got, want := strings.Join(keys(list(url.Values{"errorClass": {"upstream_quota"}})), ","), "req1/0 429"; got != want {
		t.Fatalf("errorClass attempts = %s, want %s", got, want)
	}
	window := url.Values{
		"from": {base.Format(time.RFC3339Nano)},
		"to":   {base.Add(30 * time.Second).Format(time.RFC3339Nano)},
	}
	if got, want := strings.Join(keys(list(window)), ","), "req1/1 503,req1/0 429"; got != want {
		t.Fatalf("window attempts = %s, want %s", got, want)
	}
	if got, want := strings.Join(keys(list(url.Values{"limit": {"1"}})), ","), "req2/0 401"; got != want {
		t.Fatalf("limit=1 attempts = %s, want %s", got, want)
	}

	// An empty result is an empty JSON array, never null.
	if body := strings.TrimSpace(string(get(url.Values{"node": {"absent"}}))); body != "[]" {
		t.Fatalf("empty list body = %q, want []", body)
	}
}

func TestRequestDetailCarriesAttempts(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	seedAttemptLabels(s)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for _, row := range []stats.Request{
		{ID: "req1", TS: base, Route: "r1", Node: "n1", Provider: "p1", Status: 200, Retries: 1,
			Attempts: []stats.Attempt{
				{Attempt: 0, Node: "n1", Status: 429, ErrorClass: "upstream_quota"},
				{Attempt: 1, Node: "n2", Status: 503, ErrorClass: "upstream_unavailable"},
			}},
		{ID: "req2", TS: base.Add(time.Minute), Route: "r1", Node: "n1", Provider: "p1", Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	detail := func(id string) map[string]json.RawMessage {
		t.Helper()
		resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests/"+id, "", "")
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET /api/requests/%s = %d: %s", id, resp.StatusCode, b)
		}
		var payload map[string]json.RawMessage
		if err := jsonDecode(resp.Body, &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	raw, ok := detail("req1")["attempts"]
	if !ok {
		t.Fatal("request detail has no attempts key")
	}
	var attempts []attemptJSON
	if err := json.Unmarshal(raw, &attempts); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want 2", attempts)
	}
	// Attempts read in the order they were discarded.
	if attempts[0].Attempt != 0 || attempts[0].Status != 429 || attempts[0].NodeName != "Node One" {
		t.Fatalf("first attempt = %+v", attempts[0])
	}
	if attempts[1].Attempt != 1 || attempts[1].Status != 503 || attempts[1].NodeName != "Node Two" {
		t.Fatalf("second attempt = %+v", attempts[1])
	}

	if _, ok := detail("req2")["attempts"]; ok {
		t.Fatal("request without attempts returned an attempts key")
	}

	// The list endpoint never carries attempts.
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests", "", "")
	var rows []map[string]json.RawMessage
	if err := jsonDecode(resp.Body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("request rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if _, ok := row["attempts"]; ok {
			t.Fatalf("list row carries attempts: %v", row)
		}
	}
}
