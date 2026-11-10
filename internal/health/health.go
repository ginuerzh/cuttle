// Package health exposes runtime node health derived from x node markers.
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
