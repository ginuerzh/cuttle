package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

// newModelsUpstream serves an OpenAI-style model list and records how many
// times it was called. A non-nil fail flag makes it return 502.
func newModelsUpstream(t *testing.T, hits *atomic.Int32, fail *atomic.Bool, path, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if fail != nil && fail.Load() {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		if r.URL.Path != path {
			t.Errorf("upstream path = %q, want %q", r.URL.Path, path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readProviderModelsResponse(t *testing.T, client *http.Client, u string) ([]provider.Model, http.Header) {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, u, "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d: %s", u, resp.StatusCode, b)
	}
	var models []provider.Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatal(err)
	}
	return models, resp.Header
}

func readProviderModels(t *testing.T, client *http.Client, u string) []provider.Model {
	t.Helper()

	models, _ := readProviderModelsResponse(t, client, u)
	return models
}

func TestProviderModelsCachesAndRefreshes(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model","name":"Chat Model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	models := readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if len(models) != 1 || models[0].ID != "chat-model" || models[0].Name != "Chat Model" {
		t.Fatalf("models = %+v", models)
	}
	if !slices.Equal(models[0].APIs, []provider.API{provider.Chat}) {
		t.Fatalf("apis = %v, want [chat]", models[0].APIs)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 after the first call", got)
	}

	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models"); len(got) != 1 || got[0].ID != "chat-model" {
		t.Fatalf("cached models = %+v", got)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 within the cache TTL", got)
	}

	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models?refresh=1"); len(got) != 1 || got[0].ID != "chat-model" {
		t.Fatalf("refreshed models = %+v", got)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after refresh=1", got)
	}
}

