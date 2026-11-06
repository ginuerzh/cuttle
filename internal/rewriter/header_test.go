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
