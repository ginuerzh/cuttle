package admin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// The entrypoint filter must follow entrypoint order, the same rule the data
// plane and the monitor graph use: a row belongs to the first entrypoint whose
// glob matches its client model. A catch-all therefore lists only the rows no
// earlier entrypoint handled.
func TestRequestsEntrypointFilterFollowsEntrypointOrder(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	s.mu.Lock()
	s.cfg.Entrypoints = []config.Entrypoint{
		{ID: "e_0000000001", Name: "claude", Match: []string{"claude-*"}, Routes: []config.RouteBinding{{Route: "r"}}},
		{ID: "e_0000000002", Name: "fallback", Match: []string{"*"}, Routes: []config.RouteBinding{{Route: "r"}}},
	}
	s.mu.Unlock()
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	for _, row := range []stats.Request{
		{ID: "r1", TS: base, ClientModel: "claude-opus", Route: "r", Status: 200},
		{ID: "r2", TS: base.Add(time.Minute), ClientModel: "gpt-4.5", Route: "r", Status: 200},
		{ID: "r3", TS: base.Add(2 * time.Minute), ClientModel: "", Route: "r", Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(entrypoint string) []string {
		resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?entrypoint="+entrypoint, "", "")
		var rows []stats.Request
		if err := jsonDecode(resp.Body, &rows); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}

	if got := ids("e_0000000001"); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("claude rows = %v, want [r1]", got)
	}
	if got := ids("e_0000000002"); len(got) != 2 || got[0] != "r3" || got[1] != "r2" {
		t.Fatalf("catch-all rows = %v, want [r3 r2]", got)
	}
}
