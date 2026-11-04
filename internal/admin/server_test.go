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

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/stats"
)

type recordingReloader struct {
	cfg   *config.Config
	err   error
	calls int
	store *stats.Store
}

func (r *recordingReloader) Stats() *stats.Store { return r.store }

func (r *recordingReloader) Reload(c *config.Config) error {
	if r.err != nil {
		return r.err
	}
	r.cfg = c
	r.calls++
	return nil
}

func newTestAdmin(t *testing.T) (*Server, *recordingReloader, string, *httptest.Server) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "llm-gw.yaml")
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
	rel := &recordingReloader{store: store}
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
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/aliases", `[{"match":"*","model":"fast"}]`, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", resp.StatusCode)
	}
	if rel.calls != 0 {
		t.Fatal("rejected request must not reload")
	}

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/aliases", `[{"match":"*","model":"fast"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put aliases status = %d", resp.StatusCode)
	}
	if len(rel.cfg.Aliases) != 1 || rel.cfg.Aliases[0].Model != "fast" {
		t.Fatalf("aliases not applied: %+v", rel.cfg.Aliases)
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

	body := `{"name":"p1","type":"generic","chat":"https://api.example.com/v1","keys":["sk-secret-value"]}`
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
	if !strings.Contains(string(b), `"keysSet":1`) || !strings.Contains(string(b), "sk-...alue") {
		t.Fatalf("missing key metadata: %s", b)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v mode=%v", err, info.Mode().Perm())
	}

	update := `{"name":"p1","type":"generic","chat":"https://api.example.com/v1"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p1", update, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("update status = %d", resp.StatusCode)
	}
	if len(rel.cfg.Providers[0].Keys) != 1 || rel.cfg.Providers[0].Keys[0] != "sk-secret-value" {
		t.Fatalf("keys were cleared by an update without keys: %+v", rel.cfg.Providers[0].Keys)
	}

	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes", `{"name":"n1","provider":"p1"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/routes", `{"name":"r1","nodes":["n1"]}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create route status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/p1", "", csrf); resp.StatusCode != http.StatusBadRequest {
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
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/p1", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete provider status = %d", resp.StatusCode)
	}
}

func TestReloadFailureKeepsFileAndMemory(t *testing.T) {
	s, rel, path, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	rel.err = errors.New("boom")
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/aliases", `[{"match":"*","model":"fast"}]`, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file should not exist after a failed reload: %v", err)
	}
	if len(s.cfg.Aliases) != 0 {
		t.Fatalf("in-memory config changed after a failed reload: %+v", s.cfg.Aliases)
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
	if len(got) != 4 {
		t.Fatalf("presets = %d, want one per endpoint (4)", len(got))
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
		{"opencode-go", "opencode-go", "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go", ""},
		{"opencode-zen", "opencode-zen", "https://opencode.ai/zen/v1", "https://opencode.ai/zen/v1", "https://opencode.ai/zen", ""},
		{"commandcode", "commandcode", "https://api.commandcode.ai/provider/v1", "https://api.commandcode.ai/provider/v1", "https://api.commandcode.ai/provider", ""},
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

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers/local/test", "", csrf)
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
		Keys: []string{"invalid-key"},
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

	body := `{"name":"anthropic","type":"generic","anthropic":"` + up.URL + `","keys":["sk-ant"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	s.ProbeOnce(context.Background())

	s.mu.Lock()
	res, ok := s.probes["anthropic"]
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
	_, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-time.Hour)
	for _, row := range []stats.Request{
		{ID: "r1", SID: "s1", TS: base, Provider: "p", Model: "m", Effort: "high", Status: 200, DurationMS: 10, InputTokens: 1, OutputTokens: 2},
		{ID: "r2", SID: "s2", TS: base.Add(time.Minute), Provider: "p", Model: "m", Effort: "low", Status: 500, Error: "boom", DurationMS: 30, InputTokens: 3, OutputTokens: 4},
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
	calls := rel.calls

	same := `{"name":"p","type":"deepseek","chat":"` + chat + `","responses":"` + responses + `","anthropic":"` + anthropic + `","gemini":"` + gemini + `"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", same, csrf); resp.StatusCode != http.StatusNoContent {
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
			if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", tt.body, csrf); resp.StatusCode != http.StatusBadRequest {
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

	changed := `{"name":"p","type":"generic","chat":"https://api.example.com/v2"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", changed, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("generic url update = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Providers[0].Chat; got != "https://api.example.com/v2" {
		t.Fatalf("chat = %q, want the edited generic URL", got)
	}

	retype := `{"name":"p","type":"deepseek","chat":"https://api.example.com/v2"}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", retype, csrf); resp.StatusCode != http.StatusBadRequest {
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
