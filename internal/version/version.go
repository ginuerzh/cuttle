// Package version records the build identity injected at link time.
package version

import "strings"

// Version, Commit and BuildDate are populated by -ldflags -X. The defaults
// keep a plain go build usable.
var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

// Info is the JSON-friendly build identity.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"builtAt,omitempty"`
}

// Current returns the normalized build identity.
func Current() Info {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "dev"
	}
	return Info{
		Version: v,
		Commit:  strings.TrimSpace(Commit),
		BuiltAt: strings.TrimSpace(BuildDate),
	}
}

// ShortCommit returns the first seven characters of the commit.
func ShortCommit() string {
	c := strings.TrimSpace(Commit)
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// String formats the identity for CLI and log output.
func String() string {
	info := Current()
	parts := make([]string, 0, 2)
	if c := ShortCommit(); c != "" {
		parts = append(parts, c)
	}
	if info.BuiltAt != "" {
		parts = append(parts, info.BuiltAt)
	}
	if len(parts) == 0 {
		return info.Version
	}
	return info.Version + " (" + strings.Join(parts, ", ") + ")"
}
