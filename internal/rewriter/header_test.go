package rewriter

import (
	"strings"
	"testing"

	corerewriter "github.com/go-gost/core/rewriter"
)

func TestHeaderRewriterAddsRequestID(t *testing.T) {
	in := []byte("Content-Type: application/json\r\n\r\n")
	md := map[string]any{"sid": "req-123", "direction": "response", "kind": "header"}
	out, err := (&HeaderRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(md))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "X-Llm-Gw-Request-Id: req-123") {
		t.Fatalf("header missing: %q", s)
	}
	if !strings.Contains(s, "Content-Type: application/json") {
		t.Fatalf("original header missing: %q", s)
	}
}

func TestOpenCodeSessionRewriterAddsFallback(t *testing.T) {
	in := []byte("Content-Type: application/json\r\n\r\n")
	out, err := (&OpenCodeSessionRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
		map[string]any{"sid": "req-123"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "X-Opencode-Session: req-123") {
		t.Fatalf("session header missing: %q", out)
	}
}

func TestOpenCodeSessionRewriterPreservesClientSession(t *testing.T) {
	in := []byte("Content-Type: application/json\r\nx-opencode-session: client-session\r\n\r\n")
	out, err := (&OpenCodeSessionRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
		map[string]any{"sid": "req-123"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(out)), "x-opencode-session: client-session") {
		t.Fatalf("client session changed: %q", out)
	}
	if strings.Contains(string(out), "X-Opencode-Session: req-123") {
		t.Fatalf("fallback overwrote client session: %q", out)
	}
}

func TestOpenCodeSessionRewriterPreservesNativeClaudeSession(t *testing.T) {
	in := []byte("Content-Type: application/json\r\nx-claude-code-session-id: native-session\r\n\r\n")
	out, err := (&OpenCodeSessionRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
		map[string]any{"sid": "req-123"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(out)), "x-opencode-session") {
		t.Fatalf("native session should not get a custom header: %q", out)
	}
}

func TestOpenCodeSessionRewriterFailsOpen(t *testing.T) {
	in := []byte("not a header block")
	out, err := (&OpenCodeSessionRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
		map[string]any{"sid": "req-123"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("output changed: %q", out)
	}
}

func TestHeaderRewriterFailsOpen(t *testing.T) {
	in := []byte("not a header block")
	out, err := (&HeaderRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
		map[string]any{"sid": "req-123"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("output changed: %q", out)
	}
}
