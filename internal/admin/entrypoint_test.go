package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
)

// setupRouteFixtures creates one provider, one node and the fast/default
// routes that reference the node id.
func setupRouteFixtures(t *testing.T) (string, *http.Client, string) {
	t.Helper()
	_, _, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, ts.URL)
	if resp := doJSON(t, client, http.MethodPost, base+"/api/providers", providerBody("p", "https://a.example"), csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider = %d, want 201", resp.StatusCode)
	}
	pid := providerID(t, client, base, "p")
	if resp := doJSON(t, client, http.MethodPost, base+"/api/nodes",
		`{"name":"n","provider":"`+pid+`","model":"m1"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node = %d, want 201", resp.StatusCode)
	}
	nid := nodeID(t, client, base, "n")
	for _, body := range []string{
		`{"name":"fast","nodes":["` + nid + `"],"strategy":"round"}`,
		`{"name":"default","nodes":["` + nid + `"],"strategy":"round"}`,
	} {
		if resp := doJSON(t, client, http.MethodPost, base+"/api/routes", body, csrf); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create route = %d, want 201", resp.StatusCode)
		}
	}
	return base, client, csrf
}

func TestEntrypointListAndPut(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	fastID := routeID(t, client, base, "fast")
	defaultID := routeID(t, client, base, "default")

	resp := doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	var list []config.Entrypoint
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("entrypoints = %+v, want empty", list)
	}

	body := `[{"name":"fast","match":["gpt-4.5*"],"routes":[{"route":"` + fastID + `"}]},{"name":"default","match":["*"],"routes":[{"route":"` + defaultID + `"}]}]`
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
	for i, want := range []string{fastID, defaultID} {
		if len(list[i].Routes) != 1 || list[i].Routes[0].Route != want {
			t.Fatalf("entrypoint %d routes = %+v, want the route id %q", i, list[i].Routes, want)
		}
		if list[i].ID == "" {
			t.Fatalf("entrypoint %d has no id", i)
		}
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
	if len(got) != 2 || got[0].Name != "fast" || len(got[0].Routes) != 1 || got[0].Routes[0].Route != fastID {
		t.Fatalf("config entrypoints = %+v", got)
	}
}

func TestConfigRoundTripEntrypointBindings(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, ts.URL)

	if resp := doJSON(t, client, http.MethodPost, base+"/api/providers", providerBody("p", "https://a.example"), csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider = %d, want 201", resp.StatusCode)
	}
	pid := providerID(t, client, base, "p")
	if resp := doJSON(t, client, http.MethodPost, base+"/api/nodes",
		`{"name":"n","provider":"`+pid+`","model":"m1"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node = %d, want 201", resp.StatusCode)
	}
	nid := nodeID(t, client, base, "n")
	for _, name := range []string{"cheap", "premium"} {
		if resp := doJSON(t, client, http.MethodPost, base+"/api/routes",
			`{"name":"`+name+`","nodes":["`+nid+`"]}`, csrf); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create route %s = %d, want 201", name, resp.StatusCode)
		}
	}
	cheapID := routeID(t, client, base, "cheap")
	premiumID := routeID(t, client, base, "premium")

	topology := func(bindingList string) string {
		return `{"providers":[` + providerBody("p", "https://a.example") + `],` +
			`"nodes":[{"name":"n","provider":"` + pid + `","model":"m1"}],` +
			`"routes":[{"name":"cheap","nodes":["` + nid + `"]},{"name":"premium","nodes":["` + nid + `"]}],` +
			`"entrypoints":[{"name":"claude","match":["claude-*"],"routes":[` + bindingList + `]}]}`
	}
	bindings := `{"route":"` + cheapID + `","probe":{"command":"true","interval":"10s","timeout":"2s"}},` +
		`{"route":"` + premiumID + `"}`
	putConfig(t, client, base, csrf, topology(bindings))

	got := rel.cfg.Entrypoints
	if len(got) != 1 || len(got[0].Routes) != 2 {
		t.Fatalf("entrypoints = %+v, want one entrypoint with two bindings", got)
	}
	if got[0].Routes[0].Route != cheapID || got[0].Routes[1].Route != premiumID {
		t.Fatalf("binding order = %+v, want cheap then premium", got[0].Routes)
	}
	probe := got[0].Routes[0].Probe
	if probe == nil || probe.Command != "true" ||
		probe.Interval != config.Duration(10*time.Second) || probe.Timeout != config.Duration(2*time.Second) {
		t.Fatalf("probe = %+v, want true/10s/2s", probe)
	}
	if got[0].Routes[1].Probe != nil {
		t.Fatalf("second binding probe = %+v, want none", got[0].Routes[1].Probe)
	}

	// GET /api/config round-trips the same ordered bindings and the probe.
	resp := doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	var view struct {
		Entrypoints []config.Entrypoint `json:"entrypoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entrypoints) != 1 || len(view.Entrypoints[0].Routes) != 2 ||
		view.Entrypoints[0].Routes[1].Route != premiumID ||
		view.Entrypoints[0].Routes[0].Probe == nil ||
		view.Entrypoints[0].Routes[0].Probe.Command != "true" {
		t.Fatalf("GET /api/config entrypoints = %+v, want both bindings and the probe", view.Entrypoints)
	}

	// A dangling binding route id is rejected and leaves the stored config
	// untouched.
	resp = doJSON(t, client, http.MethodPut, base+"/api/config", topology(`{"route":"r_0000000000"}`), csrf)
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("dangling binding = %d: %s, want 400", resp.StatusCode, b)
	}
	if got := rel.cfg.Entrypoints; len(got) != 1 || len(got[0].Routes) != 2 {
		t.Fatalf("a rejected PUT changed the entrypoints: %+v", got)
	}
}

func TestEntrypointPutRejectsUnknownRoute(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"fast","match":["*"],"routes":[{"route":"missing"}]}]`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if len(s.cfg.Entrypoints) != 0 {
		t.Fatalf("failed put changed config: %+v", s.cfg.Entrypoints)
	}
}

