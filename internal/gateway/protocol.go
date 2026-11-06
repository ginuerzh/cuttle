package gateway

import (
	"fmt"
	"strings"

	corerouting "github.com/go-gost/core/routing"
	xrouting "github.com/go-gost/x/routing"

	"github.com/go-gost/llm-gw/internal/provider"
)

// inboundGroup binds one client-facing wire protocol to the provider API that
// should serve it when the provider supports that API.
type inboundGroup struct {
	api         provider.API
	pathPattern string
}

var inboundGroups = []inboundGroup{
	{provider.Chat, `^/v1/chat/completions$`},
	{provider.Responses, `^/v1/responses$`},
	{provider.Anthropic, `^(/v1/messages|/v1/messages/count_tokens)$`},
	{provider.Gemini, `^/v1beta/models/[^/]+:(generateContent|streamGenerateContent)$`},
}

// modelGlobPattern compiles one entrypoint's model globs into a single anchored
// regexp. catchAll reports an explicit "*" entry, which matches requests
// without a model body too.
func modelGlobPattern(match []string) (pattern string, catchAll bool, err error) {
	globs := make([]string, 0, len(match))
	for _, g := range match {
		if g = strings.TrimSpace(g); g != "" {
			globs = append(globs, g)
		}
	}
	if len(globs) == 0 {
		return "", false, fmt.Errorf("match is required")
	}

	inner := make([]string, 0, len(globs))
	for _, g := range globs {
		if g == "*" {
			return "", true, nil
		}
		rgx := globToRegexp(g)
		rgx = strings.TrimPrefix(rgx, "^")
		rgx = strings.TrimSuffix(rgx, "$")
		inner = append(inner, rgx)
	}
	return "(?i)^(" + strings.Join(inner, "|") + ")$", false, nil
}

// buildProtocolMatcher combines the entrypoint's model globs with one inbound
// path group. A catch-all entrypoint matches on the path alone because its body
// may have no model field.
func buildProtocolMatcher(match []string, pathPattern string) (corerouting.Matcher, error) {
	pattern, catchAll, err := modelGlobPattern(match)
	if err != nil {
		return nil, err
	}
	if catchAll {
		return xrouting.NewMatcher(fmt.Sprintf("PathRegexp(%q)", pathPattern))
	}
	return xrouting.NewMatcher(fmt.Sprintf("PathRegexp(%q) && BodyJSON(%q, %q)", pathPattern, "model", pattern))
}
