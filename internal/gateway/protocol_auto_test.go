package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gost/core/hop"
	xlogger "github.com/go-gost/x/logger"
	mdutil "github.com/go-gost/x/metadata/util"

	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func autoProtocolConfig(t *testing.T, upstream string) *gwconfig.Config {
	t.Helper()
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
		Entrypoints: []gwconfig.Entrypoint{
			{Name: "all", Match: []string{"*"}, Route: "r"},
		},
		Providers: []gwconfig.Provider{{
			Name:      "ds",
			Type:      "deepseek",
			Chat:      upstream + "/chat",
			Responses: upstream + "/responses",
			Anthropic: upstream + "/anthropic",
			Keys:      []string{"sk-test"},
		}},
		Nodes:  []gwconfig.Node{{Name: "n", Provider: "ds", Model: "deepseek-v4-flash"}},
		Routes: []gwconfig.Route{{Name: "r", Nodes: []string{"n"}}},
	}
}

func TestAutoProtocolRouting(t *testing.T) {
	up := newCapabilityUpstream(t)
	gw := startGateway(t, autoProtocolConfig(t, up.srv.URL))

	cases := []struct {
		path         string
		body         string
		wantPath     string
		wantProtocol string
		wantInbound  string
	}{
		{"/v1/chat/completions", `{"model":"deepseek-v4-flash","messages":[]}`, "/chat/chat/completions", "chat", "openai-chat"},
		{"/v1/responses", `{"model":"deepseek-v4-flash","input":"hi"}`, "/responses/responses", "responses", "openai-responses"},
		{"/v1/messages", `{"model":"deepseek-v4-flash","max_tokens":8,"messages":[]}`, "/anthropic/v1/messages", "anthropic", "anthropic"},
	}
	for _, tc := range cases {
		if code := postModel(t, gw, tc.path, tc.body); code != http.StatusOK {
			t.Fatalf("%s status = %d", tc.path, code)
		}
		hit := up.last(t)
		if hit.path != tc.wantPath {
			t.Fatalf("%s upstream path = %q, want %q", tc.path, hit.path, tc.wantPath)
		}
		if v := decodeBody(t, hit.body); v["model"] != "deepseek-v4-flash" {
			t.Fatalf("%s upstream model = %v, want deepseek-v4-flash", tc.path, v["model"])
		}
	}

	rows := waitCapabilityStats(t, gw, len(cases))
	protocols := make(map[string]int)
	inbounds := make(map[string]int)
	for _, row := range rows {
		protocols[row.Protocol]++
		inbounds[row.InboundProtocol]++
	}
	for _, want := range []string{"chat", "responses", "anthropic"} {
		if protocols[want] != 1 {
			t.Fatalf("protocol %q count = %d, want 1 (all %+v)", want, protocols[want], protocols)
		}
	}
	for _, want := range []string{"openai-chat", "openai-responses", "anthropic"} {
		if inbounds[want] != 1 {
			t.Fatalf("inbound %q count = %d, want 1 (all %+v)", want, inbounds[want], inbounds)
		}
	}
}

func TestAutoProtocolUnknownPathFallback(t *testing.T) {
	up := newCapabilityUpstream(t)
	gw := startGateway(t, autoProtocolConfig(t, up.srv.URL))

	if code := postModel(t, gw, "/v1/models", `{"model":"deepseek-v4-flash"}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).path; got != "/v1/models" {
		t.Fatalf("fallback upstream path = %q, want /v1/models", got)
	}
	rows := waitCapabilityStats(t, gw, 1)
	if rows[0].Protocol != "chat" {
		t.Fatalf("protocol = %q, want the default chat", rows[0].Protocol)
	}
}

func TestAutoProtocolStaticClassificationExcludes(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := autoProtocolConfig(t, up.srv.URL)
	cfg.Providers[0].Type = "opencode-go"
	cfg.Providers[0].Responses = ""
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages", `{"model":"deepseek-v4-flash","max_tokens":8,"messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	hit := up.last(t)
	if hit.path != "/chat/chat/completions" {
		t.Fatalf("upstream path = %q, want the chat fallback", hit.path)
	}
	if v := decodeBody(t, hit.body); v["model"] != "deepseek-v4-flash" {
		t.Fatalf("upstream model = %v, want deepseek-v4-flash", v["model"])
	}
}

func TestGenericAutoProtocolUsesConfiguredBase(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := autoProtocolConfig(t, up.srv.URL)
	cfg.Providers[0].Type = "generic"
	cfg.Providers[0].Responses = ""
	cfg.Nodes[0].Model = "model-not-in-the-catalog"
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages", `{"model":"model-not-in-the-catalog","max_tokens":8,"messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).path; got != "/anthropic/v1/messages" {
		t.Fatalf("upstream path = %q, want the anthropic base", got)
	}
}

func TestPinnedNodeAPIOverridesInbound(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := autoProtocolConfig(t, up.srv.URL)
	cfg.Nodes[0].API = "chat"
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages", `{"model":"deepseek-v4-flash","max_tokens":8,"messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).path; got != "/chat/chat/completions" {
		t.Fatalf("upstream path = %q, want the pinned chat base", got)
	}
	rows := waitCapabilityStats(t, gw, 1)
	if rows[0].Protocol != "chat" {
		t.Fatalf("protocol = %q, want chat", rows[0].Protocol)
	}
}

