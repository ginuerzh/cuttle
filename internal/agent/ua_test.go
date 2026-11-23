package agent_test

import (
	"testing"

	"github.com/ginuerzh/cuttle/internal/agent"
)

func TestClientOf(t *testing.T) {
	cases := map[string]string{
		"claude-cli/2.1.328 (external, cli)": "claude-code",
		"opencode/latest/2.3.2/cli":          "opencode",
		"codex_cli_rs/0.167.1":               "codex",
		"Python-urllib/3.14":                 "",
		"curl/8.5.0":                         "",
		"":                                   "",
		"  opencode/2.3.0/cli  ":             "opencode",
	}
	for ua, want := range cases {
		if got := agent.ClientOf(ua); got != want {
			t.Fatalf("ClientOf(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestClientOfIsCaseInsensitive(t *testing.T) {
	for _, ua := range []string{"OpenCode/2.3.2", "OPENCODE/2.3.2/cli", "Opencode/latest"} {
		if got := agent.ClientOf(ua); got != "opencode" {
			t.Fatalf("ClientOf(%q) = %q, want opencode", ua, got)
		}
	}
}

func TestKnownClients(t *testing.T) {
	got := agent.KnownClients()
	want := []string{"claude-code", "opencode", "codex"}
	if len(got) != len(want) {
		t.Fatalf("KnownClients = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KnownClients = %v, want %v", got, want)
		}
	}
	// The returned slice must not alias package state.
	got[0] = "mutated"
	if agent.KnownClients()[0] != "claude-code" {
		t.Fatal("KnownClients returned an aliased slice")
	}
}
