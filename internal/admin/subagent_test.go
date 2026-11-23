package admin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/agent"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func TestSubagentHealthStatus(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	rows := []stats.Request{
		// opencode: traffic and markers -> ok
		{ID: "o1", TS: base.Add(time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli", Subagent: true, SubagentSource: agent.SourceOpenCode},
		{ID: "o2", TS: base.Add(2 * time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli"},
		// claude-code: traffic, no markers -> regressed
		{ID: "c1", TS: base.Add(3 * time.Minute), Provider: "p", Model: "m", UserAgent: "claude-cli/2.1.328 (external)"},
		// codex: no traffic at all -> absent
	}
	for _, row := range rows {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/subagent/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got []subagentHealthRow
	if err := jsonDecode(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	byClient := map[string]subagentHealthRow{}
	for _, r := range got {
		byClient[r.Client] = r
	}
	if r := byClient["opencode"]; r.Status != subagentStatusOK || r.Requests != 2 || r.Marked != 1 {
		t.Fatalf("opencode = %+v, want ok / 2 / 1", r)
	}
	if r := byClient["claude-code"]; r.Status != subagentStatusRegressed || r.Marked != 0 || r.MarkedPct != 0 {
		t.Fatalf("claude-code = %+v, want regressed with zero markedPct", r)
	}
	if r := byClient["codex"]; r.Status != subagentStatusAbsent {
		t.Fatalf("codex = %+v, want absent", r)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want one per known client", len(got))
	}
}

func TestSubagentHealthRejectsBadWindow(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/subagent/health?from=not-a-time", "", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unparsable window", resp.StatusCode)
	}
}

func TestSubagentHealthRequiresAuth(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	resp, err := http.Get(ts.URL + "/api/subagent/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a session", resp.StatusCode)
	}
}
