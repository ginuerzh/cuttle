package effort

import (
	"reflect"
	"testing"
)

func TestRank(t *testing.T) {
	for level, want := range map[string]int{"none": 0, " LOW ": 2, "high": 4, "xhigh": 5, "max": 6} {
		if got, ok := Rank(level); !ok || got != want {
			t.Fatalf("Rank(%q) = %d, %v; want %d, true", level, got, ok, want)
		}
	}
	if _, ok := Rank("extreme"); ok {
		t.Fatal("Rank(extreme) must be unknown")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{"max", "LOW", "max", "extreme", " ", "xhigh"})
	want := []string{"low", "xhigh", "max"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize = %v; want %v", got, want)
	}
}

func TestCeiling(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "high"},
		{[]string{"bogus"}, "high"},
		{[]string{"low", "max", "high"}, "max"},
		{[]string{"low", "xhigh"}, "xhigh"},
		{[]string{"low", "medium"}, "medium"},
	}
	for _, c := range cases {
		if got := Ceiling(c.in); got != c.want {
			t.Fatalf("Ceiling(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestIntersect(t *testing.T) {
	if got := Intersect([]string{"low", "high", "max"}, []string{"low", "high"}); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("intersection = %v", got)
	}
	if got := Intersect(nil, []string{"high", "low"}); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("one-sided = %v", got)
	}
	if got := Intersect([]string{"low"}, nil); !reflect.DeepEqual(got, []string{"low"}) {
		t.Fatalf("one-sided = %v", got)
	}
	if got := Intersect([]string{"low"}, []string{"high"}); got != nil {
		t.Fatalf("disjoint = %v; want nil", got)
	}
}
