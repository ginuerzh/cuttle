package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/go-gost/core/logger"
)

func TestLevelFilteringAndSetLevel(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("info", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}

	l.Debug("hidden")
	if buf.Len() != 0 {
		t.Fatalf("debug leaked at info: %q", buf.String())
	}
	l.Info("visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Fatalf("info missing: %q", buf.String())
	}

	if err := l.SetLevel("debug"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	l.Debug("now-visible")
	if !strings.Contains(buf.String(), "now-visible") {
		t.Fatalf("debug missing after SetLevel: %q", buf.String())
	}
	if got := l.GetLevel(); got != logger.DebugLevel {
		t.Fatalf("GetLevel = %q, want debug", got)
	}

	if err := l.SetLevel("info"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	l.Debug("hidden-again")
	if buf.Len() != 0 {
		t.Fatalf("debug leaked after lowering: %q", buf.String())
	}
}

func TestWithFieldsSharesLevel(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("info", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	child := l.WithFields(map[string]any{"route": "fast"})
	if err := l.SetLevel("debug"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	child.Debug("child-debug")
	if !strings.Contains(buf.String(), "child-debug") || !strings.Contains(buf.String(), `"route":"fast"`) {
		t.Fatalf("child did not share level/fields: %q", buf.String())
	}
}

func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("debug", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello")
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("json output: %v (%q)", err, buf.String())
	}
	if entry["level"] != "info" || entry["msg"] != "hello" {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestTextOutput(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("warn", "text", &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Warn("careful")
	if !strings.Contains(buf.String(), "level=warn") || !strings.Contains(buf.String(), "careful") {
		t.Fatalf("text output = %q", buf.String())
	}
}

func TestNewRejectsInvalidValues(t *testing.T) {
	if _, err := newLogger("loud", "json", &bytes.Buffer{}); err == nil {
		t.Fatal("expected invalid level error")
	}
	if _, err := newLogger("info", "yaml", &bytes.Buffer{}); err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestApplyChangesLevelAndWarnsOnFormat(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("info", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	Apply(l, "debug", "text")
	if l.GetLevel() != logger.DebugLevel {
		t.Fatalf("level = %q, want debug", l.GetLevel())
	}
	if !strings.Contains(buf.String(), "restart") {
		t.Fatalf("missing format warning: %q", buf.String())
	}
}

func TestCallerPointsToCallSite(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("debug", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}

	l.Info("direct")
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if caller, _ := entry["caller"].(string); !strings.Contains(caller, "logging_test.go") {
		t.Fatalf("direct caller = %q", caller)
	}

	buf.Reset()
	l.Infof("formatted %s", "message")
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if caller, _ := entry["caller"].(string); !strings.Contains(caller, "logging_test.go") {
		t.Fatalf("formatted caller = %q", caller)
	}
}

func TestApplyWarnsOnFormatChangeAtErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	l, err := newLogger("error", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	Apply(l, "error", "text")
	if !strings.Contains(buf.String(), "restart") {
		t.Fatalf("missing restart warning at error level: %q", buf.String())
	}
}

func TestSetLevelRejectsInvalid(t *testing.T) {
	l, err := newLogger("info", "json", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetLevel("loud"); err == nil {
		t.Fatal("expected invalid level error")
	}
	if got := l.GetLevel(); got != logger.InfoLevel {
		t.Fatalf("level changed on error: %q", got)
	}
}

func TestConcurrentLogAndSetLevel(t *testing.T) {
	l, err := newLogger("info", "json", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); l.Info("line") }()
		go func() { defer wg.Done(); _ = l.SetLevel("debug") }()
	}
	wg.Wait()
}
