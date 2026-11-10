package gateway_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/gateway"
)

func stickyUpstream(name string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Provider", name)
		if r.Header.Get("X-Test-Fail") != "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"provider-`+name+`"},"finish_reason":"stop"}]}`)
	}))
}

func stickyConfig(ocURL, ccURL string) *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Entrypoints = []config.Entrypoint{{ID: "e_0000000001", Name: "default", Match: []string{"*"}, Route: "r_0000000001"}}
	cfg.Providers = []config.Provider{
		{ID: "p_0000000001", Name: "oc", Type: "generic", Chat: ocURL, Keys: []config.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-oc"}}},
		{ID: "p_0000000002", Name: "cc", Type: "generic", Chat: ccURL, Keys: []config.ProviderKey{{ID: "pk_0000000002", Name: "key-1", Key: "sk-cc"}}},
	}
	cfg.Nodes = []config.Node{
		{ID: "n_0000000001", Name: "oc", Provider: "p_0000000001", Model: "m", Weight: 10},
		{ID: "n_0000000002", Name: "cc", Provider: "p_0000000002", Model: "m", Weight: 3},
	}
	cfg.Routes = []config.Route{{ID: "r_0000000001", Name: "r", Nodes: []string{"n_0000000001", "n_0000000002"}, Strategy: "sticky"}}
	return cfg
}

func stickyPost(t *testing.T, addr, session string, fail bool) (body string, status int, provider string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-claude-code-session-id", session)
	if fail {
		req.Header.Set("X-Test-Fail", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode, resp.Header.Get("X-Test-Provider")
}

func TestStickyRouteKeepsOneSessionOnOneNode(t *testing.T) {
	oc := stickyUpstream("oc")
	defer oc.Close()
	cc := stickyUpstream("cc")
	defer cc.Close()

	gw, err := gateway.New(stickyConfig(oc.URL, cc.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	_, status, first := stickyPost(t, gw.Addr().String(), "sess-sticky", false)
	if status != http.StatusOK || first == "" {
		t.Fatalf("first request = %d/%q", status, first)
	}
	for i := 0; i < 10; i++ {
		body, status, provider := stickyPost(t, gw.Addr().String(), "sess-sticky", false)
		if status != http.StatusOK {
			t.Fatalf("request %d status = %d", i, status)
		}
		if provider != first {
			t.Fatalf("request %d provider = %q, want %q", i, provider, first)
		}
		if !strings.Contains(body, "provider-"+first) {
			t.Fatalf("request %d body = %q, want provider-%s", i, body, first)
		}
	}
}

func TestStickyRouteWeightedDistribution(t *testing.T) {
	oc := stickyUpstream("oc")
	defer oc.Close()
	cc := stickyUpstream("cc")
	defer cc.Close()

	gw, err := gateway.New(stickyConfig(oc.URL, cc.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	const total = 400
	counts := map[string]int{}
	for i := 0; i < total; i++ {
		_, status, provider := stickyPost(t, gw.Addr().String(), fmt.Sprintf("sess-dist-%d", i), false)
		if status != http.StatusOK {
			t.Fatalf("request %d status = %d", i, status)
		}
		counts[provider]++
	}
	share := float64(counts["oc"]) / total
	if share < 0.70 || share > 0.84 {
		t.Fatalf("oc share = %.4f (counts %v), want about 10/13", share, counts)
	}
}

func TestStickyRouteMovesAfterFailureAndStays(t *testing.T) {
	oc := stickyUpstream("oc")
	defer oc.Close()
	cc := stickyUpstream("cc")
	defer cc.Close()

	gw, err := gateway.New(stickyConfig(oc.URL, cc.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	_, status, failed := stickyPost(t, gw.Addr().String(), "sess-fail", true)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("failing request status = %d, want 503", status)
	}
	if failed == "" {
		t.Fatal("failing request did not name its provider")
	}

	_, status, moved := stickyPost(t, gw.Addr().String(), "sess-fail", false)
	if status != http.StatusOK {
		t.Fatalf("failover status = %d", status)
	}
	if moved == failed {
		t.Fatalf("session stayed on the failed provider %q", failed)
	}

	_, status, again := stickyPost(t, gw.Addr().String(), "sess-fail", false)
	if status != http.StatusOK {
		t.Fatalf("second failover status = %d", status)
	}
	if again != moved {
		t.Fatalf("session moved again from %q to %q", moved, again)
	}
}
