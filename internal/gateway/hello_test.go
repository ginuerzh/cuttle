package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHelloProbeAnsweredLocally(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, testConfig(up.URL))
	base := "http://" + gw.Addr().String()

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		wantBody string
	}{
		{"head", http.MethodHead, "/api/hello", ""},
		{"get", http.MethodGet, "/api/hello", `{"message":"hello"}`},
		{"get with query", http.MethodGet, "/api/hello?probe=1", `{"message":"hello"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, base+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d body = %s", resp.StatusCode, body)
			}
			if string(body) != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream was called %d times", got)
	}
}

func TestHelloOnlyMatchesExactPath(t *testing.T) {
	for _, line := range []string{
		"HEAD /api/hello HTTP/1.1\r\n",
		"GET /api/hello?x=1 HTTP/1.1\r\n",
	} {
		if !isHelloLine([]byte(line)) {
			t.Fatalf("isHelloLine(%q) = false", line)
		}
	}
	for _, line := range []string{
		"POST /api/hello HTTP/1.1\r\n",
		"GET /api/hello/extra HTTP/1.1\r\n",
		"GET /v1/messages HTTP/1.1\r\n",
		"",
	} {
		if isHelloLine([]byte(line)) {
			t.Fatalf("isHelloLine(%q) = true", line)
		}
	}
}
