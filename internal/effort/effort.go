// Package effort normalizes and compares reasoning-effort levels.
package effort

import "strings"

// levelOrder is the fixed reasoning-effort ladder, lowest first.
var levelOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

var levelRanks = func() map[string]int {
	ranks := make(map[string]int, len(levelOrder))
	for rank, level := range levelOrder {
		ranks[level] = rank
	}
	return ranks
}()

// DefaultCeiling is used when a target declares no effort levels.
const DefaultCeiling = "high"

// Rank returns the rank of level and whether it is recognized.
func Rank(level string) (int, bool) {
	rank, ok := levelRanks[strings.ToLower(strings.TrimSpace(level))]
	return rank, ok
}

// Normalize returns recognized levels lowercased, deduplicated, and sorted
// from lowest to highest.
func Normalize(values []string) []string {
	seen := make([]bool, len(levelOrder))
	for _, value := range values {
		if rank, ok := Rank(value); ok {
			seen[rank] = true
		}
	}

	var normalized []string
	for rank, level := range levelOrder {
		if seen[rank] {
			normalized = append(normalized, level)
		}
	}
	return normalized
}

// Ceiling returns the highest recognized level, or DefaultCeiling when none
// are recognized.
func Ceiling(values []string) string {
	normalized := Normalize(values)
	if len(normalized) == 0 {
		return DefaultCeiling
	}
	return normalized[len(normalized)-1]
}

// Intersect returns recognized levels present in both lists, sorted from
// lowest to highest. A list that normalizes to empty adopts the other list
// unless it was explicitly empty (non-nil and zero-length). Disjoint known
// lists and explicit empty lists return a non-nil empty slice so later merges
// cannot repopulate a known empty intersection.
func Intersect(a, b []string) []string {
	if (a != nil && len(a) == 0) || (b != nil && len(b) == 0) {
		return []string{}
	}

	a = Normalize(a)
	b = Normalize(b)
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}

	allowed := make(map[string]struct{}, len(b))
	for _, level := range b {
		allowed[level] = struct{}{}
	}

	var common []string
	for _, level := range a {
		if _, ok := allowed[level]; ok {
			common = append(common, level)
		}
	}
	if len(common) == 0 {
		return []string{}
	}
	return common
}
