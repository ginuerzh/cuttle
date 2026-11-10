package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
)

// createProviderTest creates a provider through the API and returns its id.
func createProviderTest(t *testing.T, client *http.Client, base, csrf, body, name string) string {
	t.Helper()

	if resp := doJSON(t, client, http.MethodPost, base+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	return providerID(t, client, base, name)
}

// providerUpdateBody builds an update payload that keeps the provider type and
// base URL unchanged.
func providerUpdateBody(name, extra string) string {
	body := `{"name":"` + name + `","type":"generic","chat":"https://a.example"`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func TestProviderCRUDByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	// The server assigns the id: a client-supplied one is ignored.
	body := `{"id":"p_0000000000","name":"alpha","type":"generic","chat":"https://a.example"}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create = %d: %s", resp.StatusCode, b)
	}

	list := listProviders(t, client, ts.URL)
	if len(list) != 1 {
		t.Fatalf("providers = %+v, want one", list)
	}
	id := list[0].ID
	if !identity.Valid(identity.Provider, id) || id == "p_0000000000" {
		t.Fatalf("id = %q, want a generated provider id", id)
	}
	if list[0].Name != "alpha" {
		t.Fatalf("name = %q, want alpha", list[0].Name)
	}

	// PUT by id renames the provider and keeps the id.
	rename := `{"name":"beta","type":"generic","chat":"https://a.example"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, rename, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update by id = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers; len(got) != 1 || got[0].ID != id || got[0].Name != "beta" {
		t.Fatalf("stored providers = %+v, want the same id renamed to beta", got)
	}
	if got := getProvider(t, client, ts.URL, id); got.ID != id || got.Name != "beta" {
		t.Fatalf("view after rename = %+v", got)
	}

	// GET /api/config carries the id, so a round-trip cannot drop it.
	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	var cfg struct {
		Providers []providerJSON `json:"providers"`
	}
	if err := jsonDecode(cfgResp.Body, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].ID != id || cfg.Providers[0].Name != "beta" {
		t.Fatalf("config providers = %+v", cfg.Providers)
	}

	// Writes are addressed by id; the display name is not a handle.
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/beta", rename, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update by name = %d, want 400", resp.StatusCode)
	}

	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/"+id, "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete by id = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Providers) != 0 {
		t.Fatalf("providers after delete = %+v", rel.cfg.Providers)
	}
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/providers/"+id, "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted provider = %d, want 404", resp.StatusCode)
	}
}

