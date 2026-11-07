package stats

import "context"

// gatewayKeyContextKey is the context key for the authenticated gateway key
// name. It is unexported so other packages use the helpers below.
type gatewayKeyContextKey struct{}

// WithGatewayKey returns a context carrying the name of the gateway key that
// authenticated the request.
func WithGatewayKey(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, gatewayKeyContextKey{}, name)
}

// GatewayKeyFromContext returns the gateway key name carried by ctx, or the
// empty string when the request was not authenticated with a key.
func GatewayKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(gatewayKeyContextKey{}).(string)
	return name
}
