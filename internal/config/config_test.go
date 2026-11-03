package config

import (
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
    baseURL: https://api.deepseek.com
    keys: ["${TEST_KEY}"]
    auth:
      header: Authorization
      prefix: "Bearer "
    pathMap:
      /v1/chat/completions: /chat/completions
routes:
  - name: default
    providers: [deepseek]
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
    baseURL: https://a.example
routes:
  - name: r
    providers: [a]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.Listen != ":8787" {
		t.Fatalf("listen = %q, want :8787", c.Gateway.Listen)
	}
	if c.Providers[0].Auth.Header != "Authorization" || c.Providers[0].Auth.Prefix != "Bearer " {
		t.Fatalf("auth defaults = %+v", c.Providers[0].Auth)
	}
	if c.Providers[0].TLS.Secure == nil || !*c.Providers[0].TLS.Secure {
		t.Fatal("tls.secure should default to true")
	}
}

func TestLoadKeepsCustomAuthHeaderWithoutPrefix(t *testing.T) {
	c, err := Load(writeConfig(t, `
providers:
  - name: a
    baseURL: https://a.example
    auth:
      header: x-api-key
routes:
  - name: r
    providers: [a]
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
		Providers: []Provider{{Name: "a", BaseURL: "https://a.example"}},
		Routes:    []Route{{Name: "r", Providers: []string{"missing"}}},
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
    baseURL: https://a.example
    keys: ["${THIS_ENV_DOES_NOT_EXIST}"]
routes:
  - name: r
    providers: [a]
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
		Providers: []Provider{{
			Name: "p",
			Models: map[string]string{
				"strong": "deepseek-v4-pro",
				"fast":   "deepseek-v4-flash",
				"*":      "deepseek-v4-flash",
			},
			EffortModels: map[string]map[string]string{
				"fast": {"high": "deepseek-v4-pro"},
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

	p := c.Providers[0]
	if got := p.EffectiveModel("strong", "high"); got != "deepseek-v4-pro" {
		t.Fatalf("strong/high = %q", got)
	}
	if got := p.EffectiveModel("fast", "high"); got != "deepseek-v4-pro" {
		t.Fatalf("fast/high = %q, want effort override", got)
	}
	if got := p.EffectiveModel("fast", "low"); got != "deepseek-v4-flash" {
		t.Fatalf("fast/low = %q", got)
	}
	if got := p.EffectiveModel("missing", "high"); got != "deepseek-v4-flash" {
		t.Fatalf("missing/high = %q, want * fallback", got)
	}
}

func TestValidateRejectsBadProtocol(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{Name: "a", BaseURL: "https://a.example", Protocol: "grpc"}},
	}
	if err := Validate(c); err == nil {
		t.Fatal("want error for unsupported protocol")
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
    baseURL: https://a.example
routes:
  - name: r
    providers: [a]
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