func TestProviderUpdateRejectsMismatchedID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	id := createProviderTest(t, client, ts.URL, csrf, providerUpdateBody("alpha", ""), "alpha")
	other := identity.Derive(identity.Provider, "another-provider")

	body := `{"id":"` + other + `","name":"alpha","type":"generic","chat":"https://a.example"}`
	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, body, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched id = %d: %s, want 400", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "does not match the path") {
		t.Fatalf("error = %s, want it to name the path mismatch", raw)
	}
	if got := rel.cfg.Providers[0]; got.ID != id || got.Name != "alpha" {
		t.Fatalf("provider changed after a rejected update: %+v", got)
	}

	// A body id equal to the path id is allowed.
	same := `{"id":"` + id + `","name":"gamma","type":"generic","chat":"https://a.example"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, same, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("matching id = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers[0]; got.ID != id || got.Name != "gamma" {
		t.Fatalf("provider after a matching-id update = %+v", got)
	}
}

func TestProviderKeyUpdateByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	id := createProviderTest(t, client, ts.URL, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"key-a","key":"sk-a"},{"name":"key-b","key":"sk-b"}]}`,
		"p")

	view := getProvider(t, client, ts.URL, id)
	if len(view.Keys) != 2 {
		t.Fatalf("keys = %+v, want two", view.Keys)
	}
	for _, k := range view.Keys {
		if !identity.Valid(identity.ProviderKey, k.ID) {
			t.Fatalf("key %q has id %q, want a generated pk_ id", k.Name, k.ID)
		}
	}
	keyA, keyB := view.Keys[0].ID, view.Keys[1].ID
	if view.Keys[0].Name != "key-a" || view.Keys[1].Name != "key-b" {
		t.Fatalf("keys = %+v, want key-a then key-b", view.Keys)
	}

	update := func(updates string) *http.Response {
		body := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":` + updates + `}`
		return doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, body, csrf)
	}
	keys := func() []config.ProviderKey { return rel.cfg.Providers[0].Keys }

	// Rename by key id keeps the id and the stored value.
	if resp := update(`[{"id":"` + keyA + `","name":"renamed"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rename = %d: %s", resp.StatusCode, b)
	}
	if got := keys(); len(got) != 2 || got[0].ID != keyA || got[0].Name != "renamed" || got[0].Key != "sk-a" {
		t.Fatalf("keys after rename = %+v", got)
	}

	// The enabled flag is set by key id.
	if resp := update(`[{"id":"` + keyA + `","enabled":false}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("disable = %d: %s", resp.StatusCode, b)
	}
	if got := keys(); got[0].ID != keyA || got[0].EnabledValue() {
		t.Fatalf("keys after disable = %+v, want the renamed key disabled", got)
	}

	// The value is replaced by key id without changing the name.
	if resp := update(`[{"id":"` + keyB + `","key":"sk-b2"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rotate = %d: %s", resp.StatusCode, b)
	}
	if got := keys(); got[1].ID != keyB || got[1].Name != "key-b" || got[1].Key != "sk-b2" {
		t.Fatalf("keys after rotate = %+v", got)
	}

	// An empty id and a value append a new key with a generated id.
	if resp := update(`[{"name":"key-c","key":"sk-c"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("append = %d: %s", resp.StatusCode, b)
	}
	got := keys()
	if len(got) != 3 || got[2].Name != "key-c" || got[2].Key != "sk-c" {
		t.Fatalf("keys after append = %+v, want key-c appended", got)
	}
	keyC := got[2].ID
	if !identity.Valid(identity.ProviderKey, keyC) || keyC == keyA || keyC == keyB {
		t.Fatalf("appended key id = %q, want a fresh pk_ id", keyC)
	}

	// A rename onto another key's name is rejected.
	if resp := update(`[{"id":"` + keyA + `","name":"key-b"}]`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate rename = %d, want 400", resp.StatusCode)
	}
	// A dangling key id is rejected.
	if resp := update(`[{"id":"pk_0000000000","name":"x"}]`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("dangling key id = %d, want 400", resp.StatusCode)
	}

	// A key pinned by a node of the same provider cannot be deleted.
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"n","provider":"`+id+`","model":"m1","key":"`+keyB+`"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}
	resp := update(`[{"id":"` + keyB + `","delete":true}]`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete pinned key = %d, want 400", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "is used by node") {
		t.Fatalf("error = %s, want it to name the node", raw)
	}

	// An unpinned key is deleted by id.
	if resp := update(`[{"id":"` + keyA + `","delete":true}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	got = keys()
	if len(got) != 2 || got[0].ID != keyB || got[1].ID != keyC {
		t.Fatalf("keys after delete = %+v, want key-b and key-c", got)
	}
}

func TestProviderImmutabilityByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	const chat = "https://api.deepseek.com/v1"
	body := `{"name":"original","type":"deepseek","chat":"` + chat + `"}`
	id := createProviderTest(t, client, ts.URL, csrf, body, "original")
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"n","provider":"`+id+`","model":"m1"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}
	calls := rel.calls

	// A rename is allowed and keeps the id.
	rename := `{"name":"renamed","type":"deepseek","chat":"` + chat + `"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, rename, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rename = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers[0]; got.ID != id || got.Name != "renamed" {
		t.Fatalf("provider after rename = %+v", got)
	}
	// Every reference still points at the id.
	if got := rel.cfg.Nodes; len(got) != 1 || got[0].Provider != id {
		t.Fatalf("nodes after rename = %+v, want the provider id kept", got)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/"+id, "", csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete a renamed provider in use = %d, want 400", resp.StatusCode)
	}

	// The lock follows the id, not the name.
	for _, changed := range []string{
		`{"name":"renamed","type":"opencode-go","chat":"` + chat + `"}`,
		`{"name":"renamed","type":"deepseek","chat":"https://example.com/v1"}`,
	} {
		if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id, changed, csrf); resp.StatusCode != http.StatusBadRequest {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("change after rename = %d, want 400: %s", resp.StatusCode, b)
		}
	}
	if rel.calls != calls+1 {
		t.Fatalf("reload calls = %d, want %d (only the rename reloads)", rel.calls, calls+1)
	}
	if got := rel.cfg.Providers[0]; got.ID != id || got.Type != "deepseek" || got.Chat != chat {
		t.Fatalf("provider changed after rejected updates: %+v", got)
	}
}

func TestProviderModelsAndProbeUseID(t *testing.T) {
	srv, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"chat-model"}]}`))
	}))
	t.Cleanup(up.Close)

	body := `{"name":"alpha","type":"generic","chat":"` + up.URL + `/v1","keys":[{"name":"key-1","key":"sk-test"}]}`
	id := createProviderTest(t, client, ts.URL, csrf, body, "alpha")

	if got := readProviderModels(t, client, ts.URL+"/api/providers/"+id+"/models"); len(got) != 1 || got[0].ID != "chat-model" {
		t.Fatalf("models = %+v", got)
	}
	srv.mu.Lock()
	_, cached := srv.modelsCache[id]
	srv.mu.Unlock()
	if !cached {
		t.Fatal("the catalog cache is not keyed by the provider id")
	}

	// A rename keeps the id and the cached catalog.
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+id,
		`{"name":"beta","type":"generic","chat":"`+up.URL+`/v1"}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rename = %d: %s", resp.StatusCode, b)
	}
	if got := readProviderModels(t, client, ts.URL+"/api/providers/"+id+"/models"); len(got) != 1 || got[0].ID != "chat-model" {
		t.Fatalf("models after rename = %+v", got)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1: the rename must keep the cached catalog", got)
	}

	// The on-demand probe is addressed by id.
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers/"+id+"/test", "", csrf)
	var res probeResult
	if err := jsonDecode(resp.Body, &res); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !res.OK {
		t.Fatalf("probe = %+v (status %d)", res, resp.StatusCode)
	}

	// The periodic probe records its result under the provider id.
	srv.ProbeOnce(context.Background())
	srv.mu.Lock()
	byID, ok := srv.probes[id]
	_, byName := srv.probes["beta"]
	srv.mu.Unlock()
	if !ok || !byID.OK || byName {
		t.Fatalf("probe by %q = %+v (found %v), recorded under the name = %v", id, byID, ok, byName)
	}
	latest, err := rel.store.LatestProbes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := latest[id]; !ok {
		t.Fatalf("persisted probes = %+v, want the provider id", latest)
	}
}
