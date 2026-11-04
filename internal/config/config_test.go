package config

import (
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
routes:
  - name: default
    nodes: [deepseek]
    match:
      model: "*"
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
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
routes:
  - name: r
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
}

func TestLoadKeepsCustomAuthHeaderWithoutPrefix(t *testing.T) {
	c, err := Load(writeConfig(t, `
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    auth:
      header: x-api-key
nodes:
  - name: n
    provider: a
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

func TestValidateRejectsUnknownProvider(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "a", Type: "generic", Chat: "https://a.example/v1"}},
		Nodes:     []Node{{Name: "n", Provider: "missing"}},
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

func TestLoadRejectsEmptyKey(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    keys: ["${THIS_ENV_DOES_NOT_EXIST}"]
nodes:
  - name: n
    provider: a
routes:
  - name: r
    nodes: [n]
`))
	if err == nil || !strings.Contains(err.Error(), "keys[0]") {
		t.Fatalf("err = %v, want empty key error", err)
	}
}

func TestResolveAliasAndEffectiveModel(t *testing.T) {
	c := &Config{
		Aliases: []Alias{
			{Match: "claude-opus*", Model: "strong"},
			{Match: "gpt-5.5*", Model: "strong"},
			{Match: "*", Model: "fast"},
		},
		Providers: []Provider{{Name: "p"}},
		Nodes: []Node{{
			Name:     "n",
			Provider: "p",
			Models: map[string]string{
				"strong": "deepseek-v4-pro",
				"fast":   "deepseek-v4-flash",
				"*":      "deepseek-v4-flash",
			},
		}},
	}

	cases := map[string]string{
		"claude-opus-4-5": "strong",
		"gpt-5.5":         "strong",
		"claude-sonnet-4": "fast",
		"unknown-model":   "fast",
	}
	for model, want := range cases {
		if got := c.ResolveAlias(model); got != want {
			t.Fatalf("ResolveAlias(%q) = %q, want %q", model, got, want)
		}
	}

	n := c.Nodes[0]
	if got := n.EffectiveModel("strong"); got != "deepseek-v4-pro" {
		t.Fatalf("strong = %q", got)
	}
	if got := n.EffectiveModel("fast"); got != "deepseek-v4-flash" {
		t.Fatalf("fast = %q", got)
	}
	if got := n.EffectiveModel("missing"); got != "deepseek-v4-flash" {
		t.Fatalf("missing = %q, want * fallback", got)
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
