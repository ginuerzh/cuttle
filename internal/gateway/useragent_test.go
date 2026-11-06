package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-gost/llm-gw/internal/version"
)

func TestUpstreamUserAgentIsGatewayOwned(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, testConfig(up.URL))
	req, _ := http.NewRequest(http.MethodPost,
		"http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Python-urllib/3.14")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	want := "llm-gw/" + version.Current().Version
	if got != want {
		t.Fatalf("upstream User-Agent = %q, want %q", got, want)
	}
}

func TestProviderHeadersAreApplied(t *testing.T) {
	var gotUA, gotCustom string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotCustom = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(up.Close)

	cfg := testConfig(up.URL)
	cfg.Providers[0].Headers = map[string]string{
		"User-Agent": "commandcode-client/2.0",
		"X-Custom":   "yes",
	}
	gw := startGateway(t, cfg)
	req, _ := http.NewRequest(http.MethodPost,
		"http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Python-urllib/3.14")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if gotUA != "commandcode-client/2.0" || gotCustom != "yes" {
		t.Fatalf("upstream headers = UA %q, X-Custom %q", gotUA, gotCustom)
	}
}

func TestProviderUserAgentOverride(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(up.Close)

	cfg := testConfig(up.URL)
	cfg.Providers[0].UserAgent = "commandcode-client/1.0"
	gw := startGateway(t, cfg)
	req, _ := http.NewRequest(http.MethodPost,
		"http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Python-urllib/3.14")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got != "commandcode-client/1.0" {
		t.Fatalf("upstream User-Agent = %q", got)
	}
}
