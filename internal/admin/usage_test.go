package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func setUsageProviders(t *testing.T, srv *Server, providers ...config.Provider) {
	t.Helper()
	srv.mu.Lock()
	srv.cfg.Providers = providers
	srv.mu.Unlock()
}

func decodeUsage(t *testing.T, resp *http.Response) (providerUsageResponse, string) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got providerUsageResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return got, string(body)
}

func TestProviderUsageAPI(t *testing.T) {
	srv, rel, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"11.12","granted_balance":"1","topped_up_balance":"10.12"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-secret-1234"},
	})
	if err := rel.store.Insert(context.Background(), stats.Request{
		ID: "r1", SID: "r1", TS: time.Now().UTC(), Provider: "deepseek",
		Status: 200, InputTokens: 10, OutputTokens: 2, CachedTokens: 3,
	}); err != nil {
		t.Fatal(err)
	}

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got, raw := decodeUsage(t, resp)
	if got.Totals == nil || got.Totals.Requests != 1 || got.Totals.CachedTokens != 3 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %+v", got.Providers)
	}
	p := got.Providers[0]
	if p.Local == nil || p.Local.CachedTokens != 3 {
		t.Fatalf("local = %+v", p.Local)
	}
	if !p.Supported || !p.Enabled || len(p.Accounts) != 1 {
		t.Fatalf("provider = %+v", p)
	}
	a := p.Accounts[0]
	if a.KeyPreview != "sk-...1234" || len(a.Balance) != 1 || a.Balance[0].Total != 11.12 {
		t.Fatalf("account = %+v", a)
	}
	if strings.Contains(raw, "sk-secret-1234") {
		t.Fatalf("response leaked key: %s", raw)
	}
}

func TestProviderUsageCache(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-test"},
	})

	client, _ := login(t, ts.URL)
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("cached status = %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestProviderUsageMultipleKeys(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total := "1"
		if r.Header.Get("Authorization") == "Bearer sk-secret-2222" {
			total = "2"
		}
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"` + total + `"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []string{"sk-secret-1111", "sk-secret-2222"},
	})

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", "")
	got, raw := decodeUsage(t, resp)
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 2 {
		t.Fatalf("providers = %+v", got.Providers)
	}
	a := got.Providers[0].Accounts
	if a[0].KeyPreview != "sk-...1111" || a[1].KeyPreview != "sk-...2222" {
		t.Fatalf("previews = %q/%q", a[0].KeyPreview, a[1].KeyPreview)
	}
	if a[0].Balance[0].Total != 1 || a[1].Balance[0].Total != 2 {
		t.Fatalf("balances = %+v/%+v", a[0].Balance, a[1].Balance)
	}
	if strings.Contains(raw, "sk-secret-1111") || strings.Contains(raw, "sk-secret-2222") {
		t.Fatalf("response leaked a key: %s", raw)
	}
}

func TestProviderUsageFailureKeepsLastGood(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	var fail atomic.Bool
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("bad sk-secret-1234"))
			return
		}
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"11.12"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-secret-1234"},
	})

	client, _ := login(t, ts.URL)
	first, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	lastGood := first.Providers[0].Accounts[0].CheckedAt
	if lastGood == "" {
		t.Fatal("missing first checkedAt")
	}

	fail.Store(true)
	second, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	a := second.Providers[0].Accounts[0]
	if len(a.Balance) != 1 || a.Balance[0].Total != 11.12 {
		t.Fatalf("stale balance = %+v", a)
	}
	if !strings.Contains(a.Error, "500") || !a.Stale || a.CheckedAt != lastGood {
		t.Fatalf("stale account = %+v", a)
	}

	third, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""))
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if third.Providers[0].Accounts[0].Error == "" || !third.Providers[0].Cached {
		t.Fatalf("cached failure = %+v", third.Providers[0])
	}
}

func TestProviderUsageDisabledAndNoKey(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	disabled := false
	setUsageProviders(t, srv,
		config.Provider{
			Name: "off", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-test"},
			Enabled: &disabled,
		},
		config.Provider{
			Name: "nokey", Type: "deepseek", Chat: vendor.URL + "/v1",
		},
	)

	client, _ := login(t, ts.URL)
	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if calls.Load() != 0 {
		t.Fatalf("calls = %d, want 0", calls.Load())
	}
	if got.Providers[0].Enabled || got.Providers[0].Note != "provider is disabled" ||
		len(got.Providers[0].Accounts) != 0 || got.Providers[0].CheckedAt != "" {
		t.Fatalf("disabled = %+v", got.Providers[0])
	}
	if got.Providers[1].Note != "no API key configured" ||
		len(got.Providers[1].Accounts) != 0 || got.Providers[1].CheckedAt != "" {
		t.Fatalf("nokey = %+v", got.Providers[1])
	}
}

func TestProviderUsageUnsupported(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "zen", Type: "opencode-zen", Chat: vendor.URL + "/zen/v1", Keys: []string{"sk-test"},
	})

	client, _ := login(t, ts.URL)
	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	p := got.Providers[0]
	if p.Supported || !strings.Contains(p.Note, "https://opencode.ai/console") || len(p.Accounts) != 0 {
		t.Fatalf("provider = %+v", p)
	}
	if calls.Load() != 0 {
		t.Fatalf("calls = %d, want 0", calls.Load())
	}
}

