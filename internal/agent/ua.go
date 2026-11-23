package agent

import "strings"

// clientPrefix ties a user-agent prefix to the client label the console shows.
// It exists so the marker-health check can ask "did this client send any
// marker at all" without a new stats column: the UA already identifies the
// client, and the marker column identifies the class.
type clientPrefix struct {
	client string
	prefix string
}

// clientPrefixes is ordered; the first match wins, so a fork whose UA starts
// with a known prefix is labelled as that client.
var clientPrefixes = []clientPrefix{
	{"claude-code", "claude-cli/"},
	{"opencode", "opencode/"},
	{"codex", "codex_cli_rs/"},
}

// ClientOf returns the client label for a request's user agent, or "" when the
// agent is not one cuttle knows. The match is case-insensitive on the trimmed
// value.
func ClientOf(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if ua == "" {
		return ""
	}
	for _, p := range clientPrefixes {
		if strings.HasPrefix(ua, p.prefix) {
			return p.client
		}
	}
	return ""
}

// KnownClients returns every client label this package can produce, in table
// order. The health endpoint uses it to report a client that sent no traffic at
// all, which no stats row can reveal.
func KnownClients() []string {
	out := make([]string, 0, len(clientPrefixes))
	for _, p := range clientPrefixes {
		out = append(out, p.client)
	}
	return out
}

// SourceOfClient maps a client label back to the marker source it produces, or
// "" when the client has no known marker. Both tables are keyed by the same
// constants, so this keeps the health endpoint from inventing a mapping.
func SourceOfClient(client string) string {
	for _, p := range clientPrefixes {
		if p.client != client {
			continue
		}
		for _, m := range markers {
			if m.header != "" && sourceClient(m.source) == client {
				return m.source
			}
		}
	}
	return ""
}

// sourceClient maps a marker source constant to its client label.
func sourceClient(source string) string {
	switch source {
	case SourceClaudeCode:
		return "claude-code"
	case SourceOpenCode:
		return "opencode"
	case SourceCodex:
		return "codex"
	}
	return ""
}
