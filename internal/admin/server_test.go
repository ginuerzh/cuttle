package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/capture"
	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
	"github.com/ginuerzh/cuttle/internal/stats"
)

type recordingReloader struct {
	cfg      *config.Config
	err      error
	calls    int
	attempts int
	store    *stats.Store
	capture  *capture.Manager
	health   []health.Route
}

func (r *recordingReloader) Stats() *stats.Store { return r.store }

func (r *recordingReloader) Capture() *capture.Manager { return r.capture }

func (r *recordingReloader) RouteHealth() []health.Route { return r.health }

func (r *recordingReloader) Reload(c *config.Config) error {
	r.attempts++
	if r.err != nil {
		return r.err
	}
	r.cfg = c
	r.calls++
	return nil
}

func newTestAdmin(t *testing.T) (*Server, *recordingReloader, string, *httptest.Server) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cuttle.yaml")
	hash, err := config.HashPassword("pass")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Admin.Listen = "127.0.0.1:0"
	cfg.Admin.PasswordHash = hash
	cfg.Admin.SessionTTL = config.Duration(time.Hour)
	config.ApplyDefaults(cfg)

	store, err := stats.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rel := &recordingReloader{
		store:   store,
		capture: capture.NewManager(capture.Policy{Mode: "off", MaxBytes: 32768}),
	}
	s := New(path, cfg, rel, xlogger.Nop())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, rel, path, ts
}

func doJSON(t *testing.T, c *http.Client, method, u, body, csrf string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func login(t *testing.T, tsURL string) (*http.Client, string) {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}

	resp := doJSON(t, c, http.MethodPost, tsURL+"/api/login", `{"password":"pass"}`, "")
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d: %s", resp.StatusCode, b)
	}

	u, _ := url.Parse(tsURL)
	var csrf string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == csrfCookie {
			csrf = ck.Value
		}
	}
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}
	return c, csrf
}

// providerJSON is one provider as the HTTP API writes it. The tests decode it
// instead of the Go view type so they assert the wire shape: ids are the
// handles, names are labels.
type providerJSON struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Type string            `json:"type"`
	Chat string            `json:"chat,omitempty"`
	Keys []providerKeyJSON `json:"keys"`
}

// providerKeyJSON is one masked provider key in the provider payload.
type providerKeyJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyPreview string `json:"keyPreview"`
	Enabled    bool   `json:"enabled"`
}

// listProviders returns every provider the API lists.
func listProviders(t *testing.T, client *http.Client, base string) []providerJSON {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, base+"/api/providers", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("list providers = %d: %s", resp.StatusCode, b)
	}
	var out []providerJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// getProvider returns one provider addressed by id.
func getProvider(t *testing.T, client *http.Client, base, id string) providerJSON {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, base+"/api/providers/"+id, "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("get provider %q = %d: %s", id, resp.StatusCode, b)
	}
	var out providerJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// providerID returns the id of the provider with the display name.
