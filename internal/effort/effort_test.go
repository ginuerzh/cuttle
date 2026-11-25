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
	cases := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{name: "normalized overlap", a: []string{"max", "LOW", "high", "max"}, b: []string{"high", "low", "extreme"}, want: []string{"low", "high"}},
		{name: "nil left", a: nil, b: []string{"high", "LOW"}, want: []string{"low", "high"}},
		{name: "nil right", a: []string{"low"}, b: nil, want: []string{"low"}},
		{name: "unknown left", a: []string{"extreme"}, b: []string{"low"}, want: []string{"low"}},
		{name: "unknown right", a: []string{"low"}, b: []string{"extreme"}, want: []string{"low"}},
		{name: "disjoint", a: []string{"low"}, b: []string{"high"}, want: []string{}},
		{name: "known empty left", a: []string{}, b: []string{"low"}, want: []string{}},
		{name: "known empty right", a: []string{"low"}, b: []string{}, want: []string{}},
		{name: "both absent", a: nil, b: nil, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Intersect(c.a, c.b); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Intersect(%#v, %#v) = %#v, want %#v", c.a, c.b, got, c.want)
			}
		})
	}
}
