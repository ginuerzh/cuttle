package stats

import (
	"context"
	"testing"
)

func TestGatewayKeyIDContext(t *testing.T) {
	ctx := WithGatewayKeyID(context.Background(), "gk_0000000001")
	if got := GatewayKeyIDFromContext(ctx); got != "gk_0000000001" {
		t.Fatalf("gateway key id from context = %q, want %q", got, "gk_0000000001")
	}
	if got := GatewayKeyIDFromContext(context.Background()); got != "" {
		t.Fatalf("gateway key id from empty context = %q, want empty", got)
	}
}