func providerID(t *testing.T, client *http.Client, base, name string) string {
	t.Helper()

	for _, p := range listProviders(t, client, base) {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("provider %q is not listed", name)
	return ""
}

func TestRouteHealthEndpoint(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()
	rel.health = []health.Route{{
		ID:          "r_0000000001",
		Name:        "deepseek-flash",
		MaxFails:    2,
		FailTimeout: "30s",
		Nodes: []health.Node{{
			ID:          "n_0000000001",
			Name:        "cc-dsf",
			ProviderID:  "p_0000000001",
			FailCount:   2,
			LastFailure: &now,
			Dead:        true,
			DeadUntil:   &now,
		}},
	}}
	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/routes/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got []health.Route
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "r_0000000001" || got[0].Name != "deepseek-flash" || len(got[0].Nodes) != 1 {
		t.Fatalf("health = %+v", got)
	}
	if !got[0].Nodes[0].Dead || got[0].Nodes[0].FailCount != 2 || got[0].MaxFails != 2 {
		t.Fatalf("node health = %+v", got[0])
	}
}

func TestAuthAndCSRF(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)

	if resp := doJSON(t, &http.Client{}, http.MethodGet, ts.URL+"/api/config", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
	}

	if resp := doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/api/login", `{"password":"wrong"}`, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", resp.StatusCode)
	}

	raw := doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/api/login", `{"password":"pass"}`, "")
	if raw.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d", raw.StatusCode)
	}
	var session *http.Cookie
	for _, ck := range raw.Cookies() {
		if ck.Name == sessionCookie {
			session = ck
		}
	}
	if session == nil || !session.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	client, csrf := login(t, ts.URL)
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/entrypoints", `[{"name":"default","match":["*"],"route":"fast"}]`, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", resp.StatusCode)
	}
	if rel.calls != 0 {
		t.Fatal("rejected request must not reload")
	}

	for _, tc := range []struct {
		path, body string
	}{
		{"/api/providers", providerBody("p", "https://a.example")},
		{"/api/nodes", `{"name":"n","provider":"p","model":"m1"}`},
		{"/api/routes", `{"name":"fast","nodes":["n"]}`},
	} {
		if resp := doJSON(t, client, http.MethodPost, ts.URL+tc.path, tc.body, csrf); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s status = %d", tc.path, resp.StatusCode)
		}
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/entrypoints", `[{"name":"default","match":["*"],"route":"fast"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints status = %d", resp.StatusCode)
	}
	if len(rel.cfg.Entrypoints) != 1 || rel.cfg.Entrypoints[0].Route != "fast" {
		t.Fatalf("entrypoints not applied: %+v", rel.cfg.Entrypoints)
	}

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	b, _ := io.ReadAll(cfgResp.Body)
	if strings.Contains(string(b), "passwordHash") {
		t.Fatal("config response must not include passwordHash")
	}
}

func TestProviderCRUDMasksKeys(t *testing.T) {
	_, rel, path, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	body := `{"name":"p1","type":"generic","chat":"https://api.example.com/v1","keys":[{"name":"key-1","key":"sk-secret-value"}]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d: %s", resp.StatusCode, b)
	}

	list := doJSON(t, client, http.MethodGet, ts.URL+"/api/providers", "", "")
	b, _ := io.ReadAll(list.Body)
	for _, legacy := range []string{"baseURL", "pathMap", `"protocol"`} {
		if strings.Contains(string(b), legacy) {
			t.Fatalf("provider view still exposes legacy field %q: %s", legacy, b)
		}
	}
	if strings.Contains(string(b), "sk-secret-value") {
		t.Fatalf("plaintext key leaked: %s", b)
	}
	if !strings.Contains(string(b), `"name":"key-1"`) || !strings.Contains(string(b), "sk-...alue") {
		t.Fatalf("missing key metadata: %s", b)
	}
	pid := listProviders(t, client, ts.URL)[0].ID
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v mode=%v", err, info.Mode().Perm())
	}

	update := `{"name":"p1","type":"generic","chat":"https://api.example.com/v1"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/"+pid, update, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("update status = %d", resp.StatusCode)
	}
	if len(rel.cfg.Providers[0].Keys) != 1 || rel.cfg.Providers[0].Keys[0].Key != "sk-secret-value" {
		t.Fatalf("keys were cleared by an update without keys: %+v", rel.cfg.Providers[0].Keys)
	}

	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes", `{"name":"n1","provider":"p1","model":"m1"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/routes", `{"name":"r1","nodes":["n1"]}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create route status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/"+pid, "", csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete referenced provider status = %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/nodes/n1", "", csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete referenced node status = %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/routes/r1", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete route status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/nodes/n1", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete node status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/"+pid, "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete provider status = %d", resp.StatusCode)
	}
}

func TestNodeAPIRoundTripThroughAdmin(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", providerBody("p", "https://a.example"), csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes", `{"name":"n","provider":"p","model":"m1","api":"anthropic"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node status = %d", resp.StatusCode)
	}

	list := doJSON(t, client, http.MethodGet, ts.URL+"/api/nodes", "", "")
	var nodes []config.Node
	if err := json.NewDecoder(list.Body).Decode(&nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].API != "anthropic" {
		t.Fatalf("nodes = %+v, want one node with api anthropic", nodes)
	}

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/nodes/n", `{"name":"n","provider":"p","model":"m1","api":"auto"}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("update node status = %d", resp.StatusCode)
	}
	if len(rel.cfg.Nodes) != 1 || rel.cfg.Nodes[0].API != "" {
		t.Fatalf("stored node = %+v, want api normalized away", rel.cfg.Nodes)
	}
}

