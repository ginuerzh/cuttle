package main

import "testing"

func TestParseServeArgs(t *testing.T) {
	got, err := parseServeArgs([]string{"serve", "-c", "x.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "x.yaml" {
		t.Fatalf("path = %q, want x.yaml", got)
	}

	got, err = parseServeArgs([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "llm-gw.yaml" {
		t.Fatalf("default path = %q, want llm-gw.yaml", got)
	}

	if _, err := parseServeArgs(nil); err == nil {
		t.Fatal("want usage error")
	}
	if _, err := parseServeArgs([]string{"run"}); err == nil {
		t.Fatal("want usage error for unknown subcommand")
	}
}
