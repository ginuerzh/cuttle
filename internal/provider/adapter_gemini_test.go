package provider

import (
	"slices"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func TestGeminiClassifyAndResolve(t *testing.T) {
	p := mustProvider(t, config.Provider{Name: "gm", Type: "gemini", Gemini: "https://generativelanguage.googleapis.com/v1beta", Chat: "https://generativelanguage.googleapis.com/v1beta/openai"})
	m, ok := p.Resolve("gemini-2.5-pro")
	if !ok {
		t.Fatal("Resolve: ok = false, want true")
	}
	if !slices.Equal(m.APIs, []API{Chat, Gemini}) {
		t.Fatalf("APIs = %v, want [chat gemini]", m.APIs)
	}
	if _, ok := p.Resolve(""); ok {
		t.Fatal("Resolve(\"\"): ok = true, want false")
	}
}

func TestGeminiAuth(t *testing.T) {
	p := mustProvider(t, config.Provider{Name: "gm", Type: "gemini", Chat: "https://x.example/v1beta/openai", Gemini: "https://x.example/v1beta"})
	if a := p.Auth(Chat); a.Header != "Authorization" || a.Prefix != "Bearer " {
		t.Fatalf("chat auth = %+v", a)
	}
	if a := p.Auth(Gemini); a.Header != "x-goog-api-key" || a.Prefix != "" {
		t.Fatalf("gemini auth = %+v", a)
	}
}
