package gateway

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
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
			if snap := gw.AuthFailures().Snapshot(); len(snap.Recent) != 0 {
				t.Fatalf("authenticated request recorded as a failure: %+v", snap.Recent)
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
		reason  string
	}{
		{
			name:   "missing",
			keys:   []gwconfig.GatewayKey{{Name: "client", Key: authTestSecret}},
			reason: "missing",
		},
		{
			name:    "invalid",
			keys:    []gwconfig.GatewayKey{{Name: "client", Key: authTestSecret}},
			headers: map[string]string{"Authorization": "Bearer wrong-value"},
			reason:  "invalid",
		},
		{
			name:    "disabled",
			keys:    []gwconfig.GatewayKey{{Name: "off", Key: "off-secret", Enabled: boolPtr(false)}},
			headers: map[string]string{"Authorization": "Bearer off-secret"},
			reason:  "disabled",
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
			if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer realm="cuttle"` {
				t.Fatalf("www-authenticate = %q, want %q", got, `Bearer realm="cuttle"`)
			}
			if !resp.Close {
				t.Fatal("response does not advertise Connection: close")
			}

			snap := gw.AuthFailures().Snapshot()
			if len(snap.Recent) != 1 {
				t.Fatalf("recent failures = %d, want 1", len(snap.Recent))
			}
			got := snap.Recent[0]
			if got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
			if got.Method != http.MethodPost || got.Path != "/v1/chat/completions" {
				t.Fatalf("method/path = %q %q", got.Method, got.Path)
			}
			if got.ClientIP == "" {
				t.Fatal("client IP is empty")
			}
			for _, secret := range []string{authTestSecret, "wrong-value", "off-secret"} {
				if strings.Contains(got.Method, secret) || strings.Contains(got.Path, secret) ||
					strings.Contains(got.ClientIP, secret) || strings.Contains(got.UserAgent, secret) {
					t.Fatalf("credential leaked into failure record: %+v", got)
				}
			}
		})
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}

func TestAuthPolicyReasons(t *testing.T) {
	policy := newAuthPolicy([]gwconfig.GatewayKey{
		{ID: "gk_0000000001", Name: "first", Key: "first-secret"},
		{ID: "gk_0000000002", Name: "second", Key: "second-secret"},
		{ID: "gk_0000000003", Name: "disabled", Key: "disabled-secret", Enabled: boolPtr(false)},
	})
	if !policy.required {
		t.Fatal("policy should be required")
	}

	for _, tc := range []struct {
		name       string
		candidates []string
		wantID     string
		wantReason string
		wantOK     bool
	}{
		{"no candidates", nil, "", "missing", false},
		{"wrong candidate", []string{"wrong-secret"}, "", "invalid", false},
		{"disabled candidate", []string{"disabled-secret"}, "", "disabled", false},
		{"second candidate", []string{"second-secret"}, "gk_0000000002", "", true},
		{"both first and second", []string{"first-secret", "second-secret"}, "gk_0000000001", "", true},
		{"wrong plus second", []string{"wrong-secret", "second-secret"}, "gk_0000000002", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, reason, ok := policy.authenticate(tc.candidates)
			if id != tc.wantID || reason != tc.wantReason || ok != tc.wantOK {
				t.Fatalf("authenticate(%v) = (%q, %q, %v), want (%q, %q, %v)",
					tc.candidates, id, reason, ok, tc.wantID, tc.wantReason, tc.wantOK)
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

// authRawDial opens a raw TCP connection so a test can reuse the socket for a
// second request that a normal HTTP client would suppress after the first
// response advertises Connection: close.
func authRawDial(t *testing.T, gw *Gateway) (net.Conn, *bufio.Reader) {
	t.Helper()

	conn, err := net.Dial("tcp", gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn, bufio.NewReader(conn)
}

func authRawWrite(t *testing.T, conn net.Conn, raw string) {
	t.Helper()

	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
}

func authRawReadResponse(t *testing.T, br *bufio.Reader) (*http.Response, string) {
	t.Helper()

	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw response body: %v", err)
	}
	return resp, string(body)
}

// authRawSecondRequest sends a request on an already-used socket and returns
// the response status, or -1 when the server closed the connection.
func authRawSecondRequest(t *testing.T, conn net.Conn, br *bufio.Reader, raw string) int {
	t.Helper()

	if _, err := io.WriteString(conn, raw); err != nil {
		return -1
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second request on the same connection: status = %d body = %q, want 401 or a closed connection",
			resp.StatusCode, body)
	}
	return resp.StatusCode
}

func authRawPOSTRequest(path, body string, headers map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "POST %s HTTP/1.1\r\nHost: gateway.test\r\nContent-Length: %d\r\n", path, len(body))
	for name, value := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", name, value)
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

func authRawChunkedRequest(path, chunkedBody string, headers map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "POST %s HTTP/1.1\r\nHost: gateway.test\r\nTransfer-Encoding: chunked\r\n", path)
	for name, value := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", name, value)
	}
	b.WriteString("\r\n")
	b.WriteString(chunkedBody)
	return b.String()
}

// TestGatewayAuthSecondRequestOnSameConnectionIsRejected is the regression test
// for connection-scoped authentication: x's keep-alive loop must not process a
// second request on an already-authenticated socket.
func TestGatewayAuthSecondRequestOnSameConnectionIsRejected(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	conn, br := authRawDial(t, gw)
	authRawWrite(t, conn, authRawPOSTRequest("/unknown", authTestChatBody, map[string]string{
		"Authorization": "Bearer " + authTestSecret,
		"Content-Type":  "application/json",
	}))
	resp, body := authRawReadResponse(t, br)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d body = %s", resp.StatusCode, body)
	}

	authRawSecondRequest(t, conn, br, authRawPOSTRequest("/unknown", authTestChatBody, map[string]string{
		"Content-Type": "application/json",
	}))
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1: the second request on the connection must not reach the upstream", got)
	}
}

func TestGatewayAuthSecondChunkedRequestOnSameConnectionIsRejected(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody string
		hits    atomic.Int32
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, err := io.ReadAll(r.Body)
		mu.Lock()
		if err == nil {
			gotBody = string(b)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))
	conn, br := authRawDial(t, gw)

	body := `{"model":"deepseek-chat","messages":[{"role":"user","content":"` + strings.Repeat("c", 5000) + `"}]}`
	var chunked strings.Builder
	for i, part := range []string{body[:7], body[7:2048], body[2048:]} {
		if i == 0 {
			// Chunk extensions must be passed through with the size line.
			fmt.Fprintf(&chunked, "%x;test=1\r\n%s\r\n", len(part), part)
			continue
		}
		fmt.Fprintf(&chunked, "%x\r\n%s\r\n", len(part), part)
	}
	chunked.WriteString("0\r\n")
	chunked.WriteString("X-Test-Trailer: yes\r\n")
	chunked.WriteString("\r\n")

	authRawWrite(t, conn, authRawChunkedRequest("/unknown", chunked.String(), map[string]string{
		"Authorization": "Bearer " + authTestSecret,
		"Content-Type":  "application/json",
		"Trailer":       "X-Test-Trailer",
	}))
	resp, respBody := authRawReadResponse(t, br)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d body = %s", resp.StatusCode, respBody)
	}

	mu.Lock()
	received := gotBody
	mu.Unlock()
	if received != body {
		t.Fatalf("upstream body = %d bytes, want %d", len(received), len(body))
	}

	authRawSecondRequest(t, conn, br, authRawPOSTRequest("/unknown", authTestChatBody, nil))
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1: the second request on the connection must not reach the upstream", got)
	}
}

func TestGatewayAuthNoBodyFirstRequestClosesConnection(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	conn, br := authRawDial(t, gw)
	authRawWrite(t, conn, "GET /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer "+authTestSecret+"\r\n\r\n")
	resp, body := authRawReadResponse(t, br)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d body = %s", resp.StatusCode, body)
	}

	authRawSecondRequest(t, conn, br, "GET /unknown HTTP/1.1\r\nHost: gateway.test\r\n\r\n")
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1: the second request on the connection must not reach the upstream", got)
	}
}

func TestGatewayAuthAllDisabledRejectsMissingCredential(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{
		Name:    "off",
		Key:     "off-secret",
		Enabled: boolPtr(false),
	}))

	resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want 401", resp.StatusCode, body)
	}
	if body != authTestWant401 {
		t.Fatalf("body = %q, want %q", body, authTestWant401)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer realm="cuttle"` {
		t.Fatalf("www-authenticate = %q, want %q", got, `Bearer realm="cuttle"`)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}

func TestGatewayAuthLogTargetStripsQuery(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   string
	}{
		{"/v1/chat/completions", "/v1/chat/completions"},
		{"/v1/chat?key=secret", "/v1/chat"},
		{"http://user:pass@example.test/v1/messages?api_key=secret", "/v1/messages"},
		{"http://user:pass@host", "/"},
		{"http://user:pass@host/v1?x", "/v1"},
		{"/x%0a", "/x%0A"},
		{"*", "*"},
		{"://bad", "/"},
	} {
		if got := gatewayAuthLogTarget(tc.target); got != tc.want {
			t.Fatalf("gatewayAuthLogTarget(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestSanitizeGatewayAuthMethod(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   string
	}{
		{"GET", "GET"},
		{"POST", "POST"},
		{"BAD METHOD", "-"},
		{"GET\n", "-"},
		{"GET\x7f", "-"},
		{"GÉT", "-"},
	} {
		if got := sanitizeGatewayAuthMethod(tc.method); got != tc.want {
			t.Fatalf("sanitizeGatewayAuthMethod(%q) = %q, want %q", tc.method, got, tc.want)
		}
	}
}

// authFillingReader emits 'a' forever without a newline, recording how many
// bytes the head reader consumed.
type authFillingReader struct {
	read int
}

func (r *authFillingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.read += len(p)
	return len(p), nil
}

func TestReadGatewayAuthHeadRejectsOversizedLine(t *testing.T) {
	reader := &authFillingReader{}
	head, err := readGatewayAuthHead(bufio.NewReader(reader))
	if !errors.Is(err, errGatewayAuthHeadTooLarge) {
		t.Fatalf("err = %v, want errGatewayAuthHeadTooLarge", err)
	}
	if len(head) > gatewayAuthHeaderLimit {
		t.Fatalf("head = %d bytes, want at most %d", len(head), gatewayAuthHeaderLimit)
	}
	if max := gatewayAuthHeaderLimit + 4096; reader.read > max {
		t.Fatalf("read %d bytes, want at most %d (limit plus one bufio buffer)", reader.read, max)
	}
}

func TestReadGatewayAuthHeadLongLine(t *testing.T) {
	const longHeaderPrefix = "X-Long: "
	for _, contentLength := range []int{4095, 4096, 8191, 8192} {
		t.Run(fmt.Sprintf("content-%d", contentLength), func(t *testing.T) {
			// The header line's content (prefix plus value) is exactly
			// contentLength bytes, so the terminating CRLF lands on the next
			// 4096-byte fragment boundary. Before fragments were accumulated,
			// that lone CRLF was mistaken for the blank line and the rest of
			// the head was left unread.
			raw := "GET /v1/models HTTP/1.1\r\nHost: gateway.test\r\n" +
				longHeaderPrefix + strings.Repeat("a", contentLength-len(longHeaderPrefix)) + "\r\n" +
				"X-After: after\r\n\r\n"
			head, err := readGatewayAuthHead(bufio.NewReader(strings.NewReader(raw)))
			if err != nil {
				t.Fatalf("read head: %v", err)
			}
			if string(head) != raw {
				t.Fatalf("head = %d bytes, want %d", len(head), len(raw))
			}
			if !bytes.Contains(head, []byte("X-After: after\r\n")) {
				t.Fatalf("head does not contain the header following the long line")
			}
			if !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
				t.Fatalf("head does not end with the blank line")
			}
		})
	}
}

func TestGatewayAuthReloadToEmptyKeysTurnsAuthOff(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	if resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status before reload = %d body = %s, want 401", resp.StatusCode, body)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits before reload = %d, want 0", got)
	}

	if err := gw.Reload(testConfig(up.URL)); err != nil {
		t.Fatal(err)
	}

	resp, body := authRequest(t, gw, http.MethodPost, "/v1/chat/completions", authTestChatBody, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status after reload = %d body = %s, want 200", resp.StatusCode, body)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits after reload = %d, want 1", got)
	}
}