func TestRouteDeleteRejectedWhileEntrypointReferences(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	rid := routeID(t, client, base, "fast")

	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"fast","match":["*"],"routes":[{"route":"`+rid+`"}]}]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}

	resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/"+rid, "", csrf)
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
	rid := routeID(t, client, base, "fast")

	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", `[]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/"+rid, "", csrf); resp.StatusCode != http.StatusNoContent {
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

func TestRouteRetryFieldsRoundTrip(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	nid := nodeID(t, client, base, "n")

	if resp := doJSON(t, client, http.MethodPost, base+"/api/routes",
		`{"name":"retry","nodes":["`+nid+`"],"retries":2,"retryCodes":"429,502"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create route = %d, want 201", resp.StatusCode)
	}
	rid := routeID(t, client, base, "retry")

	routeRetryFields := func() (json.RawMessage, json.RawMessage) {
		t.Helper()
		resp := doJSON(t, client, http.MethodGet, base+"/api/routes", "", "")
		var list []map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		for _, route := range list {
			var id string
			if err := json.Unmarshal(route["id"], &id); err != nil {
				t.Fatal(err)
			}
			if id == rid {
				return route["retries"], route["retryCodes"]
			}
		}
		t.Fatalf("route retry missing from %+v", list)
		return nil, nil
	}

	assertRetry := func(wantRetries, wantCodes string) {
		t.Helper()
		retries, codes := routeRetryFields()
		if got := string(retries); got != wantRetries {
			t.Fatalf("retries = %s, want %s", got, wantRetries)
		}
		if got := string(codes); got != wantCodes {
			t.Fatalf("retryCodes = %s, want %s", got, wantCodes)
		}
	}

	assertRetry("2", `"429,502"`)

	if resp := doJSON(t, client, http.MethodPut, base+"/api/routes/"+rid,
		`{"name":"retry","nodes":["`+nid+`"],"retries":1,"retryCodes":"5xx"}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("update route = %d, want 204", resp.StatusCode)
	}

	assertRetry("1", `"5xx"`)
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
