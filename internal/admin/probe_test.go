package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

func TestProbeUsesFirstEnabledKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	disabled := false
	res := probeHTTP(context.Background(), config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL + "/v1",
		Keys: []config.ProviderKey{
			{Name: "a", Key: "sk-a", Enabled: &disabled},
			{Name: "b", Key: "sk-b"},
		},
		Auth:   config.Auth{Header: "Authorization", Prefix: "Bearer "},
		Health: config.Health{Path: "/health"},
	})
	if !res.OK {
		t.Fatalf("probe = %+v, want OK", res)
	}
	if got != "Bearer sk-b" {
		t.Fatalf("Authorization = %q, want Bearer sk-b", got)
	}
}

func TestProbeUsesChatBaseForGeminiPreset(t *testing.T) {
	p := config.Provider{
		Name: "gm", Type: "gemini",
		Chat:   "https://generativelanguage.googleapis.com/v1beta/openai",
		Gemini: "https://generativelanguage.googleapis.com/v1beta",
		Keys:   []config.ProviderKey{{Name: "k", Key: "sk-test"}},
	}
	base, api, _, _, ok := probeTarget(p)
	if !ok {
		t.Fatal("probeTarget: ok = false")
	}
	if api != provider.Chat || base != p.Chat {
		t.Fatalf("probe = (%s, %s), want chat base with Bearer", base, api)
	}
}
