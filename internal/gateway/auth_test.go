package gateway

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
)

const (
	authTestSecret    = "gateway-secret"
	authTestWant401   = `{"error":{"message":"invalid gateway key","type":"authentication_error"}}`
	authTestChatBody  = `{"model":"deepseek-chat","messages":[]}`
	authTestHelloPath = "/api/hello"
)

func boolPtr(v bool) *bool { return &v }

func authUpstream(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(up.Close)
	return up, &hits
}

func authConfig(upstreamURL string, keys ...gwconfig.GatewayKey) *gwconfig.Config {
	cfg := testConfig(upstreamURL)
	cfg.Gateway.Keys = keys
	return cfg
}

func authRequest(t *testing.T, gw *Gateway, method, path, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequest(method, "http://"+gw.Addr().String()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func TestGatewayAuthOpenWhenNoKeys(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, testConfig(up.URL))

	resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

func TestGatewayAuthAcceptsSupportedHeaders(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	for _, tc := range []struct {
		name    string
		header  string
		value   string
		wantHit int32
	}{
		{"authorization bearer", "Authorization", "Bearer " + authTestSecret, 1},
		{"authorization lowercase scheme", "Authorization", "bearer " + authTestSecret, 2},
		{"x-api-key", "x-api-key", authTestSecret, 3},
		{"x-goog-api-key", "x-goog-api-key", authTestSecret, 4},
		{"api-key", "api-key", authTestSecret, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody,
				map[string]string{tc.header: tc.value})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d body = %s", resp.StatusCode, body)
			}
			if got := hits.Load(); got != tc.wantHit {
				t.Fatalf("upstream hits = %d, want %d", got, tc.wantHit)
			}
		})
	}
}

func TestGatewayAuthAnyCandidateMayMatch(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, map[string]string{
		"Authorization": "Bearer wrong-value",
		"x-api-key":     authTestSecret,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

func TestGatewayAuthRejectsMissingInvalidAndDisabled(t *testing.T) {
	up, hits := authUpstream(t)

	for _, tc := range []struct {
		name    string
		keys    []gwconfig.GatewayKey
		headers map[string]string
	}{
		{
			name: "missing",
			keys: []gwconfig.GatewayKey{{Name: "client", Key: authTestSecret}},
		},
		{
			name:    "invalid",
			keys:    []gwconfig.GatewayKey{{Name: "client", Key: authTestSecret}},
			headers: map[string]string{"Authorization": "Bearer wrong-value"},
		},
		{
			name:    "disabled",
			keys:    []gwconfig.GatewayKey{{Name: "off", Key: "off-secret", Enabled: boolPtr(false)}},
			headers: map[string]string{"Authorization": "Bearer off-secret"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gw := startGateway(t, authConfig(up.URL, tc.keys...))

			resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, tc.headers)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d body = %s, want 401", resp.StatusCode, body)
			}
			if body != authTestWant401 {
				t.Fatalf("body = %q, want %q", body, authTestWant401)
			}
			if got := resp.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("content-type = %q", got)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
				t.Fatalf("www-authenticate = %q, want a Bearer challenge", got)
			}
			if !resp.Close {
				t.Fatal("response does not advertise Connection: close")
			}
		})
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}

func TestAuthPolicyReasons(t *testing.T) {
	policy := newAuthPolicy([]gwconfig.GatewayKey{
		{Name: "first", Key: "first-secret"},
		{Name: "second", Key: "second-secret"},
		{Name: "disabled", Key: "disabled-secret", Enabled: boolPtr(false)},
	})
	if !policy.required {
		t.Fatal("policy should be required")
	}

	for _, tc := range []struct {
		name       string
		candidates []string
		wantName   string
		wantReason string
		wantOK     bool
	}{
		{"no candidates", nil, "", "missing", false},
		{"wrong candidate", []string{"wrong-secret"}, "", "invalid", false},
		{"disabled candidate", []string{"disabled-secret"}, "", "disabled", false},
		{"second candidate", []string{"second-secret"}, "second", "", true},
		{"both first and second", []string{"first-secret", "second-secret"}, "first", "", true},
		{"wrong plus second", []string{"wrong-secret", "second-secret"}, "second", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, reason, ok := policy.authenticate(tc.candidates)
			if name != tc.wantName || reason != tc.wantReason || ok != tc.wantOK {
				t.Fatalf("authenticate(%v) = (%q, %q, %v), want (%q, %q, %v)",
					tc.candidates, name, reason, ok, tc.wantName, tc.wantReason, tc.wantOK)
			}
		})
	}

	open := newAuthPolicy(nil)
	if open.required || open.hasEnabled() {
		t.Fatalf("empty policy = %+v, want open and without enabled entries", open)
	}
}

