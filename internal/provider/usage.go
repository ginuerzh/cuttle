package provider

import (
	"context"
	"time"
)

// UsageProvider is the optional extension point for upstream usage and
// billing queries. No v1 adapter implements it: the interface and its types
// are reserved so a future implementation does not change the core Provider
// interface.
type UsageProvider interface {
	Usage(ctx context.Context, q UsageQuery) (UsageReport, error)
}

// UsageQuery scopes a usage report. Zero time bounds mean "no bound".
type UsageQuery struct {
	Start time.Time
	End   time.Time
	Model string
}

// UsageReport is one provider usage aggregate for the requested window.
type UsageReport struct {
	Provider     string
	Start        time.Time
	End          time.Time
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	Cost         *Price
}
