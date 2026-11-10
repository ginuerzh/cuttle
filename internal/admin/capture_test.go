package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/capture"
	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func TestCaptureAPI(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	routeID := identity.Derive(identity.Route, "fast")
	srv.mu.Lock()
	srv.cfg.Routes = []config.Route{{ID: routeID, Name: "fast"}}
	srv.mu.Unlock()

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/captures", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var rules []capture.Rule
	if err := json.NewDecoder(resp.Body).Decode(&rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("initial rules = %+v", rules)
	}

	body := `{"route":"` + routeID + `","clientModel":"claude-*","reason":"investigate","limit":5,"ttlSeconds":60}`
	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/captures", body, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var rule capture.Rule
	if err := json.NewDecoder(resp.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.ID == "" || rule.Route != routeID || rule.ClientModel != "claude-*" || rule.Limit != 5 {
		t.Fatalf("rule = %+v", rule)
	}

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/captures", "", "")
	if err := json.NewDecoder(resp.Body).Decode(&rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules = %+v", rules)
	}

	resp = doJSON(t, client, http.MethodDelete, ts.URL+"/api/captures/"+rule.ID, "", csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/captures", "", "")
	if err := json.NewDecoder(resp.Body).Decode(&rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules after delete = %+v", rules)
	}

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/captures",
		`{"route":"missing","reason":"bad","limit":1,"ttlSeconds":60}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown route status = %d, want 400", resp.StatusCode)
	}

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/captures",
		`{"reason":"bad","limit":0,"ttlSeconds":60}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want 400", resp.StatusCode)
	}

	resp = doJSON(t, &http.Client{}, http.MethodGet, ts.URL+"/api/captures", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}
}

func TestRequestsExposeCapture(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	row := stats.Request{ID: "rid", SID: "rid", TS: time.Now().Add(-time.Minute).UTC(), Provider: "p"}
	if err := rel.store.Insert(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	if err := rel.store.PutCapture(t.Context(), "rid", &capture.Capture{
		RequestRewritten: "out", Reason: "rule:1", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?captured=1", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var rows []stats.Request
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].HasCapture {
		t.Fatalf("rows = %+v", rows)
	}

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/requests/rid", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", resp.StatusCode)
	}
	var got stats.Request
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Capture == nil || got.Capture.RequestRewritten != "out" {
		t.Fatalf("detail = %+v", got)
	}
}
