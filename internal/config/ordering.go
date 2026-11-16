package config

import "sort"

// SpecificityScore returns the number of literal (non-'*') bytes in the
// most specific glob in match. A request can satisfy at most one glob per
// entrypoint, so scoring sums the best case rather than all globs; adding a
// redundant glob never promotes an entrypoint above an exact match.
//
// An empty match has score 0. The returned value is used only for relative
// comparison between rule sets.
func SpecificityScore(match []string) int {
	best := 0
	for _, g := range match {
		n := 0
		for _, r := range g {
			if r != '*' {
				n++
			}
		}
		if n > best {
			best = n
		}
	}
	return best
}

// StarCount returns the total number of '*' characters across match. It is
// the tiebreak between two rules with the same literal count: fewer
// wildcards means more literal context and wins.
func StarCount(match []string) int {
	stars := 0
	for _, g := range match {
		for _, r := range g {
			if r == '*' {
				stars++
			}
		}
	}
	return stars
}

// OrderEntrypoints returns a copy of eps sorted by specificity, highest
// first: score descending, then fewer '*' characters, then original
// position (stable). The input is left untouched.
//
// The function exists so every consumer that uses declaration order as
// precedence (the compiled hop group, the count-tokens policy, the admin
// stats filter) agrees on a single rule set established by URI routing.
func OrderEntrypoints(eps []Entrypoint) []Entrypoint {
	out := make([]struct {
		ep   Entrypoint
		idx  int
		sc   int
		star int
	}, len(eps))
	for i, e := range eps {
		out[i] = struct {
			ep   Entrypoint
			idx  int
			sc   int
			star int
		}{e, i, SpecificityScore(e.Match), StarCount(e.Match)}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].sc != out[j].sc {
			return out[i].sc > out[j].sc
		}
		if out[i].star != out[j].star {
			return out[i].star < out[j].star
		}
		return out[i].idx < out[j].idx
	})
	sorted := make([]Entrypoint, len(out))
	for i := range out {
		sorted[i] = out[i].ep
	}
	return sorted
}
