package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
)

func testConfig(upstreamURL string, pathMap map[string]string) *gwconfig.Config {
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Providers: []gwconfig.Provider{{
			Name:     "up",
			BaseURL:  upstreamURL,
			Protocol: "openai",
			Keys:     []string{"sk-test"},
			Auth:     gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
			PathMap:  pathMap,
		}},
		Routes: []gwconfig.Route{{
			Name:      "default",
			Providers: []string{"up"},
		}},
	}
}

func startGateway(t *testing.T, cfg *gwconfig.Config) *Gateway {
	t.Helper()

	gw, err := New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	return gw
}

func TestGatewayForwardsAndRewrites(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gw := startGateway(t, testConfig(upstream.URL, map[string]string{
		"/v1/chat/completions": "/chat/completions",
	}))

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+gw.Addr().String()+"/v1/chat/completions?x=1",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotQuery != "x=1" {
		t.Fatalf("query = %q, want x=1", gotQuery)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("authorization = %q, want Bearer sk-test", gotAuth)
	}
	if !strings.Contains(gotBody, `"model":"deepseek-chat"`) {
		t.Fatalf("body = %q, want the model unchanged", gotBody)
	}
	if !resp.Close {
		t.Fatal("response should close the connection (http.keepalive=false)")
	}
}

func TestGatewayPassesThroughUnmappedPathWithoutKey(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL, map[string]string{"/mapped": "/other"})
	cfg.Providers[0].Keys = nil
	gw := startGateway(t, cfg)

	resp, err := http.Get("http://" + gw.Addr().String() + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if gotPath != "/unknown" {
		t.Fatalf("path = %q, want /unknown (pass-through)", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("authorization = %q, want empty for a keyless provider", gotAuth)
	}
}
