package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
)

func setupRouteFixtures(t *testing.T) (string, *http.Client, string) {
	t.Helper()
	_, _, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, ts.URL)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/providers", providerBody("p", "https://a.example"), http.StatusCreated},
		{http.MethodPost, "/api/nodes", `{"name":"n","provider":"p","model":"m1"}`, http.StatusCreated},
		{http.MethodPost, "/api/routes", `{"name":"fast","nodes":["n"],"strategy":"round"}`, http.StatusCreated},
		{http.MethodPost, "/api/routes", `{"name":"default","nodes":["n"],"strategy":"round"}`, http.StatusCreated},
	} {
		if resp := doJSON(t, client, tc.method, base+tc.path, tc.body, csrf); resp.StatusCode != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
	return base, client, csrf
}

func TestEntrypointListAndPut(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)

	resp := doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	var list []config.Entrypoint
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("entrypoints = %+v, want empty", list)
	}

	body := `[{"name":"fast","match":["gpt-4.5*"],"route":"fast"},{"name":"default","match":["*"],"route":"default"}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}

	resp = doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "fast" || list[1].Name != "default" {
		t.Fatalf("entrypoints = %+v, want fast then default", list)
	}

	resp = doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	var got []config.Entrypoint
	if err := json.Unmarshal(cfg["entrypoints"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "fast" {
		t.Fatalf("config entrypoints = %+v", got)
	}
}

func TestEntrypointPutRejectsUnknownRoute(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"fast","match":["*"],"route":"missing"}]`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if len(s.cfg.Entrypoints) != 0 {
		t.Fatalf("failed put changed config: %+v", s.cfg.Entrypoints)
	}
}

func TestRouteDeleteRejectedWhileEntrypointReferences(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)

	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"fast","match":["*"],"route":"fast"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}

	resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/fast", "", csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete = %d, want 400", resp.StatusCode)
	}
	b, _ := readAll(resp.Body)
	if !strings.Contains(string(b), "fast") {
		t.Fatalf("body = %s, want the entrypoint name", b)
	}

	routes := doJSON(t, client, http.MethodGet, base+"/api/routes", "", "")
	var list []config.Route
	if err := json.NewDecoder(routes.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("rejected delete removed a route: %+v", list)
	}
}

func TestRouteDeleteAfterEntrypointRemoved(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)

	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", `[]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/fast", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
}

func TestRouteCreateHasNoMatchField(t *testing.T) {
	base, client, _ := setupRouteFixtures(t)

	resp := doJSON(t, client, http.MethodGet, base+"/api/routes", "", "")
	var list []map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	for _, route := range list {
		if _, ok := route["match"]; ok {
			t.Fatalf("route still exposes match: %+v", route)
		}
	}
}

func TestRouteOrderEndpointRemoved(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)

	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/order", `["default","fast"]`, csrf)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("route order status = %d, want 400/404", resp.StatusCode)
	}

	routes := doJSON(t, client, http.MethodGet, base+"/api/routes", "", "")
	var list []config.Route
	if err := json.NewDecoder(routes.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "fast" || list[1].Name != "default" {
		t.Fatalf("route order changed: %+v", list)
	}
}
