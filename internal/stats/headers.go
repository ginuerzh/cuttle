package stats

import (
	"net/http"
	"strings"
)

// headerAllowlist is deliberately small: these headers explain upstream
// throttling and correlation without carrying prompts or credentials.
var headerExact = map[string]bool{
	"content-type":        true,
	"user-agent":          true,
	"retry-after":         true,
	"x-request-id":        true,
	"x-cuttle-request-id": true,
	"x-opencode-session":  true,
	"cf-ray":              true,
	"anthropic-version":   true,
}

var headerPrefixes = []string{
	"anthropic-ratelimit-",
	"x-ratelimit-",
}

// captureHeaders returns the allowlisted subset of h, lower-cased and joined.
// Authorization, cookie and API-key headers are never returned.
func captureHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string)
	for key, values := range h {
		name := strings.ToLower(strings.TrimSpace(key))
		if !allowedHeader(name) || len(values) == 0 {
			continue
		}
		out[name] = strings.Join(values, ", ")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func allowedHeader(name string) bool {
	if headerExact[name] {
		return true
	}
	for _, prefix := range headerPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
