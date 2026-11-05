package gateway_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/go-gost/llm-gw/internal/admin"
	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/gateway"
)

func TestAdminCreatesProviderAndTakesEffect(t *testing.T) {
	var hitPath, hitAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		hitAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfgPath := filepath.Join(t.TempDir(), "llm-gw.yaml")
	hash, err := config.HashPassword("pass")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Admin.PasswordHash = hash
	cfg.Admin.SessionTTL = config.Duration(time.Hour)
	config.ApplyDefaults(cfg)
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")

	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	ts := httptest.NewServer(admin.New(cfgPath, cfg, gw, xlogger.Nop()).Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"password":"pass"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	u, _ := url.Parse(ts.URL)
	var csrf string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "llm_gw_csrf" {
			csrf = ck.Value
		}
	}

	do := func(method, path, body string) int {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	provider := `{"name":"p","type":"generic","chat":"` + upstream.URL + `","keys":["sk-e2e"]}`
	if code := do(http.MethodPost, "/api/providers", provider); code != http.StatusCreated {
		t.Fatalf("create provider = %d", code)
	}
	if code := do(http.MethodPost, "/api/nodes", `{"name":"p","provider":"p","model":"m1"}`); code != http.StatusCreated {
		t.Fatalf("create node = %d", code)
	}
	if code := do(http.MethodPost, "/api/routes", `{"name":"r","nodes":["p"]}`); code != http.StatusCreated {
		t.Fatalf("create route = %d", code)
	}
	if code := do(http.MethodPut, "/api/entrypoints", `[{"name":"default","match":["*"],"route":"r"}]`); code != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", code)
	}

	resp, err = http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if hitPath != "/chat/completions" {
		t.Fatalf("upstream path = %q, want /chat/completions (provider created via API must be live)", hitPath)
	}
	if hitAuth != "Bearer sk-e2e" {
		t.Fatalf("upstream auth = %q", hitAuth)
	}

	// Restart consistency: the file on disk must load into the same config.
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Providers) != 1 || saved.Providers[0].Name != "p" || len(saved.Nodes) != 1 || len(saved.Routes) != 1 {
		t.Fatalf("persisted config mismatch: %+v", saved)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}