func TestGatewayHelloStaysPublic(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	resp, body := authRequest(t, gw, http.MethodGet, authTestHelloPath, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}

func TestGatewayCountTokensRequiresKey(t *testing.T) {
	up, _ := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	resp, body := authRequest(t, gw, http.MethodPost, "/v1/messages/count_tokens", `{"model":"claude-3"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated count_tokens status = %d body = %s, want 401", resp.StatusCode, body)
	}

	resp, body = authRequest(t, gw, http.MethodPost, "/v1/messages/count_tokens", `{"model":"claude-3"}`,
		map[string]string{"x-api-key": authTestSecret})
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("authenticated count_tokens status = %d body = %s, want not 401", resp.StatusCode, body)
	}
}

func TestGatewayAuthRejectsMalformedHead(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	conn, err := net.Dial("tcp", gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := io.WriteString(conn, "not http\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}

func TestGatewayAuthKeyNotForwarded(t *testing.T) {
	var (
		mu      sync.Mutex
		headers http.Header
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = r.Header.Clone()
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))
	resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, map[string]string{
		"Authorization":  "Bearer " + authTestSecret,
		"x-api-key":      authTestSecret,
		"x-goog-api-key": authTestSecret,
		"api-key":        authTestSecret,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	mu.Lock()
	defer mu.Unlock()
	for name, values := range headers {
		for _, value := range values {
			if strings.Contains(value, authTestSecret) {
				t.Fatalf("upstream header %s leaked the gateway key", name)
			}
		}
	}
}

func TestGatewayAuthReplaysLargeBody(t *testing.T) {
	var (
		mu      sync.Mutex
		gotLen  int
		gotHead string
		gotTail string
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		mu.Lock()
		if err == nil {
			gotLen = len(b)
			if len(b) > 100 {
				gotHead, gotTail = string(b[:60]), string(b[len(b)-60:])
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(up.Close)

	const bodySize = 1 << 20
	prefix := `{"model":"deepseek-chat","messages":[{"role":"user","content":"`
	suffix := `"}]}`
	body := prefix + strings.Repeat("a", bodySize-len(prefix)-len(suffix)) + suffix
	if len(body) != bodySize {
		t.Fatalf("test body = %d bytes, want %d", len(body), bodySize)
	}

	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))
	// An unmapped path bypasses the body rewriter, so the upstream sees the
	// exact bytes the auth handler replayed.
	resp, respBody := authRequest(t, gw, http.MethodPost, "/unknown", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + authTestSecret,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, respBody)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotLen != bodySize {
		t.Fatalf("upstream body = %d bytes, want %d (head=%q tail=%q)", gotLen, bodySize, gotHead, gotTail)
	}
}

func TestGatewayAuthReloadRotates(t *testing.T) {
	up, _ := authUpstream(t)
	const (
		keyA = "rotate-secret-a"
		keyB = "rotate-secret-b"
	)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: keyA}))

	if resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody,
		map[string]string{"Authorization": "Bearer " + keyA}); resp.StatusCode != http.StatusOK {
		t.Fatalf("key A before reload: status = %d body = %s", resp.StatusCode, body)
	}

	if err := gw.Reload(authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: keyB})); err != nil {
		t.Fatal(err)
	}

	if resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody,
		map[string]string{"Authorization": "Bearer " + keyA}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("key A after reload: status = %d body = %s, want 401", resp.StatusCode, body)
	}
	if resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody,
		map[string]string{"Authorization": "Bearer " + keyB}); resp.StatusCode != http.StatusOK {
		t.Fatalf("key B after reload: status = %d body = %s", resp.StatusCode, body)
	}
}
