package stats

import (
	"context"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/agent"
)

func TestStoreSubagentHealth(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		// opencode: four requests, three marked.
		{ID: "o1", TS: base.Add(time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli", Subagent: true, SubagentSource: agent.SourceOpenCode},
		{ID: "o2", TS: base.Add(2 * time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli", Subagent: true, SubagentSource: agent.SourceOpenCode},
		{ID: "o3", TS: base.Add(3 * time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli", Subagent: true, SubagentSource: agent.SourceOpenCode},
		{ID: "o4", TS: base.Add(4 * time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli"},
		// claude-code: two requests, both marked.
		{ID: "c1", TS: base.Add(5 * time.Minute), Provider: "p", Model: "m", UserAgent: "claude-cli/2.1.328 (external)", Subagent: true, SubagentSource: agent.SourceClaudeCode},
		{ID: "c2", TS: base.Add(6 * time.Minute), Provider: "p", Model: "m", UserAgent: "claude-cli/2.1.328 (external)", Subagent: true, SubagentSource: agent.SourceClaudeCode},
		// An opencode-UA request carrying the codex marker must not count as
		// opencode being marked: the check exists to catch opencode's own
		// header going missing, and a fork that injects someone else's marker
		// would otherwise mask exactly that regression.
		{ID: "o5", TS: base.Add(8 * time.Minute), Provider: "p", Model: "m", UserAgent: "opencode/latest/2.3.2/cli", Subagent: true, SubagentSource: agent.SourceCodex},
		// An unknown agent contributes no client row.
		{ID: "u1", TS: base.Add(7 * time.Minute), Provider: "p", Model: "m", UserAgent: "Python-urllib/3.14"},
	}
	for _, r := range rows {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.SubagentHealth(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byClient := map[string]SubagentHealthRow{}
	for _, r := range got {
		byClient[r.Client] = r
	}
	if r, ok := byClient["opencode"]; !ok || r.Requests != 5 || r.Marked != 3 {
		t.Fatalf("opencode = %+v, want 5 requests / 3 marked (the codex-marked one does not count)", r)
	}
	if r, ok := byClient["claude-code"]; !ok || r.Requests != 2 || r.Marked != 2 {
		t.Fatalf("claude-code = %+v, want 2 requests / 2 marked", r)
	}
	if _, ok := byClient[""]; ok {
		t.Fatalf("an unknown agent must not produce a client row: %+v", got)
	}
	if _, ok := byClient["codex"]; ok {
		t.Fatalf("a client with no traffic must be absent from the store result: %+v", got)
	}

	// The window must bound the result.
	none, err := s.SubagentHealth(ctx, base.Add(-2*time.Hour), base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("rows outside the window = %+v, want none", none)
	}
}
