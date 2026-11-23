// Package health exposes runtime node health derived from x node markers and
// the entrypoint binding eligibility owned by the command probe runner.
package health

import "time"

// Node is one node's current marker state within a route. ID and ProviderID
// identify the configured entities; Name is the display label the console
// shows.
type Node struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	ProviderID  string     `json:"providerId,omitempty"`
	FailCount   int64      `json:"failCount"`
	LastFailure *time.Time `json:"lastFailure,omitempty"`
	Dead        bool       `json:"dead"`
	DeadUntil   *time.Time `json:"deadUntil,omitempty"`
}

// Route is the health of one configured route. ID identifies the route; Name is
// its display label.
type Route struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MaxFails    int    `json:"maxFails"`
	FailTimeout string `json:"failTimeout"`
	Nodes       []Node `json:"nodes"`
}

// Entrypoint is the binding health of one entrypoint. ID identifies the
// entrypoint and Name is its display label. Active is the binding that will
// serve the next request without a subagent marker; Bindings is ordered by the
// data plane's selection priority: class-conditioned bindings first, then
// unconditional ones, and inside each tier probed bindings before probe-less
// ones, each in configuration order.
type Entrypoint struct {
	ID       string        `json:"entrypointId"`
	Name     string        `json:"entrypointName"`
	Active   ActiveBinding `json:"active"`
	Bindings []Binding     `json:"bindings"`
}

// ActiveBinding names the binding that will serve the next request without a
// subagent marker. Only unconditional bindings are candidates. Fallback is true
// when at least one unconditional binding carries a probe and none of them is
// eligible: the first probe-less unconditional binding then serves, or the first
// probed one when there is no probe-less binding. A conditioned binding never
// contributes to Fallback, because lead traffic never reaches it.
type ActiveBinding struct {
	RouteID  string `json:"routeId"`
	Fallback bool   `json:"fallback"`
}

// Binding is one entrypoint route binding. Index is its position in the
// configured list and RouteID/RouteName name the bound route. Eligible is the
// binding's marker state, read with the same rule the inner binding group uses,
// so it always agrees with Active; a binding that has no probe or has not probed
// yet is eligible. Last is the last probe result, absent until the first run.
type Binding struct {
	Index     int    `json:"index"`
	RouteID   string `json:"routeId"`
	RouteName string `json:"routeName"`
	// When is the binding's traffic class, empty for an unconditional binding.
	// A conditioned binding is never a candidate for Active, which stays
	// lead-scoped.
	When     string `json:"when,omitempty"`
	Eligible bool   `json:"eligible"`
	Last     *Probe `json:"last,omitempty"`
}

// Probe is one command probe result. ExitCode is -1 when the command could not
// be started or timed out. Error carries the first 1 KiB of stderr (the head),
// capped by the runner.
type Probe struct {
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	ExitCode  int       `json:"exitCode"`
	LatencyMS int64     `json:"latencyMs"`
	Error     string    `json:"error,omitempty"`
}
