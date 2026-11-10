package stats

import "context"

// gatewayKeyIDContextKey is the context key for the authenticated gateway key
// identifier. It is unexported so other packages use the helpers below.
type gatewayKeyIDContextKey struct{}

// WithGatewayKeyID returns a context carrying the identifier of the gateway key
// that authenticated the request: the key id, or the display name while a
// config written without ids is still possible. Task 7 makes it id-only.
func WithGatewayKeyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, gatewayKeyIDContextKey{}, id)
}

// GatewayKeyIDFromContext returns the gateway key identifier carried by ctx, or
// the empty string when the request was not authenticated with a key.
func GatewayKeyIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(gatewayKeyIDContextKey{}).(string)
	return id
}
