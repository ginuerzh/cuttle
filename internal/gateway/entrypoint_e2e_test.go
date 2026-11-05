package gateway_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/gateway"
)

func writeEntrypointConfig(t *testing.T, dir, upstream string, defaultFirst bool) string {
	t.Helper()
	entrypoints := `
entrypoints:
  - name: fast
    match: ["gpt-4.5*"]
    route: fast
  - name: strong
    match: ["claude-opus*", "gpt-5.5*"]
    route: strong
  - name: default
    match: ["*"]
    route: fast
`
	if defaultFirst {
		entrypoints = `
entrypoints:
  - name: fast
    match: ["gpt-4.5*"]
    route: fast
  - name: default
    match: ["*"]
    route: fast
  - name: strong
    match: ["claude-opus*", "gpt-5.5*"]
    route: strong
`
	}
	body := fmt.Sprintf(`
gateway:
  listen: "127.0.0.1:0"
stats:
  path: %q
providers:
  - name: up
    type: generic
    chat: %q
nodes:
  - name: fast-node
    provider: up
    model: fast-model
  - name: strong-node
    provider: up
    model: strong-model
routes:
  - name: fast
    nodes: [fast-node]
  - name: strong
    nodes: [strong-node]
%s`, filepath.Join(dir, "stats.sqlite"), upstream, entrypoints)
	path := filepath.Join(dir, "llm-gw.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gatewayFromConfig(t *testing.T, path string) *gateway.Gateway {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	gw, err := gateway.New(cfg, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { gw.Close() })
	return gw
}

func postModelAndBody(t *testing.T, gw *gateway.Gateway, model string) string {
	t.Helper()
	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"`+model+`","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d: %s", model, resp.StatusCode, b)
	}
	return string(b)
}

func TestEntrypointConfigEndToEnd(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotModel = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	dir := t.TempDir()
	gw := gatewayFromConfig(t, writeEntrypointConfig(t, dir, upstream.URL, false))

	for _, tc := range []struct {
		model string
		want  string
	}{
		{"gpt-4.5", "fast-model"},
		{"claude-opus-4-5", "strong-model"},
		{"gpt-5.5", "strong-model"},
		{"unknown-model", "fast-model"},
	} {
		gotModel = ""
		postModelAndBody(t, gw, tc.model)
		if !strings.Contains(gotModel, `"model":"`+tc.want+`"`) {
			t.Fatalf("%s upstream body = %s, want model %s", tc.model, gotModel, tc.want)
		}
	}

	// Reorder entrypoints so the catch-all is before strong. Reloading the
	// running gateway must change precedence without a restart.
	reloaded, err := config.Load(writeEntrypointConfig(t, dir, upstream.URL, true))
	if err != nil {
		t.Fatalf("reload load: %v", err)
	}
	if err := gw.Reload(reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}
	gotModel = ""
	postModelAndBody(t, gw, "claude-opus-4-5")
	if !strings.Contains(gotModel, `"model":"fast-model"`) {
		t.Fatalf("after reorder upstream body = %s, want fast-model", gotModel)
	}
}
