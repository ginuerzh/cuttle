package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm-gw.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExpandsEnvAndValidates(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-test")

	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
entrypoints:
  - name: default
    match: ["*"]
    route: default
providers:
  - name: deepseek
    type: generic
    chat: https://api.deepseek.com/v1
    keys: ["${TEST_KEY}"]
    auth:
      header: Authorization
      prefix: "Bearer "
nodes:
  - name: deepseek
    provider: deepseek
    model: deepseek-chat
routes:
  - name: default
    nodes: [deepseek]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Keys[0]; got != "sk-test" {
		t.Fatalf("key = %q, want sk-test", got)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: "  default  "
    match: ["  *  ", "", "*"]
    route: "  r  "
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: "  m1  "
routes:
  - name: "  r  "
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.Listen != ":8787" {
		t.Fatalf("listen = %q, want :8787", c.Gateway.Listen)
	}
	if c.Providers[0].Auth.Header != "" || c.Providers[0].Auth.Prefix != "" {
		t.Fatalf("auth = %+v, want empty so the adapter supplies the default", c.Providers[0].Auth)
	}
	if c.Providers[0].TLS.Secure == nil || !*c.Providers[0].TLS.Secure {
		t.Fatal("tls.secure should default to true")
	}
	if c.Nodes[0].Model != "m1" || c.Nodes[0].Weight != 1 {
		t.Fatalf("node defaults = %+v, want model m1 and weight 1", c.Nodes[0])
	}
	if c.Routes[0].Name != "r" || c.Routes[0].Strategy != "round" {
		t.Fatalf("route defaults = %+v, want name r and strategy round", c.Routes[0])
	}
	if c.Entrypoints[0].Name != "default" || c.Entrypoints[0].Route != "r" {
		t.Fatalf("entrypoint defaults = %+v", c.Entrypoints[0])
	}
	if got := c.Entrypoints[0].Match; len(got) != 1 || got[0] != "*" {
		t.Fatalf("match = %#v, want deduplicated [*]", got)
	}
}

func TestLoadKeepsCustomAuthHeaderWithoutPrefix(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    auth:
      header: x-api-key
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].Auth.Header != "x-api-key" || c.Providers[0].Auth.Prefix != "" {
		t.Fatalf("auth = %+v, want x-api-key with empty prefix", c.Providers[0].Auth)
	}
}

func TestResolveEntrypointOrder(t *testing.T) {
	c := &Config{Entrypoints: []Entrypoint{
		{Name: "specific", Match: []string{"gpt-5*"}, Route: "a"},
		{Name: "fallback", Match: []string{"*"}, Route: "b"},
	}}

	ep, ok := c.ResolveEntrypoint("GPT-5.5")
	if !ok || ep.Name != "specific" || ep.Route != "a" {
		t.Fatalf("specific = %+v, %v", ep, ok)
	}
	ep, ok = c.ResolveEntrypoint("other")
	if !ok || ep.Name != "fallback" || ep.Route != "b" {
		t.Fatalf("fallback = %+v, %v", ep, ok)
	}
	if got := c.ResolveEntrypointName("gpt-5.5"); got != "specific" {
		t.Fatalf("ResolveEntrypointName = %q, want specific", got)
	}
	noCatchAll := &Config{Entrypoints: []Entrypoint{
		{Name: "specific", Match: []string{"gpt-5*"}, Route: "a"},
	}}
	if got := noCatchAll.ResolveEntrypointName("unmatched"); got != "" {
		t.Fatalf("ResolveEntrypointName(unmatched) = %q, want empty", got)
	}
}

func TestResolveEntrypointMultipleGlobsAndCase(t *testing.T) {
	c := &Config{Entrypoints: []Entrypoint{
		{Name: "claude", Match: []string{"claude-*", "claude*"}, Route: "r"},
	}}
	ep, ok := c.ResolveEntrypoint("CLAUDE-OPUS-4-5")
	if !ok || ep.Name != "claude" || ep.Route != "r" {
		t.Fatalf("ResolveEntrypoint = %+v, %v", ep, ok)
	}
}

