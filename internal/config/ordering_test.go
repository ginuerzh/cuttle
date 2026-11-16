package config

import (
	"slices"
	"testing"
)

func TestOrderEntrypointsSpecificityThenDeclaration(t *testing.T) {
	eps := []Entrypoint{
		{ID: "catchall-early", Match: []string{"*"}},
		{ID: "prefix-later", Match: []string{"claude-*"}},
		{ID: "exact-later", Match: []string{"claude-fable-5"}},
		{ID: "multi-glob", Match: []string{"dental-*", "claude-fable-5-*"}},
		{ID: "second-catchall", Match: []string{"*"}},
	}
	got := []string{}
	for _, e := range OrderEntrypoints(eps) {
		got = append(got, e.ID)
	}
	want := []string{"multi-glob", "exact-later", "prefix-later", "catchall-early", "second-catchall"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// Input unchanged.
	if eps[0].ID != "catchall-early" {
		t.Fatalf("input mutated to %q at 0", eps[0].ID)
	}
}

func TestOrderEntrypointsStarCountTiebreak(t *testing.T) {
	eps := []Entrypoint{
		{ID: "starred", Match: []string{"a*"}},
		{ID: "plain", Match: []string{"a"}},
	}
	got := OrderEntrypoints(eps)
	if got[0].ID != "plain" || got[1].ID != "starred" {
		t.Fatalf("order = %v %v, want [plain starred]", got[0].ID, got[1].ID)
	}
}

func TestOrderEntrypointsAllWildcardTiebreak(t *testing.T) {
	eps := []Entrypoint{
		{ID: "bagged", Match: []string{"**", "*"}},
		{ID: "plain", Match: []string{"*"}},
	}
	got := OrderEntrypoints(eps)
	if got[0].ID != "plain" || got[1].ID != "bagged" {
		t.Fatalf("order = %v %v, want [plain bagged] (one star beats three)", got[0].ID, got[1].ID)
	}
}

func TestOrderEntrypointsEqualEverythingKeepsDeclarationOrder(t *testing.T) {
	eps := []Entrypoint{
		{ID: "first", Match: []string{"*"}},
		{ID: "second", Match: []string{"*"}},
	}
	got := OrderEntrypoints(eps)
	if got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("order = %v %v, want declaration order preserved", got[0].ID, got[1].ID)
	}
}

func TestOrderEntrypointsDraftRuleSortsWithEveryoneElse(t *testing.T) {
	eps := []Entrypoint{
		{ID: "catchall-draft-first", Match: []string{"*"}},
		{ID: "draft-specific", Match: []string{"claude-fable*"}},
	}
	got := OrderEntrypoints(eps)
	if got[0].ID != "draft-specific" {
		t.Fatalf("got[0] = %q, want draft-specific (it still sorts above catchall)", got[0].ID)
	}
}

func TestSpecificityScore(t *testing.T) {
	cases := []struct {
		match []string
		want  int
	}{
		{nil, 0},
		{[]string{}, 0},
		{[]string{""}, 0},
		{[]string{"**", "*"}, 0},
		{[]string{"dental-*", "claude-fable-5-*"}, 15},
		{[]string{"claude-fable-5"}, 14},
	}
	for _, c := range cases {
		if got := SpecificityScore(c.match); got != c.want {
			t.Errorf("SpecificityScore(%q) = %d, want %d", c.match, got, c.want)
		}
	}
}
