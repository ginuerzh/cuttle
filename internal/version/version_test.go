package version

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		builtAt string
		want    string
	}{
		{"dev default", "", "", "", "dev"},
		{"main build", "main-694deb5", "", "", "main-694deb5"},
		{"release build", "v0.1.0", "694deb53b2c9abebf6e812505424d8b870a5ec53", "2026-11-06T10:00:00Z", "v0.1.0 (694deb5, 2026-11-06T10:00:00Z)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldV, oldC, oldB := Version, Commit, BuildDate
			t.Cleanup(func() { Version, Commit, BuildDate = oldV, oldC, oldB })
			Version, Commit, BuildDate = tc.version, tc.commit, tc.builtAt
			if got := String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentAndShortCommit(t *testing.T) {
	oldV, oldC, oldB := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldV, oldC, oldB })

	Version, Commit, BuildDate = "  main-694deb5  ", "  694deb53b2c9  ", "  2026-11-06T10:00:00Z  "
	if got := Current().Version; got != "main-694deb5" {
		t.Fatalf("Current().Version = %q", got)
	}
	if got := Current().BuiltAt; got != "2026-11-06T10:00:00Z" {
		t.Fatalf("Current().BuiltAt = %q", got)
	}
	if got := ShortCommit(); got != "694deb5" {
		t.Fatalf("ShortCommit() = %q", got)
	}

	Commit = "abc"
	if got := ShortCommit(); got != "abc" {
		t.Fatalf("ShortCommit() short = %q", got)
	}
	Commit = ""
	if got := ShortCommit(); got != "" {
		t.Fatalf("ShortCommit() empty = %q", got)
	}
}
