package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
)

func setupAliasRoute(t *testing.T) (*recordingReloader, string, string, *http.Client, string) {
	t.Helper()
	_, rel, path, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/providers", providerBody("p", "https://a.example"), http.StatusCreated},
		{http.MethodPost, "/api/nodes", `{"name":"n","provider":"p"}`, http.StatusCreated},
		{http.MethodPost, "/api/routes", `{"name":"strong","nodes":["n"],"match":{"model":"strong"}}`, http.StatusCreated},
		{http.MethodPut, "/api/aliases", `[{"match":"claude*","model":"strong"}]`, http.StatusNoContent},
	} {
		if resp := doJSON(t, client, tc.method, ts.URL+tc.path, tc.body, csrf); resp.StatusCode != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
	return rel, path, ts.URL, client, csrf
}

func TestRouteRenameUpdatesAliases(t *testing.T) {
	rel, path, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
		`{"name":"strong","nodes":["n"],"match":{"model":"strong-2"}}`, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename = %d", resp.StatusCode)
	}
	if got := rel.cfg.Aliases[0].Model; got != "strong-2" {
		t.Fatalf("alias model = %q, want strong-2", got)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Aliases[0].Model; got != "strong-2" {
		t.Fatalf("saved alias model = %q, want strong-2", got)
	}
}

func TestRouteRenameToCatchAllRejectedWhileAliased(t *testing.T) {
	_, _, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
		`{"name":"strong","nodes":["n"],"match":{"model":""}}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rename to catch-all = %d, want 400", resp.StatusCode)
	}
	b, _ := readAll(resp.Body)
	if !strings.Contains(string(b), "still reference") {
		t.Fatalf("body = %s, want a reference hint", b)
	}
}

func TestRouteDeleteRejectedWhileAliased(t *testing.T) {
	_, _, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/strong", "", csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete = %d, want 400", resp.StatusCode)
	}
	b, _ := readAll(resp.Body)
	if !strings.Contains(string(b), "claude*") {
		t.Fatalf("body = %s, want the alias glob", b)
	}
}

func TestRouteDeleteAfterAliasesCleared(t *testing.T) {
	_, _, base, client, csrf := setupAliasRoute(t)
	if resp := doJSON(t, client, http.MethodPut, base+"/api/aliases", `[]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear aliases = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/strong", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
}