func TestValidateEntrypoints(t *testing.T) {
	base := func() *Config {
		return &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{Name: "p", Type: "generic", Chat: "https://a.example/v1"}},
			Nodes:     []Node{{Name: "n", Provider: "p", Model: "m1"}},
			Routes:    []Route{{Name: "r", Nodes: []string{"n"}}},
		}
	}
	cases := []struct {
		name string
		ep   Entrypoint
	}{
		{"empty name", Entrypoint{Match: []string{"*"}, Route: "r"}},
		{"slash name", Entrypoint{Name: "a/b", Match: []string{"*"}, Route: "r"}},
		{"empty match", Entrypoint{Name: "e", Route: "r"}},
		{"empty match entries", Entrypoint{Name: "e", Match: []string{"", "  "}, Route: "r"}},
		{"unknown route", Entrypoint{Name: "e", Match: []string{"*"}, Route: "missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			c.Entrypoints = []Entrypoint{tc.ep}
			if err := Validate(c); err == nil {
				t.Fatalf("%+v should not validate", tc.ep)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		c := base()
		c.Entrypoints = []Entrypoint{
			{Name: "e", Match: []string{"a*"}, Route: "r"},
			{Name: "e", Match: []string{"b*"}, Route: "r"},
		}
		if err := Validate(c); err == nil {
			t.Fatal("duplicate entrypoint names should not validate")
		}
	})
}

func TestValidateNodeRequiresModel(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "p", Type: "generic", Chat: "https://a.example/v1"}},
		Nodes:     []Node{{Name: "n", Provider: "p"}},
		Routes:    []Route{{Name: "r", Nodes: []string{"n"}}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v, want missing model error", err)
	}
}

func TestValidateRejectsDuplicateRouteNode(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "p", Type: "generic", Chat: "https://a.example/v1"}},
		Nodes:     []Node{{Name: "n", Provider: "p", Model: "m1"}},
		Routes:    []Route{{Name: "r", Nodes: []string{"n", "n"}}},
	}
	if err := Validate(c); err == nil {
		t.Fatal("duplicate route node references should not validate")
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "a", Type: "generic", Chat: "https://a.example/v1"}},
		Nodes:     []Node{{Name: "n", Provider: "missing", Model: "m1"}},
		Routes:    []Route{{Name: "r", Nodes: []string{"n"}}},
	}
	if err := Validate(c); err == nil {
		t.Fatal("want error for unknown provider")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := Load(writeConfig(t, `
gateway:
  lstien: ":8787"
providers: []
routes: []
`))
	if err == nil || !strings.Contains(err.Error(), "lstien") {
		t.Fatalf("err = %v, want unknown field error", err)
	}
}

func TestLoadRejectsLegacyAliasConfig(t *testing.T) {
	_, err := Load(writeConfig(t, `
gateway:
  listen: ":8787"
aliases:
  - match: "*"
    model: fast
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("err = %v, want an aliases unknown-field error", err)
	}
}

func TestNodeModelExpandsEnv(t *testing.T) {
	t.Setenv("MODEL", "deepseek-v4-flash")
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: "${MODEL}"
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Nodes[0].Model; got != "deepseek-v4-flash" {
		t.Fatalf("model = %q, want expanded value", got)
	}
}

func TestLoadRejectsEmptyKey(t *testing.T) {
	_, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    keys: ["${THIS_ENV_DOES_NOT_EXIST}"]
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err == nil || !strings.Contains(err.Error(), "keys[0]") {
		t.Fatalf("err = %v, want empty key error", err)
	}
}

func TestLoadRejectsLegacyProviderFields(t *testing.T) {
	cases := []struct {
		name  string
		field string
		body  string
	}{
		{
			name:  "baseURL",
			field: "baseURL",
			body: `
providers:
  - name: a
    type: generic
    baseURL: https://a.example
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
		{
			name:  "protocol",
			field: "protocol",
			body: `
providers:
  - name: a
    type: generic
    protocol: openai
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
		{
			name:  "pathMap",
			field: "pathMap",
			body: `
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    pathMap:
      /v1/chat/completions: /chat/completions
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("err = %v, want unknown field %q", err, tt.field)
			}
		})
	}
}

func TestDurationAndWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llm-gw.yaml")
	c, err := Load(writeConfig(t, `
admin:
  listen: "127.0.0.1:8788"
  sessionTTL: 30m
stats:
  path: "stats.sqlite"
  retainRequests: 168h
entrypoints:
  - name: default
    match: ["*"]
    route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    models:
      m1:
        disabled: true
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(c.Admin.SessionTTL); got != 30*time.Minute {
		t.Fatalf("sessionTTL = %v, want 30m", got)
	}
	if got := time.Duration(c.Stats.RetainRequests); got != 168*time.Hour {
		t.Fatalf("retainRequests = %v, want 168h", got)
	}

	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Providers[0].Name != "a" || again.Routes[0].Name != "r" {
		t.Fatalf("round trip mismatch: %+v", again)
	}
	if again.Entrypoints[0].Name != "default" || again.Nodes[0].Model != "m1" {
		t.Fatalf("entrypoint/model lost in round trip: %+v", again)
	}
	if !again.Providers[0].ModelDisabled("m1") {
		t.Fatalf("model override lost in round trip: %+v", again.Providers[0].Models)
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "s3cret") {
		t.Fatal("verify failed for the correct password")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("verify succeeded for a wrong password")
	}
	if VerifyPassword("not-a-hash", "s3cret") {
		t.Fatal("verify succeeded for a malformed hash")
	}
}

func TestProviderAPIRequiresType(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "new", Chat: "https://chat.example/v1"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("err = %v, want missing type error", err)
	}
}

func TestProviderAPIHealthAPI(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := &Config{
			Gateway: Gateway{Listen: ":8787"},
			Providers: []Provider{{
				Name:      "p",
				Type:      "deepseek",
				Chat:      "https://api.deepseek.com/v1",
				Anthropic: "https://api.deepseek.com/anthropic",
				Health:    Health{API: "anthropic"},
			}},
		}
		if err := Validate(c); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})

	t.Run("unknown api", func(t *testing.T) {
		c := &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{Name: "p", Type: "deepseek", Chat: "https://api.deepseek.com/v1", Health: Health{API: "grpc"}}},
		}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), "health.api") {
			t.Fatalf("err = %v, want health.api error", err)
		}
	})

	t.Run("missing base", func(t *testing.T) {
		c := &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{Name: "p", Type: "deepseek", Chat: "https://api.deepseek.com/v1", Health: Health{API: "anthropic"}}},
		}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), "health.api") {
			t.Fatalf("err = %v, want health.api base error", err)
		}
	})
}

func TestProviderAPIRequiresBaseURL(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "p", Type: "generic"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "API base") {
		t.Fatalf("err = %v, want missing API base error", err)
	}
}

func TestProviderAPIRejectsBadBaseURLs(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
	}{
		{"chat scheme", Provider{Name: "p", Type: "generic", Chat: "ftp://chat.example"}},
		{"anthropic url", Provider{Name: "p", Type: "generic", Anthropic: "://bad"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Gateway: Gateway{Listen: ":8787"}, Providers: []Provider{tt.p}}
			if err := Validate(c); err == nil {
				t.Fatalf("provider %+v should not validate", tt.p)
			}
		})
	}
}

func TestProviderAPIExpandsEnv(t *testing.T) {
	t.Setenv("TEST_CHAT_BASE", "https://chat.example/v1")
	t.Setenv("TEST_GEMINI_BASE", "https://gemini.example/v1beta")
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: "${TEST_CHAT_BASE}"
    gemini: "${TEST_GEMINI_BASE}"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Chat; got != "https://chat.example/v1" {
		t.Fatalf("chat = %q, want expanded base URL", got)
	}
	if got := c.Providers[0].Gemini; got != "https://gemini.example/v1beta" {
		t.Fatalf("gemini = %q, want expanded base URL", got)
	}
}

func TestProviderAPINewStyleUsesAdapterAuthDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: "https://chat.example/v1"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Auth.Header; got != "" {
		t.Fatalf("auth.header = %q, want empty so the adapter supplies the default", got)
	}
}

func TestProviderAPIHealthAPINormalized(t *testing.T) {
	for _, raw := range []string{"CHAT", "Chat", " chat "} {
		t.Run(raw, func(t *testing.T) {
			c, err := Load(writeConfig(t, fmt.Sprintf(`
providers:
  - name: p
    type: deepseek
    chat: "https://chat.example/v1"
    health:
      api: %q
`, raw)))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := c.Providers[0].Health.API; got != "chat" {
				t.Fatalf("health.api = %q, want canonical %q", got, "chat")
			}
		})
	}
}

func TestProviderAPIHealthAPIRejectsInvalidValue(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: p
    type: deepseek
    chat: "https://chat.example/v1"
    health:
      api: " GRPC "
`))
	if err == nil || !strings.Contains(err.Error(), "health.api") {
		t.Fatalf("err = %v, want health.api error", err)
	}
}

func TestProviderAPIRejectsUnknownType(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "p", Type: "bogus", Chat: "https://chat.example/v1"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v, want unknown type error", err)
	}
}

func TestProviderModelOverrides(t *testing.T) {
	t.Setenv("MODEL_ID", "glm-5.3")
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      " ${MODEL_ID} ":
        disabled: true
      empty:
        disabled: false
nodes:
  - name: n
    provider: p
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers[0]
	if !p.ModelDisabled("glm-5.3") {
		t.Fatalf("models = %+v", p.Models)
	}
	if _, ok := p.Models["empty"]; ok {
		t.Fatalf("disabled:false should be dropped: %+v", p.Models)
	}
	if p.ModelDisabled("missing") {
		t.Fatal("unknown model must not be disabled")
	}
}

