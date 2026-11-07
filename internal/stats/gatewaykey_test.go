package stats

import (
	"context"
	"testing"
)

func TestGatewayKeyContext(t *testing.T) {
	ctx := WithGatewayKey(context.Background(), "claude-code")
	if got := GatewayKeyFromContext(ctx); got != "claude-code" {
		t.Fatalf("gateway key from context = %q, want %q", got, "claude-code")
	}
	if got := GatewayKeyFromContext(context.Background()); got != "" {
		t.Fatalf("gateway key from empty context = %q, want empty", got)
	}
}