func TestInboundAuthHeadersDoNotReachUpstream(t *testing.T) {
	up := newCapabilityUpstream(t)
	gw := startGateway(t, autoProtocolConfig(t, up.srv.URL))
	client := &http.Client{Timeout: 10 * time.Second}

	post := func(path, body string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		// These mimic Claude Code or an SDK sending its own credentials. The
		// gateway must replace them with the provider credentials, never
		// forward them.
		req.Header.Set("Authorization", "Bearer sk-ant-client")
		req.Header.Set("x-api-key", "sk-ant-client")
		req.Header.Set("x-goog-api-key", "client-google")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, resp.StatusCode, b)
		}
	}

	post("/v1/messages", `{"model":"deepseek-v4-flash","max_tokens":8,"messages":[]}`)
	hit := up.last(t)
	if got := hit.header.Get("Authorization"); got != "" {
		t.Fatalf("anthropic upstream Authorization = %q, want the client credential removed", got)
	}
	if got := hit.header.Get("x-api-key"); got != "sk-test" {
		t.Fatalf("anthropic upstream x-api-key = %q, want the provider key", got)
	}
	if got := hit.header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("anthropic upstream x-goog-api-key = %q, want the client credential removed", got)
	}

	post("/v1/chat/completions", `{"model":"deepseek-v4-flash","messages":[]}`)
	hit = up.last(t)
	if got := hit.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("chat upstream Authorization = %q, want the provider key", got)
	}
	if got := hit.header.Get("x-api-key"); got != "" {
		t.Fatalf("chat upstream x-api-key = %q, want the client credential removed", got)
	}
	if got := hit.header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("chat upstream x-goog-api-key = %q, want the client credential removed", got)
	}
}

func TestEntrypointOrderWithProtocolVariants(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{
			{Name: "first", Match: []string{"*"}, Route: "a"},
			{Name: "second", Match: []string{"*"}, Route: "b"},
		},
		Providers: []gwconfig.Provider{
			{Name: "pa", Type: "deepseek", Chat: up.srv.URL + "/a/chat", Anthropic: up.srv.URL + "/a/anthropic"},
			{Name: "pb", Type: "deepseek", Chat: up.srv.URL + "/b/chat", Anthropic: up.srv.URL + "/b/anthropic"},
		},
		Nodes: []gwconfig.Node{
			{Name: "na", Provider: "pa", Model: "deepseek-v4-flash"},
			{Name: "nb", Provider: "pb", Model: "deepseek-v4-flash"},
		},
		Routes: []gwconfig.Route{
			{Name: "a", Nodes: []string{"na"}},
			{Name: "b", Nodes: []string{"nb"}},
		},
	}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages", `{"model":"deepseek-v4-flash","max_tokens":8,"messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).path; got != "/a/anthropic/v1/messages" {
		t.Fatalf("upstream path = %q, want the first entrypoint's anthropic route", got)
	}
}

func TestRouteVariantKeepsBackupMetadata(t *testing.T) {
	up := newCapabilityUpstream(t)
	disabled := false
	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{
			Name: "ds", Type: "deepseek",
			Chat: up.srv.URL + "/chat", Anthropic: up.srv.URL + "/anthropic",
		}},
		Nodes: []gwconfig.Node{
			{Name: "n", Provider: "ds", Model: "deepseek-v4-flash"},
			{Name: "backup", Provider: "ds", Model: "deepseek-v4-flash", Backup: true},
			{Name: "off", Provider: "ds", Model: "deepseek-v4-flash", Enabled: &disabled},
		},
	}
	route := gwconfig.Route{Name: "plan-test", Nodes: []string{"n", "backup", "off"}}

	plan, err := buildRoutePlan(cfg, "test-build", route, xlogger.Nop(), stats.NewFacts(), capture.NewManager(capture.Policy{Mode: "off"}))
	if err != nil {
		t.Fatal(err)
	}
	h, ok := plan.hops[provider.Anthropic]
	if !ok {
		t.Fatalf("anthropic variant missing: %+v", plan.hops)
	}
	nl, ok := h.(hop.NodeList)
	if !ok {
		t.Fatal("anthropic hop does not expose its nodes")
	}
	var names []string
	backup := false
	for _, n := range nl.Nodes() {
		names = append(names, n.Name)
		if n.Name == "plan-test/ds/backup" {
			backup = mdutil.GetBool(n.Options().Metadata, "backup")
		}
	}
	sort.Strings(names)
	want := []string{"plan-test/ds/backup", "plan-test/ds/n"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("variant nodes = %v, want %v", names, want)
	}
	if !backup {
		t.Fatal("backup metadata missing from the anthropic variant")
	}
}

