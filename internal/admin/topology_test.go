package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/ginuerzh/cuttle/internal/stats"
)

func TestTopologyPositionsAPI(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	readPositions := func() map[string]stats.Position {
		t.Helper()
		resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d", resp.StatusCode)
		}
		var got struct {
			Positions map[string]stats.Position `json:"positions"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got.Positions
	}

	if got := readPositions(); len(got) != 0 {
		t.Fatalf("initial positions = %+v", got)
	}

	want := map[string]stats.Position{
		"entrypoint:fast": {X: 12, Y: 34},
		"route:fast":      {X: 56, Y: 78},
	}
	body, _ := json.Marshal(map[string]any{"positions": want})
	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions", string(body), csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put status = %d", resp.StatusCode)
	}
	if got := readPositions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %+v, want %+v", got, want)
	}

	for _, body := range []string{`{}`, `{"positions":null}`} {
		resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions", body, csrf)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing positions %q status = %d, want 400", body, resp.StatusCode)
		}
		if got := readPositions(); !reflect.DeepEqual(got, want) {
			t.Fatalf("positions after %q = %+v, want unchanged", body, got)
		}
	}

	resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions",
		`{"positions":{"":{"x":1,"y":2}}}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty id status = %d, want 400", resp.StatusCode)
	}
	if got := readPositions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions after empty id = %+v, want unchanged", got)
	}

	oversized := make(map[string]stats.Position, maxTopologyPositions+1)
	for i := 0; i <= maxTopologyPositions; i++ {
		oversized[fmt.Sprintf("node-%d", i)] = stats.Position{}
	}
	body, _ = json.Marshal(map[string]any{"positions": oversized})
	resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions", string(body), csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized map status = %d, want 400", resp.StatusCode)
	}
	if got := readPositions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions after oversized map = %+v, want unchanged", got)
	}

	resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions",
		`{"positions":{}}`, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear status = %d", resp.StatusCode)
	}
	if got := readPositions(); len(got) != 0 {
		t.Fatalf("cleared positions = %+v", got)
	}

	resp = doJSON(t, &http.Client{}, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}

	srv.stats = nil
	resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions",
		`{"positions":{}}`, csrf)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing store put status = %d, want 503", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing store get status = %d, want 503", resp.StatusCode)
	}
}
