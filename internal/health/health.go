// Package health exposes runtime node health derived from x node markers.
package health

import "time"

// Node is one node's current marker state within a route.
type Node struct {
	Name        string     `json:"name"`
	Provider    string     `json:"provider,omitempty"`
	FailCount   int64      `json:"failCount"`
	LastFailure *time.Time `json:"lastFailure,omitempty"`
	Dead        bool       `json:"dead"`
	DeadUntil   *time.Time `json:"deadUntil,omitempty"`
}

// Route is the health of one configured route.
type Route struct {
	Name        string `json:"name"`
	MaxFails    int    `json:"maxFails"`
	FailTimeout string `json:"failTimeout"`
	Nodes       []Node `json:"nodes"`
}
