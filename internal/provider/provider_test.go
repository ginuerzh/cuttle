package provider

import (
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
)

func mustProvider(t *testing.T, cfg config.Provider) Provider {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return p
}

func TestFactory(t *testing.T) {
	for _, typ := range []string{"deepseek", "opencode-go", "opencode-zen", "commandcode", "openrouter", "generic"} {
		t.Run(typ, func(t *testing.T) {
			p := mustProvider(t, config.Provider{
				Name: typ,
				Type: typ,
				Chat: "https://chat.example/v1",
			})
			if got, ok := p.BaseURL(Chat); !ok || got != "https://chat.example/v1" {
				t.Fatalf("BaseURL(chat) = %q, %v", got, ok)
			}
			if _, ok := p.BaseURL(Responses); ok {
				t.Fatal("BaseURL(responses) should report an unconfigured API")
			}
			if _, ok := p.BaseURL("bogus"); ok {
				t.Fatal("BaseURL(bogus) should report an unknown API")
			}
		})
	}
}

func TestFactoryRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Provider
	}{
		{"unknown type", config.Provider{Name: "p", Type: "bogus", Chat: "https://chat.example/v1"}},
		{"missing type", config.Provider{Name: "p", Chat: "https://chat.example/v1"}},
		{"no API base", config.Provider{Name: "p", Type: "generic"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatalf("New(%+v) should fail", tt.cfg)
			}
		})
	}
}

func TestFactoryBaseURLsStayDistinct(t *testing.T) {
	goP := mustProvider(t, config.Provider{
		Name:      "opencode-go",
		Type:      "opencode-go",
		Chat:      "https://opencode.ai/inference/go/openai/v1",
		Anthropic: "https://opencode.ai/inference/go/anthropic",
	})
	zenP := mustProvider(t, config.Provider{
		Name:      "opencode-zen",
		Type:      "opencode-zen",
		Chat:      "https://opencode.ai/zen/v1",
		Anthropic: "https://opencode.ai/zen",
	})

	for _, api := range []API{Chat, Anthropic} {
		goURL, ok := goP.BaseURL(api)
		if !ok {
			t.Fatalf("opencode-go %s base URL missing", api)
		}
		zenURL, ok := zenP.BaseURL(api)
		if !ok {
			t.Fatalf("opencode-zen %s base URL missing", api)
		}
		if goURL == zenURL {
			t.Fatalf("%s base URL is shared: %q", api, goURL)
		}
	}
}

func TestResolveUnknownWithoutIO(t *testing.T) {
	// Resolve is static-only: the unreachable base URL must not be contacted.
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: "http://127.0.0.1:1/v1",
	})
	m, ok := p.Resolve("unknown-model")
	if ok {
		t.Fatal("generic Resolve should not find an unknown model")
	}
	if m.ID != "unknown-model" || len(m.APIs) != 0 {
		t.Fatalf("Resolve = %+v, want ID only", m)
	}
}
