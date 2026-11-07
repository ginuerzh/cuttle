package stats

import (
	"testing"
	"time"

	"github.com/ginuerzh/llm-api-converter/engine"
)

func TestFactsNormalizesAnthropicInputTokens(t *testing.T) {
	f := NewFacts()
	f.Add("sid", "p", "anthropic", engine.Result{
		InputTokens: 260, OutputTokens: 5, CachedTokens: 161280,
	})
	fact, ok := f.Take("sid")
	if !ok {
		t.Fatal("fact not found")
	}
	if fact.InputTokens != 161540 {
		t.Fatalf("input tokens = %d, want 161540 (fresh 260 + cached 161280)", fact.InputTokens)
	}
	if fact.CachedTokens != 161280 {
		t.Fatalf("cached tokens = %d, want 161280", fact.CachedTokens)
	}
}

func TestFactsKeepsOpenAIInputTokens(t *testing.T) {
	f := NewFacts()
	f.Add("sid", "p", "chat", engine.Result{
		InputTokens: 1000, CachedTokens: 800,
	})
	fact, ok := f.Take("sid")
	if !ok {
		t.Fatal("fact not found")
	}
	if fact.InputTokens != 1000 {
		t.Fatalf("input tokens = %d, want 1000; cached tokens are already included", fact.InputTokens)
	}
}
func TestMarkFirstResponseKeepsFirstTimestamp(t *testing.T) {
	f := NewFacts()
	first := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	f.MarkFirstResponse("sid", first)
	f.MarkFirstResponse("sid", first.Add(time.Second))

	fact, ok := f.Take("sid")
	if !ok || !fact.FirstResponseAt.Equal(first) {
		t.Fatalf("first response = %v, want %v", fact.FirstResponseAt, first)
	}
}
