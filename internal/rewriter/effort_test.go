package rewriter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestClampEffort(t *testing.T) {
	cases := []struct{ name, body, ceiling, want string }{
		{"chat above", `{"reasoning_effort":"max","model":"m"}`, "xhigh", `{"model":"m","reasoning_effort":"xhigh"}`},
		{"responses above", `{"reasoning":{"effort":"max"}}`, "high", `{"reasoning":{"effort":"high"}}`},
		{"anthropic above", `{"output_config":{"effort":"max"},"model":"m"}`, "xhigh", `{"model":"m","output_config":{"effort":"xhigh"}}`},
		{"gemini above", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"max"}}}`, "high", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`},
		{"chat at ceiling", `{"reasoning_effort":"max"}`, "max", `{"reasoning_effort":"max"}`},
		{"below ceiling", `{"reasoning_effort":"low"}`, "high", `{"reasoning_effort":"low"}`},
		{"both fields", `{"reasoning_effort":"max","output_config":{"effort":"max"}}`, "xhigh", `{"output_config":{"effort":"xhigh"},"reasoning_effort":"xhigh"}`},
		{"all fields", `{"reasoning_effort":"max","reasoning":{"effort":"max"},"output_config":{"effort":"max"},"generationConfig":{"thinkingConfig":{"thinkingLevel":"max"}}}`, "high", `{"reasoning_effort":"high","reasoning":{"effort":"high"},"output_config":{"effort":"high"},"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`},
		{"invalid json", `not-json`, "high", `not-json`},
		{"sse framing", "data: {\"reasoning_effort\":\"max\"}\n\n", "high", "data: {\"reasoning_effort\":\"max\"}\n\n"},
		{"mention only", `{"messages":[{"role":"user","content":"what effort should I use?"}]}`, "high", `{"messages":[{"role":"user","content":"what effort should I use?"}]}`},
		{"thinking budget", `{"thinking":{"type":"enabled","budget_tokens":32000}}`, "high", `{"thinking":{"type":"enabled","budget_tokens":32000}}`},
		{"empty body", ``, "high", ``},
		{"empty ceiling", `{"reasoning_effort":"max"}`, "", `{"reasoning_effort":"max"}`},
		{"unknown ceiling", `{"reasoning_effort":"max"}`, "extreme", `{"reasoning_effort":"max"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := clampEffort([]byte(c.body), c.ceiling)
			if err != nil {
				t.Fatalf("clampEffort(%q, %q) error = %v", c.body, c.ceiling, err)
			}
			var g, w any
			_ = json.Unmarshal(got, &g)
			_ = json.Unmarshal([]byte(c.want), &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("clampEffort(%q, %q) = %s, want %s", c.body, c.ceiling, got, c.want)
			}
			if c.want == c.body && string(got) != c.body {
				t.Fatalf("clampEffort(%q, %q) re-marshaled an unchanged body: %s", c.body, c.ceiling, got)
			}
		})
	}
}