func TestProviderModelsConcurrentRefreshSingleflight(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"chat-model"}]}`))
	}))
	t.Cleanup(up.Close)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	const callers = 6
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			resp, err := client.Get(ts.URL + "/api/providers/p/models?refresh=1")
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				errs <- fmt.Errorf("status %d: %s", resp.StatusCode, b)
				return
			}
			var models []provider.Model
			if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
				errs <- err
				return
			}
			if len(models) != 1 || models[0].ID != "chat-model" {
				errs <- fmt.Errorf("models = %+v", models)
			}
		}()
	}
	close(start)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		releaseOnce.Do(func() { close(release) })
		wg.Wait()
		t.Fatal("upstream fetch did not start")
	}

	// The leader is blocked in the upstream handler, so every other refresh
	// should join its in-flight fetch instead of calling upstream again.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		flight := s.modelsInflight["p"]
		waiters := 0
		if flight != nil {
			waiters = flight.waiters
		}
		s.mu.Unlock()

		if waiters == callers-1 {
			break
		}
		if time.Now().After(deadline) {
			releaseOnce.Do(func() { close(release) })
			wg.Wait()
			t.Fatalf("inflight waiters = %d, want %d", waiters, callers-1)
		}
		time.Sleep(time.Millisecond)
	}

	releaseOnce.Do(func() { close(release) })
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 coalesced fetch for %d concurrent refreshes", got, callers)
	}
}

func TestProviderModelsKeepLastGoodOnRefreshFailure(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	var fail atomic.Bool
	up := newModelsUpstream(t, &hits, &fail, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	first := readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if len(first) != 1 || first[0].ID != "chat-model" {
		t.Fatalf("models = %+v", first)
	}

	fail.Store(true)
	lastGood := readProviderModels(t, client, ts.URL+"/api/providers/p/models?refresh=1")
	if len(lastGood) != 1 || lastGood[0].ID != "chat-model" {
		t.Fatalf("last-good models = %+v", lastGood)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 (initial + failed refresh)", got)
	}
}

func TestProviderModelsLastGoodKeepsFetchedAtHeader(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	var fail atomic.Bool
	up := newModelsUpstream(t, &hits, &fail, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	first, header := readProviderModelsResponse(t, client, ts.URL+"/api/providers/p/models")
	if len(first) != 1 || first[0].ID != "chat-model" {
		t.Fatalf("models = %+v", first)
	}
	fetchedAt := header.Get("X-Models-Fetched-At")
	if fetchedAt == "" {
		t.Fatal("missing X-Models-Fetched-At header on the first fetch")
	}

	fail.Store(true)
	lastGood, header := readProviderModelsResponse(t, client, ts.URL+"/api/providers/p/models?refresh=1")
	if len(lastGood) != 1 || lastGood[0].ID != "chat-model" {
		t.Fatalf("last-good models = %+v", lastGood)
	}
	if got := header.Get("X-Models-Fetched-At"); got != fetchedAt {
		t.Fatalf("X-Models-Fetched-At after a failed refresh = %q, want the last success %q", got, fetchedAt)
	}
}

func TestProviderModelsInvalidatedByProviderUpdate(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}

	update := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","health":{"timeout":"2s"}}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", update, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update provider = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after the provider update cleared the cache", got)
	}
}

func TestProviderModelsInvalidatedByConfigPut(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}

	// Replace the whole config through PUT /api/config while changing the
	// provider's health timeout; the cached catalog must be dropped.
	cfg := `{"providers":[{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"],"health":{"timeout":"2s"}}]}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", cfg, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put config = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after PUT /api/config cleared the cache", got)
	}
}

func TestProviderModelsInvalidatedByProviderDelete(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model"}]}`)
	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`

	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}

	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/providers/p", "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete provider = %d: %s", resp.StatusCode, b)
	}
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("recreate provider = %d: %s", resp.StatusCode, b)
	}

	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after delete/recreate cleared the cache", got)
	}
}

func TestProviderModelsCacheExpires(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}

	s.mu.Lock()
	entry := s.modelsCache["p"]
	entry.fetchedAt = time.Now().Add(-modelsCacheTTL - time.Second)
	s.modelsCache["p"] = entry
	s.mu.Unlock()

	_ = readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 after the cache TTL expired", got)
	}
}

func TestProviderModelsUpstreamError(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var fail atomic.Bool
	fail.Store(true)
	up := newModelsUpstream(t, nil, &fail, "/v1/models", `{"data":[]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1"}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/providers/p/models", "", "")
	if resp.StatusCode != http.StatusBadGateway {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("models without last-good = %d: %s, want 502", resp.StatusCode, b)
	}
}

func TestProviderModelsMergeDisabledOverrides(t *testing.T) {
	s, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model","name":"Chat Model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"],"models":{"chat-model":{"disabled":true}}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	models := readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if len(models) != 1 || models[0].ID != "chat-model" || !models[0].Disabled {
		t.Fatalf("models = %+v, want chat-model with Disabled=true", models)
	}

	// The cache must keep the raw upstream catalog; the overlay belongs to
	// the response only.
	s.mu.Lock()
	cached := s.modelsCache["p"].models
	s.mu.Unlock()
	if len(cached) != 1 || cached[0].Disabled {
		t.Fatalf("cached catalog was mutated by the overlay: %+v", cached)
	}

	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models?refresh=1"); len(got) != 1 || !got[0].Disabled {
		t.Fatalf("refreshed models = %+v, want chat-model with Disabled=true", got)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 (initial + refresh)", got)
	}
}

