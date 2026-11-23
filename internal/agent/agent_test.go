package agent_test

import (
	"net/http"
	"testing"

	"github.com/go-gost/core/routing"
	xrouting "github.com/go-gost/x/routing"

	"github.com/ginuerzh/cuttle/internal/agent"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		value   string
		want    string
		wantSub bool
	}{
		{"claude code subagent", "x-claude-code-agent-id", "agent-7", agent.SourceClaudeCode, true},
		{"opencode child session", "X-Parent-Session-Id", "ses_parent1", agent.SourceOpenCode, true},
		{"codex review", "x-openai-subagent", "review", agent.SourceCodex, true},
		{"codex empty value still present", "x-openai-subagent", "", agent.SourceCodex, true},
		{"unrelated header", "user-agent", "opencode/2.3.0", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.header != "" {
				h.Set(tt.header, tt.value)
			}
			got, sub := agent.Classify(h)
			if got != tt.want || sub != tt.wantSub {
				t.Fatalf("Classify = %q,%v want %q,%v", got, sub, tt.want, tt.wantSub)
			}
		})
	}
	if got, sub := agent.Classify(nil); got != "" || sub {
		t.Fatalf("Classify(nil) = %q,%v want empty,false", got, sub)
	}
}

func TestClassifyPrecedence(t *testing.T) {
	h := http.Header{}
	h.Set("x-openai-subagent", "collab_spawn")
	h.Set("x-parent-session-id", "ses_parent1")
	h.Set("x-claude-code-agent-id", "agent-7")
	if got, _ := agent.Classify(h); got != agent.SourceClaudeCode {
		t.Fatalf("Classify = %q want %q (table order)", got, agent.SourceClaudeCode)
	}
}

func TestSubagentRuleMatchesEveryMarker(t *testing.T) {
	m, err := xrouting.NewMatcher(agent.SubagentRule())
	if err != nil {
		t.Fatalf("rule does not parse: %v", err)
	}
	for _, h := range []string{"x-claude-code-agent-id", "x-parent-session-id", "x-openai-subagent"} {
		req := &routing.Request{Header: http.Header{}}
		req.Header.Set(h, "v")
		if !m.Match(req) {
			t.Fatalf("rule missed %q", h)
		}
	}
	if m.Match(&routing.Request{Header: http.Header{}}) {
		t.Fatal("rule matched a marker-less request")
	}
	// An empty-valued marker must match: x tests map-key presence.
	req := &routing.Request{Header: http.Header{"X-Openai-Subagent": {""}}}
	if !m.Match(req) {
		t.Fatal("rule missed an empty-valued marker")
	}
}