func TestReloadFailureKeepsFileAndMemory(t *testing.T) {
	s, rel, path, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	for _, tc := range []struct {
		path, body string
	}{
		{"/api/providers", providerBody("p", "https://a.example")},
		{"/api/nodes", `{"name":"n","provider":"p","model":"m1"}`},
		{"/api/routes", `{"name":"fast","nodes":["n"]}`},
	} {
		if resp := doJSON(t, client, http.MethodPost, ts.URL+tc.path, tc.body, csrf); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d", tc.path, resp.StatusCode)
		}
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	attempts := rel.attempts
	rel.err = errors.New("boom")
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/entrypoints", `[{"name":"default","match":["*"],"route":"fast"}]`, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config file changed after a failed reload:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if len(s.cfg.Entrypoints) != 0 {
		t.Fatalf("in-memory config changed after a failed reload: %+v", s.cfg.Entrypoints)
	}
	if rel.attempts != attempts+1 {
		t.Fatalf("reload attempts = %d, want %d", rel.attempts, attempts+1)
	}
}

func TestPresetsMatchSpec(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/presets", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"protocolDefaults", "pathMap", "baseURL", "protocol\""} {
		if strings.Contains(string(body), legacy) {
			t.Fatalf("presets still contain legacy field %q: %s", legacy, body)
		}
	}
	var got []Preset
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("presets = %d, want one per endpoint (5)", len(got))
	}
	byName := make(map[string]Preset, len(got))
	for _, p := range got {
		byName[p.Name] = p
	}
	checks := []struct {
		name      string
		typ       string
		chat      string
		responses string
		anthropic string
		gemini    string
	}{
		{"deepseek", "deepseek", "https://api.deepseek.com/v1", "https://api.deepseek.com/v1", "https://api.deepseek.com/anthropic", ""},
		{"opencode-go", "opencode-go", "https://opencode.ai/inference/go/openai/v1", "https://opencode.ai/inference/go/openai/v1", "https://opencode.ai/inference/go/anthropic", ""},
		{"opencode-zen", "opencode-zen", "https://opencode.ai/zen/v1", "https://opencode.ai/zen/v1", "https://opencode.ai/zen", ""},
		{"commandcode", "commandcode", "https://api.commandcode.ai/provider/v1", "https://api.commandcode.ai/provider/v1", "https://api.commandcode.ai/provider", ""},
		{"openrouter", "openrouter", "https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1", "https://openrouter.ai/api", ""},
	}
	for _, want := range checks {
		p, ok := byName[want.name]
		if !ok {
			t.Fatalf("missing preset %q", want.name)
		}
		if p.Type != want.typ || p.Chat != want.chat || p.Responses != want.responses || p.Anthropic != want.anthropic || p.Gemini != want.gemini {
			t.Fatalf("%s preset = %+v", want.name, p)
		}
	}
	if byName["opencode-go"].Chat == byName["opencode-zen"].Chat {
		t.Fatalf("opencode-go and opencode-zen must have different chat base URLs: %+v", byName)
	}
}