func TestProviderModelsShowDisabledOverridesMissingFromCatalog(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"current-model","name":"Current Model"}]}`)

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"],"models":{" removed-model ":{"disabled":true},"z-missing":{"disabled":true},"a-missing":{"disabled":true}}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	models := readProviderModels(t, client, ts.URL+"/api/providers/p/models")
	if len(models) != 4 {
		t.Fatalf("models = %+v, want the catalog entry plus three disabled overrides", models)
	}
	// The upstream catalog entry must be returned unchanged.
	if models[0].ID != "current-model" || models[0].Name != "Current Model" || models[0].Disabled {
		t.Fatalf("catalog entry changed: %+v", models[0])
	}
	// Overrides absent from the catalog are appended as synthetic disabled
	// entries, sorted by ID for deterministic responses.
	var missing []string
	for _, m := range models[1:] {
		if !m.Disabled {
			t.Fatalf("synthetic override not disabled: %+v", m)
		}
		missing = append(missing, m.ID)
	}
	if want := []string{"a-missing", "removed-model", "z-missing"}; !slices.Equal(missing, want) {
		t.Fatalf("missing overrides = %v, want %v", missing, want)
	}
}

func TestApplyModelOverridesAppendsMissingDisabledModels(t *testing.T) {
	catalog := []providerModel{
		{ID: "current-model", Name: "Current Model"},
		{ID: " disabled-in-catalog ", Name: "Spaced"},
	}
	overrides := map[string]config.ModelOverride{
		" current-model ":      {Disabled: true},
		" disabled-in-catalog": {Disabled: true},
		" z-removed ":          {Disabled: true},
		"a-removed":            {Disabled: true},
		"enabled":              {Disabled: false},
		"   ":                  {Disabled: true},
	}
	got := applyModelOverrides(catalog, overrides)
	if len(got) != 4 {
		t.Fatalf("models = %+v, want two catalog entries plus two synthetic ones", got)
	}
	if got[0].ID != "current-model" || !got[0].Disabled || got[0].Name != "Current Model" {
		t.Fatalf("catalog entry not overridden: %+v", got[0])
	}
	if got[1].ID != " disabled-in-catalog " || !got[1].Disabled {
		t.Fatalf("spaced catalog entry not overridden: %+v", got[1])
	}
	if got[2].ID != "a-removed" || !got[2].Disabled || got[3].ID != "z-removed" || !got[3].Disabled {
		t.Fatalf("synthetic entries = %+v, want sorted a-removed, z-removed", got[2:])
	}
	if catalog[0].Disabled || catalog[1].Disabled {
		t.Fatalf("cached catalog was mutated: %+v", catalog)
	}
}

func TestProviderViewReturnsModelOverrides(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	body := `{"name":"p","type":"generic","chat":"https://api.example.com/v1","models":{"chat-model":{"disabled":true}}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/providers/p", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("get provider = %d: %s", resp.StatusCode, b)
	}
	var view map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	raw, ok := view["models"]
	if !ok {
		t.Fatalf("provider view has no models field: %v", view)
	}
	var overrides map[string]config.ModelOverride
	if err := json.Unmarshal(raw, &overrides); err != nil {
		t.Fatal(err)
	}
	if ov, ok := overrides["chat-model"]; !ok || !ov.Disabled {
		t.Fatalf("models = %+v, want chat-model disabled", overrides)
	}
}

func TestProviderModelsKeepCacheOnModelOverrideChange(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	var hits atomic.Int32
	up := newModelsUpstream(t, &hits, nil, "/v1/models", `{"data":[{"id":"chat-model"}]}`)

	base := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", base, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models"); len(got) != 1 || got[0].Disabled {
		t.Fatalf("initial models = %+v", got)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}

	disable := `{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"],"models":{"chat-model":{"disabled":true}}}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/providers/p", disable, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("disable model = %d: %s", resp.StatusCode, b)
	}
	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models"); len(got) != 1 || !got[0].Disabled {
		t.Fatalf("models after disable = %+v", got)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 after a model-only update cleared the cache", got)
	}

	// Enabling again through the raw config endpoint must also reuse the
	// cached catalog.
	enable := `{"providers":[{"name":"p","type":"generic","chat":"` + up.URL + `/v1","keys":["sk-test"]}]}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", enable, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put config = %d: %s", resp.StatusCode, b)
	}
	if got := readProviderModels(t, client, ts.URL+"/api/providers/p/models"); len(got) != 1 || got[0].Disabled {
		t.Fatalf("models after enable = %+v", got)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 after re-enabling a model", got)
	}
}