func TestGeminiAutoProtocol(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
		Entrypoints: []gwconfig.Entrypoint{
			{Name: "all", Match: []string{"*"}, Route: "r"},
		},
		Providers: []gwconfig.Provider{{
			Name:   "gm",
			Type:   "generic",
			Chat:   up.srv.URL + "/chat",
			Gemini: up.srv.URL + "/gemini",
		}},
		Nodes:  []gwconfig.Node{{Name: "n", Provider: "gm", Model: "gemini-2.5-pro"}},
		Routes: []gwconfig.Route{{Name: "r", Nodes: []string{"n"}}},
	}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1beta/models/gemini-2.5-pro:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).path; got != "/gemini/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("upstream path = %q, want the gemini base path", got)
	}
	rows := waitCapabilityStats(t, gw, 1)
	if rows[0].Protocol != "gemini" || rows[0].InboundProtocol != "gemini" {
		t.Fatalf("stats = protocol %q inbound %q, want gemini/gemini", rows[0].Protocol, rows[0].InboundProtocol)
	}
}

func TestFailedReloadKeepsOldRewriters(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
		Entrypoints: []gwconfig.Entrypoint{
			{Name: "a", Match: []string{"a-model"}, Route: "ra"},
			{Name: "b", Match: []string{"b-model"}, Route: "rb"},
		},
		Providers: []gwconfig.Provider{
			{Name: "pa", Type: "generic", Chat: up.srv.URL + "/a/chat"},
			{Name: "pb", Type: "generic", Chat: up.srv.URL + "/b/chat"},
		},
		Nodes: []gwconfig.Node{
			{Name: "na", Provider: "pa", Model: "deepseek-chat"},
			{Name: "nb", Provider: "pb", Model: "deepseek-chat"},
		},
		Routes: []gwconfig.Route{
			{Name: "ra", Nodes: []string{"na"}},
			{Name: "rb", Nodes: []string{"nb"}},
		},
	}
	gw := startGateway(t, cfg)

	const chatBody = `{"model":"a-model","messages":[{"role":"user","content":"hi"}]}`
	if code := postModel(t, gw, "/v1/chat/completions", chatBody); code != http.StatusOK {
		t.Fatalf("status before reload = %d", code)
	}
	if got := up.last(t).path; got != "/a/chat/chat/completions" {
		t.Fatalf("upstream path before reload = %q", got)
	}

	// Route A moves to anthropic; route B is invalid because pb has no
	// anthropic base. The build must fail after route A's rewriter would have
	// been registered under its old name.
	bad := *cfg
	bad.Providers = append([]gwconfig.Provider(nil), cfg.Providers...)
	bad.Providers[0].Anthropic = up.srv.URL + "/a/anthropic"
	bad.Nodes = append([]gwconfig.Node(nil), cfg.Nodes...)
	bad.Nodes[0].API = "anthropic"
	bad.Nodes[1].API = "anthropic"
	if err := gw.Reload(&bad); err == nil {
		t.Fatal("Reload accepted an invalid route B")
	}

	if code := postModel(t, gw, "/v1/chat/completions", chatBody); code != http.StatusOK {
		t.Fatalf("status after failed reload = %d", code)
	}
	hit := up.last(t)
	if hit.path != "/a/chat/chat/completions" {
		t.Fatalf("upstream path after failed reload = %q, want the old chat path", hit.path)
	}
	v := decodeBody(t, hit.body)
	if _, ok := v["max_tokens"]; ok {
		t.Fatalf("failed reload leaked the new anthropic rewriter into the old hop: %s", hit.body)
	}
	if _, ok := v["messages"]; !ok {
		t.Fatalf("old hop body is not chat-shaped: %s", hit.body)
	}

	rows := waitCapabilityStats(t, gw, 2)
	for _, row := range rows {
		if row.Protocol != "chat" {
			t.Fatalf("stats protocol = %q after a failed reload, want chat", row.Protocol)
		}
	}
}

