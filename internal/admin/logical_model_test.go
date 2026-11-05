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
		{http.MethodPost, "/api/nodes", `{"name":"n","provider":"p","models":{"strong":"pro","*":"flash"}}`, http.StatusCreated},
		{http.MethodPost, "/api/routes", `{"name":"strong","nodes":["n"],"match":{"model":"strong"}}`, http.StatusCreated},
		{http.MethodPost, "/api/routes", `{"name":"fast","nodes":["n"],"match":{"model":"fast"}}`, http.StatusCreated},
		{http.MethodPut, "/api/aliases", `[{"match":"claude*","model":"strong"},{"match":"gpt*","model":"fast"}]`, http.StatusNoContent},
	} {
		if resp := doJSON(t, client, tc.method, ts.URL+tc.path, tc.body, csrf); resp.StatusCode != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
	return rel, path, ts.URL, client, csrf
}

func routeByName(t *testing.T, cfg *config.Config, name string) config.Route {
	t.Helper()
	for _, rt := range cfg.Routes {
		if rt.Name == name {
			return rt
		}
	}
	t.Fatalf("route %q not found", name)
	return config.Route{}
}

func TestRouteRenameUpdatesAliasesAndNodeModels(t *testing.T) {
	rel, path, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
		`{"name":"strong","nodes":["n"],"match":{"model":"strong-2"}}`, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename = %d", resp.StatusCode)
	}
	if got := rel.cfg.Aliases[0].Model; got != "strong-2" {
		t.Fatalf("claude alias model = %q, want strong-2", got)
	}
	if got := rel.cfg.Aliases[1].Model; got != "fast" {
		t.Fatalf("gpt alias model = %q, want fast", got)
	}
	models := rel.cfg.Nodes[0].Models
	if got := models["strong-2"]; got != "pro" {
		t.Fatalf("node strong-2 = %q, want pro (models: %#v)", got, models)
	}
	if _, ok := models["strong"]; ok {
		t.Fatalf("old node mapping should be removed: %#v", models)
	}
	if got := rel.cfg.Nodes[0].EffectiveModel("strong-2"); got != "pro" {
		t.Fatalf("EffectiveModel(strong-2) = %q, want pro", got)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Aliases[0].Model; got != "strong-2" {
		t.Fatalf("saved alias model = %q, want strong-2", got)
	}
	if got := saved.Nodes[0].Models["strong-2"]; got != "pro" {
		t.Fatalf("saved node mapping = %q, want pro", got)
	}
}

func TestRouteRenameLeavesAliasesWhenAnotherRouteKeepsOldModel(t *testing.T) {
	rel, _, base, client, csrf := setupAliasRoute(t)
	if resp := doJSON(t, client, http.MethodPost, base+"/api/routes",
		`{"name":"strong-alt","nodes":["n"],"match":{"model":"strong"}}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create strong-alt = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
		`{"name":"strong","nodes":["n"],"match":{"model":"strong-2"}}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename = %d", resp.StatusCode)
	}
	if got := rel.cfg.Aliases[0].Model; got != "strong" {
		t.Fatalf("claude alias model = %q, want strong (strong-alt still defines it)", got)
	}
	if got := rel.cfg.Nodes[0].Models["strong"]; got != "pro" {
		t.Fatalf("node strong = %q, want pro", got)
	}
	if _, ok := rel.cfg.Nodes[0].Models["strong-2"]; ok {
		t.Fatalf("node mapping must not move while strong-alt still defines strong: %#v", rel.cfg.Nodes[0].Models)
	}
}

func TestRouteModelCaseOrSpaceOnlyEditKeepsStoredName(t *testing.T) {
	for _, model := range []string{"Strong", " strong "} {
		rel, _, base, client, csrf := setupAliasRoute(t)
		resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
			`{"name":"strong","nodes":["n"],"match":{"model":"`+model+`"}}`, csrf)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("edit to %q = %d", model, resp.StatusCode)
		}
		if got := routeByName(t, rel.cfg, "strong").Match.Model; got != "strong" {
			t.Fatalf("stored model = %q after edit to %q, want strong", got, model)
		}
		if got := rel.cfg.Nodes[0].Models["strong"]; got != "pro" {
			t.Fatalf("node mapping changed after edit to %q: %#v", model, rel.cfg.Nodes[0].Models)
		}
	}
}

func TestRouteCreateTrimsLogicalModel(t *testing.T) {
	rel, _, base, client, csrf := setupAliasRoute(t)
	if resp := doJSON(t, client, http.MethodPost, base+"/api/routes",
		`{"name":"trimmed","nodes":["n"],"match":{"model":"  trimmed  "}}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	if got := routeByName(t, rel.cfg, "trimmed").Match.Model; got != "trimmed" {
		t.Fatalf("stored model = %q, want trimmed", got)
	}
}

func TestRouteRenameToCatchAllRejectedWhileAliased(t *testing.T) {
	rel, _, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/strong",
		`{"name":"strong","nodes":["n"],"match":{"model":""}}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rename to catch-all = %d, want 400", resp.StatusCode)
	}
	b, _ := readAll(resp.Body)
	if !strings.Contains(string(b), "still reference") {
		t.Fatalf("body = %s, want a reference hint", b)
	}
	if got := routeByName(t, rel.cfg, "strong").Match.Model; got != "strong" {
		t.Fatalf("rejected update changed the route: %q", got)
	}
}

func TestRouteDeleteRejectedWhileAliased(t *testing.T) {
	rel, _, base, client, csrf := setupAliasRoute(t)
	resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/strong", "", csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete = %d, want 400", resp.StatusCode)
	}
	b, _ := readAll(resp.Body)
	if !strings.Contains(string(b), "claude*") {
		t.Fatalf("body = %s, want the alias glob", b)
	}
	if got := routeByName(t, rel.cfg, "strong").Name; got != "strong" {
		t.Fatalf("rejected delete removed the route: %q", got)
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

func TestMigrateRouteNodeModelsRejectsConflict(t *testing.T) {
	c := &config.Config{Nodes: []config.Node{{
		Name:   "n",
		Models: map[string]string{"strong": "pro", "strong-2": "other"},
	}}}
	err := migrateRouteNodeModels(c, []string{"n"}, "strong", "strong-2")
	if err == nil || !strings.Contains(err.Error(), "already mapped") {
		t.Fatalf("err = %v, want a mapping conflict", err)
	}
	if got := c.Nodes[0].Models["strong"]; got != "pro" {
		t.Fatalf("strong = %q, want pro", got)
	}
	if got := c.Nodes[0].Models["strong-2"]; got != "other" {
		t.Fatalf("strong-2 = %q, want other", got)
	}
}