func TestGatewayAuthRejectsInvalidBodyFraming(t *testing.T) {
	up, hits := authUpstream(t)
	gw := startGateway(t, authConfig(up.URL, gwconfig.GatewayKey{Name: "client", Key: authTestSecret}))

	for _, tc := range []struct {
		name string
		head string
	}{
		{
			name: "invalid content-length",
			head: "POST /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer " + authTestSecret + "\r\nContent-Length: nope\r\n\r\n",
		},
		{
			name: "negative content-length",
			head: "POST /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer " + authTestSecret + "\r\nContent-Length: -1\r\n\r\n",
		},
		{
			name: "duplicate content-length",
			head: "POST /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer " + authTestSecret + "\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n",
		},
		{
			name: "unsupported transfer-encoding",
			head: "POST /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer " + authTestSecret + "\r\nTransfer-Encoding: gzip\r\n\r\n",
		},
		{
			name: "chunked not final",
			head: "POST /unknown HTTP/1.1\r\nHost: gateway.test\r\nAuthorization: Bearer " + authTestSecret + "\r\nTransfer-Encoding: chunked, gzip\r\n\r\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, br := authRawDial(t, gw)
			authRawWrite(t, conn, tc.head)
			resp, body := authRawReadResponse(t, br)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", resp.StatusCode, body)
			}
		})
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
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