func TestSuccessfulReloadKeepsInFlightResponseRewriter(t *testing.T) {
	gotRequest := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var gotPath string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/chat/chat/completions") {
			once.Do(func() { close(gotRequest) })
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(upstream.Close)

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
		Entrypoints: []gwconfig.Entrypoint{
			{Name: "a", Match: []string{"a-model"}, Route: "ra"},
		},
		Providers: []gwconfig.Provider{{
			Name: "pa", Type: "generic", Chat: upstream.URL + "/a/chat",
		}},
		Nodes:  []gwconfig.Node{{Name: "na", Provider: "pa", Model: "deepseek-chat"}},
		Routes: []gwconfig.Route{{Name: "ra", Nodes: []string{"na"}}},
	}
	gw := startGateway(t, cfg)

	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Post(
			"http://"+gw.Addr().String()+"/v1/chat/completions",
			"application/json",
			strings.NewReader(`{"model":"a-model","messages":[{"role":"user","content":"hi"}]}`),
		)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- result{status: resp.StatusCode, body: string(b)}
	}()

	select {
	case <-gotRequest:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not receive the in-flight request")
	}

	// Reload route A to a different target API while the response is in flight.
	good := *cfg
	good.Providers = append([]gwconfig.Provider(nil), cfg.Providers...)
	good.Providers[0].Anthropic = upstream.URL + "/a/anthropic"
	good.Nodes = append([]gwconfig.Node(nil), cfg.Nodes...)
	good.Nodes[0].API = "anthropic"
	if err := gw.Reload(&good); err != nil {
		t.Fatal(err)
	}
	close(release)

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	v := decodeBody(t, res.body)
	if _, ok := v["choices"]; !ok {
		t.Fatalf("in-flight response used the new target rewriter: %s", res.body)
	}
	mu.Lock()
	path := gotPath
	mu.Unlock()
	if path != "/a/chat/chat/completions" {
		t.Fatalf("in-flight upstream path = %q, want the old chat path", path)
	}
	rows := waitCapabilityStats(t, gw, 1)
	if rows[0].Protocol != "chat" {
		t.Fatalf("in-flight stats protocol = %q, want the old chat target", rows[0].Protocol)
	}
}

func TestCountTokensAnthropicTarget(t *testing.T) {
	up := newCapabilityUpstream(t)
	gw := startGateway(t, autoProtocolConfig(t, up.srv.URL))

	body := `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`
	if code := postModel(t, gw, "/v1/messages/count_tokens", body); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	hit := up.last(t)
	if hit.path != "/anthropic/v1/messages/count_tokens" {
		t.Fatalf("upstream path = %q, want the anthropic count_tokens path", hit.path)
	}
	v := decodeBody(t, hit.body)
	if v["model"] != "deepseek-v4-flash" {
		t.Fatalf("upstream model = %v, want deepseek-v4-flash", v["model"])
	}
	if _, ok := v["messages"]; !ok {
		t.Fatalf("messages missing from the count_tokens body: %s", hit.body)
	}

	rows := waitCapabilityStats(t, gw, 1)
	if rows[0].Protocol != "anthropic" || rows[0].InboundProtocol != "anthropic" {
		t.Fatalf("stats = protocol %q inbound %q, want anthropic/anthropic", rows[0].Protocol, rows[0].InboundProtocol)
	}
}

func TestCountTokensInvalidBodyPassesThrough(t *testing.T) {
	up := newCapabilityUpstream(t)
	gw := startGateway(t, autoProtocolConfig(t, up.srv.URL))

	if code := postModel(t, gw, "/v1/messages/count_tokens", "not-json"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := up.last(t).body; got != "not-json" {
		t.Fatalf("upstream body = %q, want the invalid body unchanged", got)
	}
}

func TestReloadKeepsOldDataPlaneWhenPinnedAPIHasNoBase(t *testing.T) {
	up := newCapabilityUpstream(t)
	cfg := testConfig(up.srv.URL)
	gw := startGateway(t, cfg)

	bad := *cfg
	bad.Nodes = append([]gwconfig.Node(nil), cfg.Nodes...)
	bad.Nodes[0].API = "anthropic"
	if err := gw.Reload(&bad); err == nil {
		t.Fatal("Reload accepted a pinned anthropic API without an anthropic base")
	}

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"deepseek-chat","messages":[]}`); code != http.StatusOK {
		t.Fatalf("old data plane status = %d after a rejected reload", code)
	}
}
