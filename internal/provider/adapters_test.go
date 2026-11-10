package provider

import (
	"slices"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		id     string
		want   []API
		wantOK bool
	}{
		{"deepseek", "deepseek", "deepseek-chat", []API{Chat, Responses, Anthropic}, true},
		{"deepseek unknown", "deepseek", "gpt-5.5", nil, false},
		{"opencode-go deepseek", "opencode-go", "deepseek-v4-flash", []API{Chat}, true},
		{"opencode-go glm", "opencode-go", "glm-5.3", []API{Anthropic}, true},
		{"opencode-go unknown", "opencode-go", "mystery-model", nil, false},
		{"opencode-zen deepseek", "opencode-zen", "deepseek-v4-flash", []API{Chat}, true},
		{"opencode-zen glm", "opencode-zen", "glm-5.3", []API{Anthropic}, true},
		{"commandcode claude", "commandcode", "claude-sonnet-5", []API{Anthropic}, true},
		{"commandcode other", "commandcode", "gpt-5.5", []API{Chat, Responses}, true},
		{"openrouter", "openrouter", "anthropic/claude-sonnet-4", []API{Chat, Responses, Anthropic}, true},
		{"generic", "generic", "anything", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustProvider(t, config.Provider{
				Name: tt.name,
				Type: tt.typ,
				Chat: "https://chat.example/v1",
			})
			m, ok := p.Resolve(tt.id)
			if ok != tt.wantOK {
				t.Fatalf("Resolve(%q) ok = %v, want %v", tt.id, ok, tt.wantOK)
			}
			if m.ID != tt.id {
				t.Fatalf("Resolve(%q).ID = %q", tt.id, m.ID)
			}
			if !slices.Equal(m.APIs, tt.want) {
				t.Fatalf("Resolve(%q).APIs = %v, want %v", tt.id, m.APIs, tt.want)
			}
		})
	}
}

func TestAuth(t *testing.T) {
	cases := []struct {
		name       string
		typ        string
		api        API
		header     string
		prefix     string
		keyHeaders []config.KeyHeader
	}{
		{"deepseek chat", "deepseek", Chat, "Authorization", "Bearer ", nil},
		{"deepseek responses", "deepseek", Responses, "Authorization", "Bearer ", nil},
		{"deepseek anthropic", "deepseek", Anthropic, "x-api-key", "", nil},
		{"deepseek gemini", "deepseek", Gemini, "x-goog-api-key", "", nil},
		{"opencode-go chat", "opencode-go", Chat, "Authorization", "Bearer ", []config.KeyHeader{{Header: "x-api-key"}}},
		{"opencode-go anthropic", "opencode-go", Anthropic, "x-api-key", "", nil},
		{"commandcode chat", "commandcode", Chat, "Authorization", "Bearer ", nil},
		{"commandcode anthropic", "commandcode", Anthropic, "Authorization", "Bearer ", nil},
		{"openrouter chat", "openrouter", Chat, "Authorization", "Bearer ", nil},
		{"openrouter responses", "openrouter", Responses, "Authorization", "Bearer ", nil},
		{"openrouter anthropic", "openrouter", Anthropic, "Authorization", "Bearer ", nil},
		{"generic anthropic", "generic", Anthropic, "x-api-key", "", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := mustProvider(t, config.Provider{
				Name: tt.name,
				Type: tt.typ,
				Chat: "https://chat.example/v1",
			})
			a := p.Auth(tt.api)
			if a.Header != tt.header || a.Prefix != tt.prefix {
				t.Fatalf("Auth(%s) = %+v, want header %q prefix %q", tt.api, a, tt.header, tt.prefix)
			}
			if !slices.Equal(a.KeyHeaders, tt.keyHeaders) {
				t.Fatalf("Auth(%s).KeyHeaders = %+v, want %+v", tt.api, a.KeyHeaders, tt.keyHeaders)
			}
		})
	}
}

func TestAuthMergesAdditiveConfig(t *testing.T) {
	t.Run("extra only keeps the adapter default", func(t *testing.T) {
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "deepseek",
			Chat: "https://chat.example/v1",
			Auth: config.Auth{Extra: map[string]string{"X-Org": "acme"}},
		})
		a := p.Auth(Chat)
		if a.Header != "Authorization" || a.Prefix != "Bearer " {
			t.Fatalf("Auth(chat) = %+v, want the adapter default header", a)
		}
		if got := a.Extra["X-Org"]; got != "acme" {
			t.Fatalf("Auth(chat).Extra = %+v, want X-Org acme", a.Extra)
		}
	})

	t.Run("prefix only overrides the adapter default prefix", func(t *testing.T) {
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "deepseek",
			Chat: "https://chat.example/v1",
			Auth: config.Auth{Prefix: "Token "},
		})
		if a := p.Auth(Chat); a.Header != "Authorization" || a.Prefix != "Token " {
			t.Fatalf("Auth(chat) = %+v, want the adapter header with the configured prefix", a)
		}
		if a := p.Auth(Anthropic); a.Header != "x-api-key" || a.Prefix != "Token " {
			t.Fatalf("Auth(anthropic) = %+v, want the adapter header with the configured prefix", a)
		}
	})

	t.Run("key headers only keep the adapter default", func(t *testing.T) {
		extra := config.KeyHeader{Header: "X-Extra", Prefix: "Key "}
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "opencode-go",
			Chat: "https://chat.example/v1",
			Auth: config.Auth{KeyHeaders: []config.KeyHeader{extra}},
		})
		a := p.Auth(Chat)
		if a.Header != "Authorization" || a.Prefix != "Bearer " {
			t.Fatalf("Auth(chat) = %+v, want the adapter default header", a)
		}
		if !slices.Contains(a.KeyHeaders, config.KeyHeader{Header: "x-api-key"}) {
			t.Fatalf("Auth(chat).KeyHeaders = %+v, want the adapter default preserved", a.KeyHeaders)
		}
		if !slices.Contains(a.KeyHeaders, extra) {
			t.Fatalf("Auth(chat).KeyHeaders = %+v, want %+v merged in", a.KeyHeaders, extra)
		}
	})

	t.Run("full override still replaces the adapter default", func(t *testing.T) {
		override := config.Auth{
			Header: "X-Custom",
			Prefix: "Token ",
			Extra:  map[string]string{"X-Org": "test"},
		}
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "opencode-go",
			Chat: "https://chat.example/v1",
			Auth: override,
		})
		a := p.Auth(Chat)
		if a.Header != override.Header || a.Prefix != override.Prefix {
			t.Fatalf("Auth(chat) = %+v, want the full override", a)
		}
		if len(a.KeyHeaders) != 0 {
			t.Fatalf("Auth(chat).KeyHeaders = %+v, want the override to replace adapter defaults", a.KeyHeaders)
		}
	})
}

func TestAuthOverride(t *testing.T) {
	override := config.Auth{
		Header:     "X-Custom",
		Prefix:     "Token ",
		KeyHeaders: []config.KeyHeader{{Header: "X-Extra", Prefix: "Key "}},
		Extra:      map[string]string{"X-Org": "test"},
	}
	for _, typ := range []string{"deepseek", "opencode-go", "commandcode", "openrouter", "generic"} {
		t.Run(typ, func(t *testing.T) {
			p := mustProvider(t, config.Provider{
				Name: typ,
				Type: typ,
				Chat: "https://chat.example/v1",
				Auth: override,
				Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-test"}},
			})
			if got := p.Auth(Anthropic); got.Header != override.Header || got.Prefix != override.Prefix {
				t.Fatalf("Auth(anthropic) = %+v, want config override", got)
			}
		})
	}
}
