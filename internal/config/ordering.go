package config

import "sort"

// SpecificityScore returns the number of literal (non-'*') bytes in the
// most specific glob in match. A request can satisfy at most one glob per
// entrypoint, so scoring sums the best case rather than all globs; adding a
// redundant glob never promotes an entrypoint above an exact match.
//
// An empty match has score 0. Match globs are expected to be ASCII model
// names; non-ASCII patterns are still counted per byte, which is monotone
// and order-consistent with the rest of the config.
func SpecificityScore(match []string) int {
	best := 0
	for _, g := range match {
		n := 0
		for i := 0; i < len(g); i++ {
			if g[i] != '*' {
				n++
			}
		}
		if n > best {
			best = n
		}
	}
	return best
}

// StarCount returns the total number of '*' bytes across match. It is the
// tiebreak between two rules with the same literal count: fewer wildcards
// means more literal context and wins.
func StarCount(match []string) int {
	stars := 0
	for _, g := range match {
		for i := 0; i < len(g); i++ {
			if g[i] == '*' {
				stars++
			}
		}
	}
	return stars
}

// entrypointOrder returns the indices into eps sorted by specificity,
// highest first: score descending, then fewer total '*' characters, then
// original position. The total order makes the sort deterministic without a
// stability promise.
func entrypointOrder(eps []Entrypoint) []int {
	idx := make([]int, len(eps))
	score := make([]int, len(eps))
	star := make([]int, len(eps))
	for i, e := range eps {
		idx[i] = i
		score[i] = SpecificityScore(e.Match)
		star[i] = StarCount(e.Match)
	}
	sort.Slice(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if score[i] != score[j] {
			return score[i] > score[j]
		}
		if star[i] != star[j] {
			return star[i] < star[j]
		}
		return i < j
	})
	return idx
}

// OrderEntrypoints returns a copy of eps sorted by specificity, highest
// first. The input is left untouched.
func OrderEntrypoints(eps []Entrypoint) []Entrypoint {
	sorted := make([]Entrypoint, len(eps))
	for pos, i := range entrypointOrder(eps) {
		sorted[pos] = eps[i]
	}
	return sorted
}
