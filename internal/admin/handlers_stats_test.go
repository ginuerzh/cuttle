package admin

import (
	"context"
	"io"
	"net/http"
	"strings"
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

func TestRequestsSubagentFilter(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	for _, row := range []stats.Request{
		{ID: "sub", TS: base, Provider: "p_0000000001", Model: "m", Status: 200, Subagent: true, SubagentSource: "opencode"},
		{ID: "lead", TS: base.Add(time.Minute), Provider: "p_0000000001", Model: "m", Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(query string) []string {
		resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests"+query, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", query, resp.StatusCode)
		}
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

	if got := ids("?subagent=1"); len(got) != 1 || got[0] != "sub" {
		t.Fatalf("subagent=1 rows = %v, want [sub]", got)
	}
	if got := ids("?subagent=0"); len(got) != 1 || got[0] != "lead" {
		t.Fatalf("subagent=0 rows = %v, want [lead]", got)
	}
	if got := ids(""); len(got) != 2 {
		t.Fatalf("unfiltered rows = %v, want both", got)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?subagent=maybe", "", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("subagent=maybe status = %d, want 400", resp.StatusCode)
	}
}

func TestRequestDetailCarriesSubagentClass(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	if err := rel.store.Insert(context.Background(), stats.Request{
		ID: "sub", TS: base, Provider: "p_0000000001", Model: "m", Status: 200,
		Subagent: true, SubagentSource: "codex",
	}); err != nil {
		t.Fatal(err)
	}

	detail := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests/sub", "", "")
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", detail.StatusCode)
	}
	var body map[string]any
	if err := jsonDecode(detail.Body, &body); err != nil {
		t.Fatal(err)
	}
	if got, ok := body["subagent"]; !ok || got != true {
		t.Fatalf("detail subagent = %v (present %v), want true", got, ok)
	}
	if got := body["subagentSource"]; got != "codex" {
		t.Fatalf("detail subagentSource = %v, want codex", got)
	}

	list := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests", "", "")
	raw, err := io.ReadAll(list.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"subagent"`) || strings.Contains(string(raw), `"subagentSource"`) {
		t.Fatalf("list payload leaks the class: %s", raw)
	}
}
