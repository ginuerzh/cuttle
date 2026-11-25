package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

// warnLogger records warnings while leaving every other logger method to the
// embedded no-op implementation.
type warnLogger struct {
	logger.Logger
	warns []string
}

func (l *warnLogger) Warnf(format string, args ...any) {
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func TestFetchCatalogInfoCollectsEfforts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":1000},"effort":{"supported_levels":["low","high","max"]}}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
	}
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	got := info["p1"]["glm-5.3"]
	if got.Output != 1000 || !slices.Equal(got.Efforts, []string{"low", "high", "max"}) {
		t.Fatalf("catalog = %+v, want output 1000 and efforts [low high max]", got)
	}
}

func TestFetchCatalogInfoKeepsEffortsWithoutOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","effort":{"supported_levels":["low","xhigh"]}}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
	}
	got, ok := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())["p1"]["glm-5.3"]
	if !ok {
		t.Fatal("model with efforts but no output limit is missing from catalog info")
	}
	if got.Output != 0 || !slices.Equal(got.Efforts, []string{"low", "xhigh"}) {
		t.Fatalf("catalog = %+v, want output 0 and efforts [low xhigh]", got)
	}
}

func TestFetchCatalogInfoPreservesKnownEmptyEfforts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"glm-5.3","effort":["low"]}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","effort":["max"]}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL, Responses: srv.URL}},
		Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
	}
	got, ok := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())["p1"]["glm-5.3"]
	if !ok {
		t.Fatal("model with a known empty effort intersection is missing from catalog info")
	}
	if got.Efforts == nil || len(got.Efforts) != 0 {
		t.Fatalf("efforts = %#v, want a non-nil empty slice", got.Efforts)
	}
}

func TestFetchCatalogInfoDeduplicatesProviders(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"context":1000000,"output":131072}}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes: []gwconfig.Node{
			{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"},
			{ID: "n2", Name: "n2", Provider: "p1", Model: "glm-5.3"},
		},
	}
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	if got := info["p1"]["glm-5.3"].Output; got != 131072 {
		t.Fatalf("cap = %d, want 131072", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("catalog calls = %d, want 1", got)
	}
}

func TestFetchCatalogInfoBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
	}
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	if len(info) != 0 {
		t.Fatalf("info = %v, want empty", info)
	}
}

func TestFetchCatalogInfoSkipsModelsWithoutOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3"},{"id":"capped","limit":{"context":1000,"output":8192}},{"id":"zero","limit":{"output":0}},{"id":"unreferenced","limit":{"output":4096}}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes: []gwconfig.Node{
			{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"},
			{ID: "n2", Name: "n2", Provider: "p1", Model: "capped"},
			{ID: "n3", Name: "n3", Provider: "p1", Model: "zero"},
		},
	}
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	if _, ok := info["p1"]["glm-5.3"]; ok {
		t.Fatalf("model without output got a cap: %v", info["p1"])
	}
	if _, ok := info["p1"]["zero"]; ok {
		t.Fatalf("zero output got a cap: %v", info["p1"])
	}
	if got := info["p1"]["capped"].Output; got != 8192 {
		t.Fatalf("cap = %d, want 8192", got)
	}
	if got := info["p1"]["unreferenced"].Output; got != 4096 {
		t.Fatalf("unreferenced cap = %d, want 4096", got)
	}
}

func TestFetchCatalogInfoSkipsDisabledNode(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blocked.Close()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
	}))
	defer live.Close()

	disabled := false
	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{
			{ID: "p-blocked", Name: "p-blocked", Type: "generic", Chat: blocked.URL},
			{ID: "p-live", Name: "p-live", Type: "generic", Chat: live.URL},
		},
		Nodes: []gwconfig.Node{
			{ID: "n-blocked", Name: "n-blocked", Provider: "p-blocked", Model: "blocked", Enabled: &disabled},
			{ID: "n-live", Name: "n-live", Provider: "p-live", Model: "glm-5.3"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	info := fetchCatalogInfo(ctx, cfg, xlogger.Nop())
	if got := info["p-live"]["glm-5.3"].Output; got != 131072 {
		t.Fatalf("enabled model cap = %d, want 131072; info = %v", got, info)
	}
}

func TestFetchCatalogInfoSkipsDisabledProvider(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blocked.Close()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
	}))
	defer live.Close()

	disabled := false
	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{
			{ID: "p-blocked", Name: "p-blocked", Type: "generic", Chat: blocked.URL, Enabled: &disabled},
			{ID: "p-live", Name: "p-live", Type: "generic", Chat: live.URL},
		},
		Nodes: []gwconfig.Node{
			{ID: "n-blocked", Name: "n-blocked", Provider: "p-blocked", Model: "blocked"},
			{ID: "n-live", Name: "n-live", Provider: "p-live", Model: "glm-5.3"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	info := fetchCatalogInfo(ctx, cfg, xlogger.Nop())
	if got := info["p-live"]["glm-5.3"].Output; got != 131072 {
		t.Fatalf("enabled model cap = %d, want 131072; info = %v", got, info)
	}
}

func TestFetchCatalogInfoWarnsOnceAndContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"other","limit":{"output":1234}}]}`)
	}))
	defer srv.Close()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer down.Close()

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer empty.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		ctx  context.Context
		cfg  *gwconfig.Config
		want string
	}{
		{
			name: "provider construction error",
			ctx:  context.Background(),
			cfg: &gwconfig.Config{
				Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "unknown", Chat: srv.URL}},
				Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
			},
			want: "unknown type",
		},
		{
			name: "catalog error",
			ctx:  context.Background(),
			cfg: &gwconfig.Config{
				Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: down.URL}},
				Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
			},
			want: "unexpected status",
		},
		{
			name: "canceled context",
			ctx:  canceled,
			cfg: &gwconfig.Config{
				Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
				Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
			},
			want: "context canceled",
		},
		{
			name: "missing model",
			ctx:  context.Background(),
			cfg: &gwconfig.Config{
				Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: empty.URL}},
				Nodes: []gwconfig.Node{
					{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"},
					{ID: "n2", Name: "n2", Provider: "p1", Model: "glm-5.3"},
				},
			},
			want: "model glm-5.3 not found",
		},
		{
			name: "provider not configured",
			ctx:  context.Background(),
			cfg: &gwconfig.Config{
				Nodes: []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p2", Model: "glm-5.3"}},
			},
			want: "provider p2 not configured",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &warnLogger{Logger: xlogger.Nop()}
			info := fetchCatalogInfo(tc.ctx, tc.cfg, log)
			if len(info) != 0 {
				t.Fatalf("info = %v, want empty", info)
			}
			if len(log.warns) != 1 {
				t.Fatalf("warnings = %q, want exactly one", log.warns)
			}
			if !strings.Contains(log.warns[0], tc.want) {
				t.Fatalf("warning = %q, want it to contain %q", log.warns[0], tc.want)
			}
		})
	}
}
