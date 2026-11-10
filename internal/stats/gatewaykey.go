package stats

import "context"

// gatewayKeyIDContextKey is the context key for the authenticated gateway key
// identifier. It is unexported so other packages use the helpers below.
type gatewayKeyIDContextKey struct{}

// WithGatewayKeyID returns a context carrying the id of the gateway key that
// authenticated the request. The id is what requests.gateway_key and
// key_rollups.gateway_key record; the display name is looked up separately.
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
