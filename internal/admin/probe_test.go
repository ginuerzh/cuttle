package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
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
