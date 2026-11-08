package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func clientIDFor(headers map[string]string) string {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return clientIDFromRequest(&http.Request{Header: h})
}

func TestClientIDFromRequest(t *testing.T) {
	if got := clientIDFor(nil); got != "" {
		t.Fatalf("no session header = %q, want empty", got)
	}

	for _, name := range sessionHeaders {
		got := clientIDFor(map[string]string{name: "session-value"})
		if got == "" {
			t.Fatalf("header %q produced no client ID", name)
		}
		if strings.Contains(got, "session-value") {
			t.Fatalf("header %q leaked the raw value in %q", name, got)
		}
	}

	first := clientIDFor(map[string]string{"x-claude-code-session-id": "sess-1"})
	if again := clientIDFor(map[string]string{"x-claude-code-session-id": "sess-1"}); again != first {
		t.Fatalf("same session = %q and %q, want equal", first, again)
	}
	if other := clientIDFor(map[string]string{"x-claude-code-session-id": "sess-2"}); other == first {
		t.Fatalf("different sessions share the client ID %q", first)
	}

	if got := clientIDFor(map[string]string{"x-claude-code-session-id": "   "}); got != "" {
		t.Fatalf("whitespace session = %q, want empty", got)
	}

	// The first configured header wins when several are present.
	both := clientIDFor(map[string]string{
		"x-claude-code-session-id": "sess-1",
		"x-session-id":             "sess-2",
	})
	if both != first {
		t.Fatalf("precedence = %q, want %q", both, first)
	}
}

func TestClientIDFromRequestSubagent(t *testing.T) {
	lead := clientIDFor(map[string]string{"x-claude-code-session-id": "sess-1"})
	sub := clientIDFor(map[string]string{
		"x-claude-code-session-id": "sess-1",
		"x-claude-code-agent-id":   "agent-1",
	})
	if sub == lead {
		t.Fatal("subagent must not share the lead's client ID")
	}
	if strings.Contains(sub, "agent-1") {
		t.Fatalf("subagent ID leaked the raw value: %q", sub)
	}
	if again := clientIDFor(map[string]string{
		"x-claude-code-session-id": "sess-1",
		"x-claude-code-agent-id":   "agent-1",
	}); again != sub {
		t.Fatalf("same subagent = %q and %q, want equal", sub, again)
	}
	if other := clientIDFor(map[string]string{
		"x-claude-code-session-id": "sess-1",
		"x-claude-code-agent-id":   "agent-2",
	}); other == sub {
		t.Fatal("different subagents share the client ID")
	}

	// An agent header without a session header names no conversation.
	if got := clientIDFor(map[string]string{"x-claude-code-agent-id": "agent-1"}); got != "" {
		t.Fatalf("agent without session = %q, want empty", got)
	}
}