func TestProviderUsageUnauthenticated(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	resp := doJSON(t, &http.Client{}, http.MethodGet, ts.URL+"/api/provider-usage", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestProviderUsageReloadClearsCache(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-test"},
	})

	client, _ := login(t, ts.URL)
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", resp.StatusCode)
	}

	srv.mu.Lock()
	next := *srv.cfg
	next.Providers = append([]config.Provider(nil), srv.cfg.Providers...)
	srv.mu.Unlock()
	srv.SetConfig(&next)

	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("after reload status = %d", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 after cache invalidation", calls.Load())
	}
}

func TestProviderUsageLocalError(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-test"},
	})
	srv.stats = nil

	client, _ := login(t, ts.URL)
	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if got.LocalError == "" {
		t.Fatal("localError is empty")
	}
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 1 ||
		len(got.Providers[0].Accounts[0].Balance) != 1 {
		t.Fatalf("providers = %+v", got.Providers)
	}
}

func TestProviderUsageCacheInvalidationRace(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer sk-old-1111" {
			once.Do(func() { close(started) })
			<-release
			w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"111"}]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("new key failed"))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-old-1111"},
	})
	client, _ := login(t, ts.URL)

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := client.Get(ts.URL + "/api/provider-usage?refresh=1")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started

	srv.mu.Lock()
	next := *srv.cfg
	next.Providers = []config.Provider{{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-new-2222"},
	}}
	srv.mu.Unlock()
	srv.SetConfig(&next)
	close(release)
	<-done

	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 1 {
		t.Fatalf("providers = %+v", got.Providers)
	}
	a := got.Providers[0].Accounts[0]
	if a.KeyPreview != "sk-...2222" {
		t.Fatalf("preview = %q", a.KeyPreview)
	}
	if len(a.Balance) != 0 {
		t.Fatalf("old key's balance leaked to the new key: %+v", a)
	}
	if !strings.Contains(a.Error, "500") {
		t.Fatalf("error = %q", a.Error)
	}
}

func TestProviderUsageCanceledRequestNotCached(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Keys: []string{"sk-test"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/provider-usage?refresh=1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleProviderUsage(rec, req)
	}()
	<-started
	cancel()
	close(release)
	<-done

	client, _ := login(t, ts.URL)
	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""))
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2: canceled request was cached", calls.Load())
	}
	a := got.Providers[0].Accounts[0]
	if a.Error != "" || len(a.Balance) != 1 || a.Balance[0].Total != 1 {
		t.Fatalf("account = %+v", a)
	}
}

func TestProviderUsageOpenRouterNote(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/key" {
			t.Errorf("path = %q, want /api/v1/key", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"limit":null,"limit_remaining":null,"usage":25.5}}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "openrouter", Type: "openrouter", Chat: vendor.URL + "/api/v1", Keys: []string{"sk-or-secret"},
	})

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", "")
	got, raw := decodeUsage(t, resp)
	if len(got.Providers) != 1 || !got.Providers[0].Supported || len(got.Providers[0].Accounts) != 1 {
		t.Fatalf("providers = %+v", got.Providers)
	}
	if !strings.Contains(raw, `"note"`) || !strings.Contains(raw, "unlimited key") || !strings.Contains(raw, "$25.50") {
		t.Fatalf("usage response has no note: %s", raw)
	}
	if strings.Contains(raw, "sk-or-secret") {
		t.Fatalf("response leaked key: %s", raw)
	}
}

func TestProviderUsageOpenRouterStaleNote(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var fail atomic.Bool
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"data":{"limit":null,"limit_remaining":null,"usage":25.5}}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "openrouter", Type: "openrouter", Chat: vendor.URL + "/api/v1", Keys: []string{"sk-or-secret"},
	})

	client, _ := login(t, ts.URL)
	first, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if len(first.Providers) != 1 || len(first.Providers[0].Accounts) != 1 ||
		!strings.Contains(first.Providers[0].Accounts[0].Note, "$25.50") {
		t.Fatalf("first usage = %+v", first.Providers)
	}

	fail.Store(true)
	second, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if len(second.Providers) != 1 || len(second.Providers[0].Accounts) != 1 {
		t.Fatalf("second usage = %+v", second.Providers)
	}
	acc := second.Providers[0].Accounts[0]
	if !acc.Stale || !strings.Contains(acc.Note, "$25.50") {
		t.Fatalf("stale account = %+v, want the previous note retained", acc)
	}
}

func TestProviderUsageUnsupportedKeyless(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	var calls atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer vendor.Close()
	disabled := false
	setUsageProviders(t, srv,
		config.Provider{Name: "zen", Type: "opencode-zen", Chat: vendor.URL + "/zen/v1"},
		config.Provider{Name: "generic", Type: "generic", Chat: vendor.URL + "/v1"},
		config.Provider{Name: "off-zen", Type: "opencode-zen", Chat: vendor.URL + "/zen/v1", Enabled: &disabled},
	)

	client, _ := login(t, ts.URL)
	got, _ := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", ""))
	if calls.Load() != 0 {
		t.Fatalf("calls = %d, want 0", calls.Load())
	}
	for i, wantNote := range []string{"https://opencode.ai/console", "no known balance query", "https://opencode.ai/console"} {
		if got.Providers[i].Supported || !strings.Contains(got.Providers[i].Note, wantNote) ||
			got.Providers[i].CheckedAt != "" {
			t.Fatalf("provider %d = %+v", i, got.Providers[i])
		}
	}
}