func TestProbeProvider(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("probe path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("X-Org"); got != "acme" {
			t.Errorf("X-Org = %q, want acme", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty for a keyless provider", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	body := `{"name":"local","type":"generic","chat":"` + up.URL + `/v1","auth":{"extra":{"X-Org":"acme"}}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	resp := doJSON(t, client, http.MethodPost, providerURL(ts.URL, providerID(t, client, ts.URL, "local"))+"/test", "", csrf)
	var res probeResult
	if err := jsonDecode(resp.Body, &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Error != "" {
		t.Fatalf("probe = %+v", res)
	}
}

func TestProbeProviderOpenCodeCatalogIsKeyless(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			http.Error(w, "invalid credential", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p := config.Provider{
		Name: "opencode-go",
		Type: "opencode-go",
		Chat: up.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "invalid-key"}},
	}
	if res := probeHTTP(context.Background(), p); !res.OK || res.Error != "" {
		t.Fatalf("probe = %+v, want a keyless catalog", res)
	}
}

func TestProbeProviderAnthropicAPI(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-ant" {
			t.Errorf("x-api-key = %q, want sk-ant", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty for an anthropic probe", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want 2023-06-01", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	body := `{"name":"anthropic","type":"generic","anthropic":"` + up.URL + `","keys":[{"name":"key-1","key":"sk-ant"}]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	pid := providerID(t, client, ts.URL, "anthropic")

	s.ProbeOnce(context.Background())

	s.mu.Lock()
	res, ok := s.probes[pid]
	s.mu.Unlock()
	if !ok || !res.OK || res.Status != http.StatusOK {
		t.Fatalf("probe = %+v", res)
	}
}

func TestProbeProviderAnthropicVersionOverride(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-version"); got != "2024-01-01" {
			t.Errorf("anthropic-version = %q, want the auth.extra override", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p := config.Provider{
		Name:      "p",
		Type:      "generic",
		Anthropic: up.URL,
		Auth:      config.Auth{Extra: map[string]string{"anthropic-version": "2024-01-01"}},
	}
	if res := probeHTTP(context.Background(), p); !res.OK || res.Error != "" {
		t.Fatalf("probe = %+v", res)
	}
}

func TestProbeProviderHealthAPI(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var chatHits, anthropicHits atomic.Int32
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer chat.Close()
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicHits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer anthropic.Close()

	body := `{"name":"p","type":"generic","chat":"` + chat.URL + `","anthropic":"` + anthropic.URL + `","health":{"api":"anthropic"}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	s.ProbeOnce(context.Background())

	if got := chatHits.Load(); got != 0 {
		t.Fatalf("chat upstream hits = %d, want 0 when health.api is anthropic", got)
	}
	if got := anthropicHits.Load(); got != 1 {
		t.Fatalf("anthropic upstream hits = %d, want 1", got)
	}
}

type probeUpstreamServer struct {
	srv  *httptest.Server
	hits atomic.Int32
	path atomic.Value
}

func newProbeUpstreamServer(t *testing.T) *probeUpstreamServer {
	t.Helper()

	u := &probeUpstreamServer{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		u.path.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func TestProbeHTTPDefaults(t *testing.T) {
	tests := []struct {
		name            string
		healthAPI       string
		chat            bool
		responses       bool
		anthropic       bool
		gemini          bool
		anthropicSuffix string
		wantAPI         string
		wantPath        string
	}{
		{name: "chat default", chat: true, wantAPI: "chat", wantPath: "/v1/models"},
		{name: "chat first when multiple APIs are configured", chat: true, anthropic: true, wantAPI: "chat", wantPath: "/v1/models"},
		{name: "health api selects anthropic", healthAPI: "anthropic", chat: true, anthropic: true, wantAPI: "anthropic", wantPath: "/v1/models"},
		{name: "responses fallback", responses: true, wantAPI: "responses", wantPath: "/v1/models"},
		{name: "anthropic root", anthropic: true, wantAPI: "anthropic", wantPath: "/v1/models"},
		{name: "anthropic v1 base", anthropic: true, anthropicSuffix: "/v1", wantAPI: "anthropic", wantPath: "/v1/models"},
		{name: "gemini default", gemini: true, wantAPI: "gemini", wantPath: "/v1beta/models"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chatUp := newProbeUpstreamServer(t)
			responsesUp := newProbeUpstreamServer(t)
			anthropicUp := newProbeUpstreamServer(t)
			geminiUp := newProbeUpstreamServer(t)

			p := config.Provider{
				Name:   "p",
				Type:   "generic",
				Health: config.Health{API: tt.healthAPI},
			}
			if tt.chat {
				p.Chat = chatUp.srv.URL + "/v1"
			}
			if tt.responses {
				p.Responses = responsesUp.srv.URL + "/v1"
			}
			if tt.anthropic {
				p.Anthropic = anthropicUp.srv.URL + tt.anthropicSuffix
			}
			if tt.gemini {
				p.Gemini = geminiUp.srv.URL + "/v1beta"
			}

			res := probeHTTP(context.Background(), p)
			if !res.OK || res.Error != "" {
				t.Fatalf("probe = %+v", res)
			}

			upstreams := map[string]*probeUpstreamServer{
				"chat":      chatUp,
				"responses": responsesUp,
				"anthropic": anthropicUp,
				"gemini":    geminiUp,
			}
			for api, up := range upstreams {
				want := int32(0)
				if api == tt.wantAPI {
					want = 1
				}
				if got := up.hits.Load(); got != want {
					t.Errorf("%s upstream hits = %d, want %d", api, got, want)
				}
			}

			gotPath, _ := upstreams[tt.wantAPI].path.Load().(string)
			if gotPath != tt.wantPath {
				t.Fatalf("probe path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func TestStatsAPI(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	s.mu.Lock()
	s.cfg.Entrypoints = []config.Entrypoint{{Name: "claude", Match: []string{"claude-*"}, Route: "r"}}
	s.mu.Unlock()
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	for _, row := range []stats.Request{
		{ID: "r1", SID: "s1", TS: base, Provider: "p", Model: "m", ClientModel: "claude-opus", Node: "n1", Route: "r", Effort: "high", Status: 200, DurationMS: 10, InputTokens: 1, OutputTokens: 2},
		{ID: "r2", SID: "s2", TS: base.Add(time.Minute), Provider: "p", Model: "m", ClientModel: "gpt-4.5", Node: "n2", Route: "r", Effort: "low", Status: 500, Error: "boom", DurationMS: 30, InputTokens: 3, OutputTokens: 4},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	sumResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/stats/summary", "", "")
	var sum stats.Summary
	if err := jsonDecode(sumResp.Body, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 2 || sum.Errors != 1 || sum.P50MS == 0 {
		t.Fatalf("summary = %+v", sum)
	}

	rowsResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?provider=p&status=500", "", "")
	var rows []stats.Request
	if err := jsonDecode(rowsResp.Body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "r2" {
		t.Fatalf("rows = %+v", rows)
	}

	nodeResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?node=n1", "", "")
	var nodeRows []stats.Request
	if err := jsonDecode(nodeResp.Body, &nodeRows); err != nil {
		t.Fatal(err)
	}
	if len(nodeRows) != 1 || nodeRows[0].ID != "r1" {
		t.Fatalf("node rows = %+v", nodeRows)
	}

	epResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?entrypoint=claude", "", "")
	var epRows []stats.Request
	if err := jsonDecode(epResp.Body, &epRows); err != nil {
		t.Fatal(err)
	}
	if len(epRows) != 1 || epRows[0].ID != "r1" {
		t.Fatalf("entrypoint rows = %+v", epRows)
	}

	missingResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?entrypoint=missing", "", "")
	var missingRows []stats.Request
	if err := jsonDecode(missingResp.Body, &missingRows); err != nil {
		t.Fatal(err)
	}
	if len(missingRows) != 0 {
		t.Fatalf("missing entrypoint rows = %+v", missingRows)
	}

	oneResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests/r1", "", "")
	var one stats.Request
	if err := jsonDecode(oneResp.Body, &one); err != nil {
		t.Fatal(err)
	}
	if one.ID != "r1" || one.InputTokens != 1 {
		t.Fatalf("request = %+v", one)
	}
}

func TestWebServesSPAFallback(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET / = %d, want 200 or 503", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK {
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(string(body), "<html") {
			t.Fatalf("body = %q", body)
		}
		if resp2, err := http.Get(ts.URL + "/providers"); err != nil || resp2.StatusCode != http.StatusOK {
			t.Fatalf("SPA fallback = %v, want 200", err)
		}
	}

	if resp, err := http.Get(ts.URL + "/api/does-not-exist"); err != nil ||
		(resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized) {
		t.Fatalf("unknown api = %v, want 401/404", err)
	}
}

func newSessionTestServer(t *testing.T, store *stats.Store, hash string) *httptest.Server {
	t.Helper()
	cfg := &config.Config{}
	cfg.Admin.PasswordHash = hash
	cfg.Admin.SessionTTL = config.Duration(time.Hour)
	config.ApplyDefaults(cfg)
	s := New("", cfg, &recordingReloader{store: store}, xlogger.Nop())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestRememberMeSurvivesRestart(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	hash, err := config.HashPassword("pass")
	if err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	first := newSessionTestServer(t, store, hash)
	resp := doJSON(t, client, http.MethodPost, first.URL+"/api/login", `{"password":"pass","remember":true}`, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	// The jar drops cookie attributes; inspect the Set-Cookie header instead.
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.MaxAge <= 0 {
			t.Fatalf("remembered session cookie MaxAge = %d, want > 0", ck.MaxAge)
		}
	}

	// A fresh Server instance has an empty in-memory map; the session must be
	// restored from SQLite.
	second := newSessionTestServer(t, store, hash)
	if resp := doJSON(t, client, http.MethodGet, second.URL+"/api/config", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("restored session status = %d, want 200", resp.StatusCode)
	}
}

func TestSessionWithoutRememberIsNotPersisted(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	hash, _ := config.HashPassword("pass")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	first := newSessionTestServer(t, store, hash)
	if resp := doJSON(t, client, http.MethodPost, first.URL+"/api/login", `{"password":"pass"}`, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	second := newSessionTestServer(t, store, hash)
	if resp := doJSON(t, client, http.MethodGet, second.URL+"/api/config", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after restart = %d, want 401", resp.StatusCode)
	}
}

func TestLogoutDeletesPersistedSession(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	hash, _ := config.HashPassword("pass")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	first := newSessionTestServer(t, store, hash)
	if resp := doJSON(t, client, http.MethodPost, first.URL+"/api/login", `{"password":"pass","remember":true}`, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	u, _ := url.Parse(first.URL)
	var csrf string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == csrfCookie {
			csrf = ck.Value
		}
	}
	if resp := doJSON(t, client, http.MethodPost, first.URL+"/api/logout", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}

	second := newSessionTestServer(t, store, hash)
	if resp := doJSON(t, &http.Client{}, http.MethodGet, second.URL+"/api/config", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
	}
	saved, err := store.LoadSessions(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("persisted sessions = %d, want 0", len(saved))
	}
}

func TestProviderTypeAndURLsImmutable(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	const (
		chat      = "https://api.deepseek.com/v1"
		responses = "https://api.deepseek.com/v1"
		anthropic = "https://api.deepseek.com/anthropic"
		gemini    = "https://generativelanguage.googleapis.com/v1beta"
	)
	body := `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	pid := providerID(t, client, ts.URL, "p")
	providerPath := providerURL(ts.URL, pid)
	calls := rel.calls

	same := `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, same, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unchanged update = %d: %s", resp.StatusCode, b)
	}

	checks := []struct {
		name string
		body string
	}{
		{
			name: "type",
			body: `{"name":"p","type":"opencode-go","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`,
		},
		{
			name: "chat url",
			body: `{"name":"p","type":"deepseek","chat":"https://example.com/chat","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`,
		},
		{
			name: "responses url",
			body: `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"https://example.com/responses","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`,
		},
		{
			name: "anthropic url",
			body: `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"` + responses + `","anthropic":"https://example.com/anthropic","gemini":"` + gemini + `"}`,
		},
		{
			name: "gemini url",
			body: `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"https://example.com/gemini"}`,
		},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			if resp := doJSON(t, client, http.MethodPut, providerPath, tt.body, csrf); resp.StatusCode != http.StatusBadRequest {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("update with changed %s = %d: %s, want 400", tt.name, resp.StatusCode, b)
			}
		})
	}

	cfg := `{"providers":[{"name":"p","type":"opencode-go","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}]}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", cfg, csrf); resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /api/config with changed type = %d: %s, want 400", resp.StatusCode, b)
	}

	if rel.calls != calls+1 {
		t.Fatalf("reload calls = %d, want %d (rejected updates must not reload)", rel.calls, calls+1)
	}
	if got := rel.cfg.Providers[0]; got.Type != "deepseek" || got.Chat != chat || got.Responses != responses || got.Anthropic != anthropic || got.Gemini != gemini {
		t.Fatalf("provider changed after rejected updates: %+v", got)
	}
}

func TestGenericProviderURLsRemainEditable(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	body := `{"name":"p","type":"generic","chat":"https://api.example.com/v1"}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	providerPath := providerURL(ts.URL, providerID(t, client, ts.URL, "p"))

	changed := `{"name":"p","type":"generic","chat":"https://api.example.com/v2"}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, changed, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("generic url update = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers[0].Chat; got != "https://api.example.com/v2" {
		t.Fatalf("chat = %q, want the edited generic URL", got)
	}

	retype := `{"name":"p","type":"deepseek","chat":"https://api.example.com/v2"}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, retype, csrf); resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("generic type change = %d: %s, want 400", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers[0].Type; got != "generic" {
		t.Fatalf("type = %q, want generic after the rejected change", got)
	}
}

func TestProviderImmutabilityIgnoresTypeCaseAndSpace(t *testing.T) {
	old := &config.Config{Providers: []config.Provider{
		{Name: "p", Type: "deepseek", Chat: "https://api.deepseek.com/v1"},
	}}
	next := &config.Config{Providers: []config.Provider{
		{Name: "p", Type: " DeepSeek ", Chat: " https://api.deepseek.com/v1 "},
	}}
	if err := validateProviderImmutability(old, next); err != nil {
		t.Fatalf("case/space-only change rejected: %v", err)
	}

	next.Providers[0].Chat = "https://api.deepseek.com/v2"
	if err := validateProviderImmutability(old, next); err == nil {
		t.Fatal("changed base URL must be rejected for a non-generic provider")
	}
}

func TestEmptyListsReturnArrays(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	for _, path := range []string{"/api/requests", "/api/routes", "/api/entrypoints", "/api/nodes"} {
		resp := doJSON(t, client, http.MethodGet, ts.URL+path, "", "")
		b, _ := io.ReadAll(resp.Body)
		if got := strings.TrimSpace(string(b)); got != "[]" {
			t.Fatalf("%s = %s, want []", path, got)
		}
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	var v map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"entrypoints", "providers", "nodes", "routes"} {
		if got := string(v[key]); got != "[]" {
			t.Fatalf("config.%s = %s, want []", key, got)
		}
	}
}

func TestProviderKeyUpdates(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	body := `{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"key-1","key":"a"},{"name":"key-2","key":"b"},{"name":"key-3","key":"c"}]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider = %d", resp.StatusCode)
	}
	pid := providerID(t, client, ts.URL, "p")
	providerPath := providerURL(ts.URL, pid)
	keyIDs := make(map[string]string, 3)
	for _, k := range getProvider(t, client, ts.URL, pid).Keys {
		keyIDs[k.Name] = k.ID
	}

	update := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":[{"id":"` + keyIDs["key-3"] + `","delete":true},{"name":"key-4","key":"d"}]}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, update, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := readAll(resp.Body)
		t.Fatalf("update keys = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 3 || got[0].Key != "a" || got[1].Key != "b" || got[2].Key != "d" || got[2].Name != "key-4" {
		t.Fatalf("keys = %v, want [a b d] named key-4", got)
	}
	if got[0].ID != keyIDs["key-1"] || got[1].ID != keyIDs["key-2"] || got[2].ID == "" {
		t.Fatalf("keys = %+v, want the stored ids kept and a generated one for key-4", got)
	}

	// A name without an id is a new key, so it collides with the existing
	// name instead of replacing its value.
	replace := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":[{"name":"key-1","key":"x"}]}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, replace, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate name = %d, want 400", resp.StatusCode)
	}
	if got := rel.cfg.Providers[0].Keys; len(got) != 3 || got[0].Key != "a" {
		t.Fatalf("keys changed after rejected replace: %v", got)
	}

	bad := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":[{"id":"pk_0000000000","delete":true}]}`
	if resp := doJSON(t, client, http.MethodPut, providerPath, bad, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown key id = %d, want 400", resp.StatusCode)
	}
	if got := rel.cfg.Providers[0].Keys; len(got) != 3 || got[0].Key != "a" {
		t.Fatalf("keys changed after rejected update: %v", got)
	}
}

func TestPutStatsCapture(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	provider := `{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"key-1","key":"sk-keep-me-1234"}],"userAgent":"custom/1.0","headers":{"X-Keep":"1"}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", provider, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes", `{"name":"n","provider":"p","model":"m1"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/routes", `{"name":"fast","nodes":["n"]}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create route status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/entrypoints", `[{"name":"default","match":["*"],"route":"fast"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints status = %d", resp.StatusCode)
	}

	body := `{"mode":"errors","sampleRate":0.5,"maxBytes":65536,"retain":"12h"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/stats/capture", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put capture status = %d", resp.StatusCode)
	}
	c := rel.cfg.Stats.Capture
	if c.Mode != "errors" || c.SampleRate == nil || *c.SampleRate != 0.5 ||
		c.MaxBytes != 65536 || c.Retain != config.Duration(12*time.Hour) {
		t.Fatalf("capture = %+v", c)
	}

	if len(rel.cfg.Providers) != 1 || len(rel.cfg.Nodes) != 1 ||
		len(rel.cfg.Routes) != 1 || len(rel.cfg.Entrypoints) != 1 {
		t.Fatalf("capture PUT changed other sections: %+v", rel.cfg)
	}
	p := rel.cfg.Providers[0]
	if len(p.Keys) != 1 || p.Keys[0].Key != "sk-keep-me-1234" ||
		p.UserAgent != "custom/1.0" || p.Headers["X-Keep"] != "1" {
		t.Fatalf("provider fields changed: %+v", p)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	var view configView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Stats.Capture.Mode != "errors" || view.Stats.Capture.MaxBytes != 65536 {
		t.Fatalf("view capture = %+v", view.Stats.Capture)
	}

	for _, bad := range []string{
		`{"mode":"always","maxBytes":65536,"retain":"12h"}`,
		`{"mode":"errors","maxBytes":1,"retain":"12h"}`,
		`{"mode":"errors","maxBytes":1048577,"retain":"12h"}`,
	} {
		if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/stats/capture", bad, csrf); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d for %s", resp.StatusCode, bad)
		}
	}

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/stats/capture", `{"maxBytes":65536}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("partial put status = %d", resp.StatusCode)
	}
	c = rel.cfg.Stats.Capture
	if c.Mode != "off" || c.SampleRate == nil || *c.SampleRate != 0.01 ||
		c.Retain != config.Duration(24*time.Hour) {
		t.Fatalf("partial capture = %+v", c)
	}
}
