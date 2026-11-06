package main

import (
	"bytes"
	"testing"

	"github.com/go-gost/llm-gw/internal/version"
)

func TestPrintVersion(t *testing.T) {
	var buf bytes.Buffer
	printVersion(&buf)
	want := "llm-gw " + version.String() + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("printVersion() = %q, want %q", got, want)
	}
}
