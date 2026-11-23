// Package agent classifies which AI client a request came from. It is the
// single source of truth for the subagent marker headers: internal/stats
// records the class on every request row from this table, and later routing
// work compiles its matcher from the same table, so the two cannot drift.
package agent

import (
	"net/http"
	"strings"
)

// Source labels identify which client's marker a request carried. They are
// stored as stats.subagent_source and must stay stable.
const (
	SourceClaudeCode = "claude_code"
	SourceOpenCode   = "opencode"
	SourceCodex      = "codex"
)

// marker ties a client's source label to the header it sends on subagent
// requests. Presence of the header marks a subagent; the value is never read.
type marker struct {
	source string
	header string
}

// markers is ordered: the first present header wins, so a request carrying
// several markers always classifies the same way.
var markers = []marker{
	{SourceClaudeCode, "x-claude-code-agent-id"},
	{SourceOpenCode, "x-parent-session-id"},
	{SourceCodex, "x-openai-subagent"},
}

// Classify reports whether the request carries a subagent marker and which
// client's it is. It tests map-key presence, not the value, because x's
// Header(name) routing matcher does the same: an empty-valued marker must
// classify exactly like a present one.
func Classify(h http.Header) (source string, subagent bool) {
	for _, m := range markers {
		if _, ok := h[http.CanonicalHeaderKey(m.header)]; ok {
			return m.source, true
		}
	}
	return "", false
}

// SubagentRule returns the x routing rule that matches any subagent marker. It
// is generated from the same table Classify uses, so the dispatch decision and
// the recorded class cannot drift.
func SubagentRule() string {
	clauses := make([]string, 0, len(markers))
	for _, m := range markers {
		clauses = append(clauses, `Header("`+m.header+`")`)
	}
	return strings.Join(clauses, " || ")
}