func TestProviderModelOverridesMergeExpandedDuplicates(t *testing.T) {
	t.Setenv("DUP_MODEL_ID", "shared-model")
	path := writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      "${DUP_MODEL_ID}":
        disabled: false
      "$DUP_MODEL_ID":
        disabled: true
`)
	// Map iteration made the winning entry nondeterministic before the merge;
	// repeat so a regression cannot pass by chance.
	for i := 0; i < 20; i++ {
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		p := c.Providers[0]
		if len(p.Models) != 1 {
			t.Fatalf("models = %+v, want one merged entry", p.Models)
		}
		if !p.ModelDisabled("shared-model") {
			t.Fatalf("models = %+v, want disabled:true to win the merge", p.Models)
		}
	}
}

func TestProviderModelOverrideUnknownField(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      m1:
        enabled: true
`))
	if err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("err = %v, want unknown field error", err)
	}
}

func TestProviderModelOverridesDropEmptyID(t *testing.T) {
	t.Setenv("MODEL_ID_NOT_SET", "")
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      " ${MODEL_ID_NOT_SET} ":
        disabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if models := c.Providers[0].Models; len(models) != 0 {
		t.Fatalf("models = %+v, want empty after dropping empty IDs", models)
	}
}

func TestTopologyIncludes(t *testing.T) {
	c := &Config{
		Routes: []Route{{Name: "fast", Nodes: []string{"n1", "n2"}}},
		Nodes: []Node{
			{Name: "n1", Provider: "p", Model: "m1"},
			{Name: "n2", Provider: "q", Model: "m2"},
		},
	}

	cases := []struct {
		route, node, provider string
		want                  bool
	}{
		{"fast", "n1", "p", true},
		{"fast", "n2", "q", true},
		{"", "orphan", "p", true},
		{"fast", "", "", true},
		{"missing", "n1", "p", false},
		{"fast", "orphan", "p", false},
		{"fast", "n1", "wrong", false},
	}
	for _, tc := range cases {
		if got := c.TopologyIncludes(tc.route, tc.node, tc.provider); got != tc.want {
			t.Fatalf("TopologyIncludes(%q,%q,%q) = %v, want %v",
				tc.route, tc.node, tc.provider, got, tc.want)
		}
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test")
	c, err := Load("../../example.yaml")
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if len(c.Entrypoints) == 0 {
		t.Fatal("example has no entrypoints")
	}
	routes := make(map[string]bool, len(c.Routes))
	for _, r := range c.Routes {
		routes[r.Name] = true
	}
	for _, e := range c.Entrypoints {
		if !routes[e.Route] {
			t.Fatalf("entrypoint %q references unknown route %q", e.Name, e.Route)
		}
	}
}

func TestLogDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != "info" || c.Log.Format != "json" {
		t.Fatalf("log defaults = %+v", c.Log)
	}

	for _, body := range []string{
		"log:\n  level: loud\n",
		"log:\n  format: yaml\n",
	} {
		if _, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"+body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestLogExpandsEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
log:
  level: ${LOG_LEVEL}
  format: ${LOG_FORMAT}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != "debug" || c.Log.Format != "text" {
		t.Fatalf("expanded log = %+v", c.Log)
	}
}

func TestCaptureDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stats.Capture.Mode != "off" || c.Stats.Capture.MaxBytes != 32768 ||
		c.Stats.Capture.Retain != Duration(24*time.Hour) {
		t.Fatalf("capture defaults = %+v", c.Stats.Capture)
	}
	for _, body := range []string{
		"stats:\n  capture:\n    mode: always\n",
		"stats:\n  capture:\n    mode: sample\n    sampleRate: 2\n",
		"stats:\n  capture:\n    mode: errors\n    maxBytes: 1\n",
	} {
		if _, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"+body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestCaptureExplicitZeroSampleRate(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
stats:
  capture:
    mode: sample
    sampleRate: 0
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stats.Capture.SampleRate == nil || *c.Stats.Capture.SampleRate != 0 {
		t.Fatalf("sampleRate = %v, want explicit 0", c.Stats.Capture.SampleRate)
	}
}

func TestWriteNewDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llm-gw.yaml")

	first := &Config{Gateway: Gateway{Listen: ":1111"}}
	ApplyDefaults(first)
	if err := Write(path, first); err != nil {
		t.Fatal(err)
	}

	second := &Config{Gateway: Gateway{Listen: ":2222"}}
	ApplyDefaults(second)
	if err := WriteNew(path, second); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("WriteNew err = %v, want ErrConfigExists", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway.Listen != ":1111" {
		t.Fatalf("listen = %q, want :1111", got.Gateway.Listen)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, second); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway.Listen != ":2222" {
		t.Fatalf("listen = %q, want :2222", got.Gateway.Listen)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
