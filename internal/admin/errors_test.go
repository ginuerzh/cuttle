package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/stats"
)

func TestErrorSummaryAndAlertsEndpoints(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	if err := rel.store.Insert(t.Context(), stats.Request{
		ID: "e1", TS: time.Now().Add(-time.Minute), Provider: "p", Route: "r",
		ClientModel: "m", Status: 401, Error: "invalid credential",
		ErrorClass: stats.ErrorClassUpstreamAuth,
	}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/stats/errors", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("errors status = %d", resp.StatusCode)
	}
	var sum stats.ErrorSummary
	if err := json.NewDecoder(resp.Body).Decode(&sum); err != nil {
		t.Fatal(err)
	}
	if sum.Errors != 1 || len(sum.ByClass) != 1 || sum.ByClass[0].Key != stats.ErrorClassUpstreamAuth {
		t.Fatalf("summary = %+v", sum)
	}

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/alerts", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts status = %d", resp.StatusCode)
	}
	var alerts []stats.Alert
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		t.Fatal(err)
	}
	if len(alerts) == 0 || alerts[0].ID != "auth" {
		t.Fatalf("alerts = %+v", alerts)
	}
}
