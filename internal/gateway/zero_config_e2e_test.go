package gateway_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/admin"
	"github.com/ginuerzh/cuttle/internal/bootstrap"
	"github.com/ginuerzh/cuttle/internal/gateway"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestZeroConfigOnboarding(t *testing.T) {
	var hitPath, hitAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		hitAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	dataDir := t.TempDir()

	staleToken := "stale-session-token"
	sum := sha256.Sum256([]byte(staleToken))
	staleHash := hex.EncodeToString(sum[:])
	store, err := stats.Open(filepath.Join(dataDir, "stats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutSession(context.Background(), staleHash, time.Now().Add(time.Hour)); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	opts := bootstrap.Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":         dataDir,
			"CUTTLE_LISTEN":       "127.0.0.1:0",
			"CUTTLE_ADMIN_LISTEN": "127.0.0.1:0",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	}
	res, err := bootstrap.Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated {
		t.Fatal("first Prepare did not generate a config")
	}
	if res.GeneratedPassword == "" {
		t.Fatal("first Prepare did not generate a password")
	}

	gw, err := gateway.New(res.Config, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if !res.ResetSessions {
		gw.Close()
		t.Fatal("first Prepare did not request a session reset")
	}
	if store := gw.Stats(); store != nil {
		if err := store.DeleteAllSessions(context.Background()); err != nil {
			gw.Close()
			t.Fatal(err)
		}
	}
	if err := bootstrap.ClearSessionsReset(res.DataDir); err != nil {
		gw.Close()
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		gw.Close()
		t.Fatal(err)
	}
	gwClosed := false
	defer func() {
		if !gwClosed {
			gw.Close()
		}
	}()

	adminSrv := admin.New(res.ConfigPath, res.Config, gw, xlogger.Nop())
	ts := httptest.NewServer(adminSrv.Handler())
	tsClosed := false
	defer func() {
		if !tsClosed {
			ts.Close()
		}
	}()

	staleReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/config", nil)
	staleReq.AddCookie(&http.Cookie{Name: "cuttle_session", Value: staleToken})
	staleResp, err := http.DefaultClient.Do(staleReq)
	if err != nil {
		ts.Close()
		gw.Close()
		t.Fatal(err)
	}
	staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusUnauthorized {
		ts.Close()
		gw.Close()
		t.Fatalf("stale session status = %d, want 401", staleResp.StatusCode)
	}

	shortClient := &http.Client{Timeout: 2 * time.Second}
	preReq, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.5","messages":[]}`))
	preReq.Header.Set("Content-Type", "application/json")
	preResp, preErr := shortClient.Do(preReq)
	if preErr == nil {
		defer preResp.Body.Close()
		if preResp.StatusCode < 500 {
			ts.Close()
			gw.Close()
			t.Fatalf("pre-config request status = %d, want >= 500 or an error", preResp.StatusCode)
		}
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginResp, err := client.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"password":"`+res.GeneratedPassword+`"}`))
	if err != nil {
		ts.Close()
		gw.Close()
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusNoContent {
		ts.Close()
		gw.Close()
		t.Fatalf("login = %d, want 204", loginResp.StatusCode)
	}

	u, _ := url.Parse(ts.URL)
	var csrf string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "cuttle_csrf" {
			csrf = ck.Value
		}
	}

	do := func(method, path, body string) int {
		t.Helper()
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

	provider := `{"name":"p","type":"generic","chat":"` + upstream.URL + `","keys":[{"name":"key-1","key":"sk-zero"}]}`
	if code := do(http.MethodPost, "/api/providers", provider); code != http.StatusCreated {
		ts.Close()
		gw.Close()
		t.Fatalf("create provider = %d, want 201", code)
	}
	if code := do(http.MethodPost, "/api/nodes", `{"name":"p","provider":"p","model":"m1"}`); code != http.StatusCreated {
		ts.Close()
		gw.Close()
		t.Fatalf("create node = %d, want 201", code)
	}
	if code := do(http.MethodPost, "/api/routes", `{"name":"r","nodes":["p"]}`); code != http.StatusCreated {
		ts.Close()
		gw.Close()
		t.Fatalf("create route = %d, want 201", code)
	}
	if code := do(http.MethodPut, "/api/entrypoints", `[{"name":"default","match":["*"],"route":"r"}]`); code != http.StatusNoContent {
		ts.Close()
		gw.Close()
		t.Fatalf("put entrypoints = %d, want 204", code)
	}

	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","messages":[]}`))
	if err != nil {
		ts.Close()
		gw.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ts.Close()
		gw.Close()
		t.Fatalf("gateway status = %d, want 200", resp.StatusCode)
	}
	if hitPath != "/chat/completions" {
		ts.Close()
		gw.Close()
		t.Fatalf("upstream path = %q, want /chat/completions", hitPath)
	}
	if hitAuth != "Bearer sk-zero" {
		ts.Close()
		gw.Close()
		t.Fatalf("upstream auth = %q, want Bearer sk-zero", hitAuth)
	}

	ts.Close()
	tsClosed = true
	gw.Close()
	gwClosed = true

	buf.Reset()
	res2, err := bootstrap.Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Generated {
		t.Fatal("second Prepare regenerated the config")
	}
	if res2.GeneratedPassword != "" {
		t.Fatal("second Prepare generated a password")
	}
	if res2.ResetSessions {
		t.Fatal("second Prepare still requests a session reset")
	}
	if buf.Len() != 0 {
		t.Fatalf("second Prepare wrote to stderr: %q", buf.String())
	}
	if len(res2.Config.Providers) != 1 {
		t.Fatalf("persisted providers = %d, want 1", len(res2.Config.Providers))
	}

	gw2, err := gateway.New(res2.Config, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer gw2.Close()
	if err := gw2.Start(); err != nil {
		t.Fatal(err)
	}

	hitPath, hitAuth = "", ""
	resp, err = http.Post("http://"+gw2.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restart gateway status = %d, want 200", resp.StatusCode)
	}
	if hitPath != "/chat/completions" {
		t.Fatalf("restart upstream path = %q, want /chat/completions", hitPath)
	}
	if hitAuth != "Bearer sk-zero" {
		t.Fatalf("restart upstream auth = %q, want Bearer sk-zero", hitAuth)
	}
}
