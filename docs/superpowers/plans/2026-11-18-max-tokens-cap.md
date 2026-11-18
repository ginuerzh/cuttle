# Max Tokens Cap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lower an outbound request's oversized output-token field to the compiled target model's catalog `Output`, so cross-model routes such as `claude-opus-5-5 → glm-5.3` no longer get an upstream 400.

**Architecture:** At hop-build time, cuttle reads each referenced provider's model catalog once and stores the target model's `Output` on the compiled `upstreamTarget`. That value flows through `registerRewriter` into `rewriter.Options`; `Rewriter.Rewrite` applies one post-conversion clamp on request-direction bodies, before capture. Catalog failures are best-effort and never change routing.

**Tech Stack:** Go 1.27, published module tags (`GOWORK=off`), `llm-api-converter` engine, x hop group, standard `encoding/json`.

**Spec:** `docs/superpowers/specs/2026-11-18-max-tokens-cap-design.md`

## Global Constraints

- Cap value is the target model's catalog `Output` (`limit.output`); lower only, never raise. `Output <= 0` means no cap.
- Catalog lookup runs at build/reload time with one overall bounded context of 5 seconds; no last-good cache; a failure logs a warning and leaves no cap.
- No config schema change: `internal/config` uses `yaml.KnownFields(true)`; no new YAML key, no admin API or console changes.
- No changes in sibling repositories, no local `replace`, no `go.mod` bump, no new converter release.
- Keep the x hop group and selector machinery; do not add a custom dispatcher.
- Code, comments, docs and commit messages in English.
- Run all Go commands with `GOWORK=off`; no web bundle change is needed.
- Commit after each completed task; never push. Fill that task's Implementation record in this plan in the same commit.

## Review Focus

- Catalog endpoint down / timeout / model absent / `Output <= 0` → no cap, no build failure: Task 1 tests `TestFetchCatalogLimitsBestEffort`.
- Two nodes sharing one provider → one catalog request per provider: Task 1 test `TestFetchCatalogLimitsDeduplicatesProviders`.
- Client value below the cap or absent → body unchanged, no field injected: Task 3 test `TestClampMaxTokensNoops`.
- Reasoning and non-OpenAI wire shapes use `max_completion_tokens`, `max_output_tokens` and `generationConfig.maxOutputTokens`: Task 3 test `TestClampMaxTokensProtocols`.
- Response bodies and Anthropic `count_tokens` requests must pass untouched: Task 3 test `TestRewriteClampSkipsResponseAndCountTokens`.

---

### Task 1: Build-time catalog limit resolver

**Files:**
- Create: `cuttle/internal/gateway/catalog_limits.go`
- Test: `cuttle/internal/gateway/catalog_limits_test.go`

**Interfaces:**
- Produces: `type catalogLimits map[string]map[string]int` (provider ID → model ID → `Output`).
- Produces: `func fetchCatalogLimits(ctx context.Context, cfg *gwconfig.Config, log logger.Logger) catalogLimits`.

- [ ] **Step 1: Write the failing catalog test**

```go
func TestFetchCatalogLimitsDeduplicatesProviders(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"context":1000000,"output":131072}}]}`)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes: []gwconfig.Node{
			{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"},
			{ID: "n2", Name: "n2", Provider: "p1", Model: "glm-5.3"},
		},
	}
	limits := fetchCatalogLimits(context.Background(), cfg, xlogger.Nop())
	if got := limits["p1"]["glm-5.3"]; got != 131072 {
		t.Fatalf("cap = %d, want 131072", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("catalog calls = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Write the failing best-effort test**

```go
func TestFetchCatalogLimitsBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}},
		Nodes:     []gwconfig.Node{{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}},
	}
	limits := fetchCatalogLimits(context.Background(), cfg, xlogger.Nop())
	if len(limits) != 0 {
		t.Fatalf("limits = %v, want empty", limits)
	}
}
```

Also add a case where `/models` returns a model with no `limit.output` and assert no cap entry for it.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestFetchCatalogLimits -count=1`
Expected: FAIL with `undefined: fetchCatalogLimits`

- [ ] **Step 4: Implement `fetchCatalogLimits`**

Collect provider IDs referenced by `cfg.Nodes`, then for each matching `cfg.Providers` entry create one `provider.New(p)` and call `Models(ctx)`. For every returned `provider.Model` with `Output > 0`, set `limits[p.ID][m.ID] = m.Output`. On `provider.New` error or `Models` error (including context cancellation), call `log.Warnf(...)` and continue. Return the map even when empty.

- [ ] **Step 5: Run the tests and the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestFetchCatalogLimits -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/gateway/catalog_limits.go internal/gateway/catalog_limits_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(gateway): resolve target model output limits from provider catalogs"
```

**Implementation record** (fill after completion): date/status; repo + commit SHA; files changed; tests/commands + results; review findings + fixes; deferred items/limitations.

---

### Task 2: Carry the cap on the compiled node target

**Files:**
- Modify: `cuttle/internal/gateway/upstream.go:25-107`
- Test: `cuttle/internal/gateway/upstream_target_test.go`

**Interfaces:**
- Consumes: `catalogLimits` from Task 1.
- Produces: `compileNodeTargets(p gwconfig.Provider, n gwconfig.Node, limits catalogLimits) (nodeTargets, error)`.
- Produces: `upstreamTarget.maxOutputTokens int` (0 means no cap).

- [ ] **Step 1: Write the failing test**

Add `nil` as the third argument to the existing `compileNodeTargets` calls in `upstream_target_test.go`, then add:

```go
func TestCompileNodeTargetsCarriesCatalogCap(t *testing.T) {
	p := gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: "https://gw.example/v1", Anthropic: "https://gw.example/anthropic"}
	n := gwconfig.Node{ID: "n1", Name: "n1", Provider: "p1", Model: "glm-5.3"}
	limits := catalogLimits{"p1": {"glm-5.3": 131072}}

	targets, err := compileNodeTargets(p, n, limits)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.maxOutputTokens; got != 131072 {
		t.Fatalf("default cap = %d, want 131072", got)
	}
	if got := targets.variants[provider.Anthropic].maxOutputTokens; got != 131072 {
		t.Fatalf("anthropic variant cap = %d, want 131072", got)
	}
	if targets2, err := compileNodeTargets(p, n, nil); err != nil || targets2.defaultTarget.maxOutputTokens != 0 {
		t.Fatalf("nil limits = %+v, %v; want no cap", targets2, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestCompileNodeTargets -count=1`
Expected: FAIL (compile error: too many arguments)

- [ ] **Step 3: Implement the cap on the target**

Change `compileTarget` to take the cap as `int` and set `upstreamTarget.maxOutputTokens`. In `compileNodeTargets`, read `limits[p.ID][targetModel]` once and pass it to every `compileTarget` call (default and variants).

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestCompileNodeTargets -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/gateway/upstream.go internal/gateway/upstream_target_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(gateway): attach model output cap to compiled targets"
```

**Implementation record** (fill after completion): date/status; repo + commit SHA; files changed; tests/commands + results; review findings + fixes; deferred items/limitations.

---

### Task 3: Clamp the rewritten request body

**Files:**
- Modify: `cuttle/internal/rewriter/rewriter.go:31-41,58-128`
- Create: `cuttle/internal/rewriter/max_tokens.go`
- Test: `cuttle/internal/rewriter/max_tokens_test.go`

**Interfaces:**
- Produces: `Options.MaxOutputTokens int`.
- Produces: `func clampMaxTokens(body []byte, protocol string, cap int) ([]byte, error)`.

- [ ] **Step 1: Write the protocol table test**

```go
func TestClampMaxTokensProtocols(t *testing.T) {
	cases := []struct {
		name, protocol, body string
		field                []string // JSON path to the output field
	}{
		{"anthropic", "anthropic", `{"max_tokens":256000,"messages":[]}`, []string{"max_tokens"}},
		{"chat max_tokens", "chat", `{"max_tokens":256000,"messages":[]}`, []string{"max_tokens"}},
		{"chat completion tokens", "chat", `{"max_completion_tokens":256000,"messages":[]}`, []string{"max_completion_tokens"}},
		{"responses", "responses", `{"max_output_tokens":256000,"messages":[]}`, []string{"max_output_tokens"}},
		{"gemini", "gemini", `{"generationConfig":{"maxOutputTokens":256000}}`, []string{"generationConfig", "maxOutputTokens"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := clampMaxTokens([]byte(tc.body), tc.protocol, 131072)
			if err != nil { t.Fatal(err) }
			if got := nestedInt(t, out, tc.field); got != 131072 {
				t.Fatalf("value = %d, want 131072; body %s", got, out)
			}
		})
	}
}
```

`nestedInt` is a test helper that unmarshals into `map[string]any` and reads the path.

- [ ] **Step 2: Write the no-op test**

`TestClampMaxTokensNoops`: table with `{"max_tokens":8192}` under cap 131072 (unchanged), `{"messages":[]}` (no field injected), `{"max_tokens":"big"}` (non-integer unchanged), `nope` (invalid JSON returns original bytes, `err == nil`), and `cap == 0` (unchanged). Assert byte-for-byte equality with the input.

- [ ] **Step 3: Write the Rewrite integration test**

```go
func TestRewriteClampsBeforeCapture(t *testing.T) {
	capMgr := capture.NewManager(capture.Policy{Mode: "errors", MaxBytes: 1 << 20})
	eng := engine.New(engine.Options{
		Model: "glm-5.3",
		ResolveModel: func(string, string) (string, string, string) { return "glm-5.3", "anthropic", "" },
	})
	rw := New(eng, Options{Protocol: "anthropic", MaxOutputTokens: 131072, Capture: capMgr, Sink: &sinkStub{}})
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil { t.Fatal(err) }
	if got := nestedInt(t, out, []string{"max_tokens"}); got != 131072 { t.Fatalf("out = %s", out) }
	cap, ok := capMgr.Finish("s1")
	if !ok || !strings.Contains(cap.RequestRewritten, `"max_tokens":131072`) {
		t.Fatalf("captured rewritten = %+v, ok=%v", cap, ok)
	}
}
```

Add `TestRewriteClampSkipsResponseAndCountTokens`: response-direction body `{"max_tokens":256000}` and request-direction `/v1/messages/count_tokens` with the same value must come back unchanged.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/rewriter -run 'TestClampMaxTokens|TestRewriteClamp' -count=1`
Expected: FAIL (`undefined: clampMaxTokens`, then `unknown field MaxOutputTokens`)

- [ ] **Step 5: Implement the clamp and wire it into `Rewrite`**

`clampMaxTokens` unmarshals into `map[string]json.RawMessage`, reads the first present field from the protocol's candidate list, decodes it as an integer, and re-marshals only when `value > cap`. For `gemini`, read/modify the nested `generationConfig` object. Never add a missing field; return the original bytes on parse/decode errors. In `Rewrite`, after `eng.Rewrite` and before `captureRewrite`, apply it only when `meta.Direction` is `request`, `r.opts.MaxOutputTokens > 0`, and `!isCountTokensURI(meta.URI)`; assign the result to `res.Data` and ignore clamp errors after logging at debug level.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/rewriter -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/rewriter/rewriter.go internal/rewriter/max_tokens.go internal/rewriter/max_tokens_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(rewriter): clamp oversized output tokens to target model limit"
```

**Implementation record** (fill after completion): date/status; repo + commit SHA; files changed; tests/commands + results; review findings + fixes; deferred items/limitations.

---

### Task 4: Wire build-time limits into the registered rewriters

**Files:**
- Modify: `cuttle/internal/gateway/build.go:59,385,760-775`
- Test: `cuttle/internal/gateway/build_cap_test.go` (new)

**Interfaces:**
- Consumes: `fetchCatalogLimits`, `compileNodeTargets(..., limits)`, `gwrewriter.Options.MaxOutputTokens`.
- Produces: every registered node rewriter carries the cap from its compiled target.

- [ ] **Step 1: Write the failing end-to-end build test**

Using the config fixture style from `internal/gateway/bindings_test.go`, build a config with one `httptest` provider serving `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}` at `/models`, one node with `Model: "glm-5.3"`, one route referencing that node, and one entrypoint matching `claude-opus*`. Call `buildHopGroup(cfg, "cap-build", xlogger.Nop(), stats.NewFacts(), nil)`, then:

```go
name := buildRewriterName("cap-build", route.ID, "", node.ID)
rw := registry.RewriterRegistry().Get(name)
out, err := rw.Rewrite(context.Background(),
	[]byte(`{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`),
	corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
if err != nil { t.Fatal(err) }
if got := nestedInt(t, out, []string{"max_tokens"}); got != 131072 { t.Fatalf("out = %s", out) }
```

Unregister the returned rewriter names with `defer` (or `t.Cleanup`). Reuse the small `nestedInt` helper idea from Task 3 inside this test file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestBuildCap -count=1`
Expected: FAIL (registered rewriter returns 256000)

- [ ] **Step 3: Wire the build**

In `buildHopGroup`, create `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)` and `defer cancel()`; call `fetchCatalogLimits(ctx, cfg, log)` and pass the result to every `compileNodeTargets` call. In `registerRewriter`, set `MaxOutputTokens: target.maxOutputTokens` in `gwrewriter.Options`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run TestBuildCap -count=1`
Expected: PASS

- [ ] **Step 5: Run the full Go gate**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Verify manually after deployment**

```bash
curl -sS -o /tmp/glm-cap.json -w '%{http_code}\n' \
  -X POST http://llm-api.home.pi/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"Reply with OK"}]}'
```

Expected: `200`. The new request capture's rewritten body must show model `glm-5.3` and `max_tokens: 131072`.

- [ ] **Step 7: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/gateway/build.go internal/gateway/build_cap_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(gateway): apply catalog output caps to registered rewriters"
```

**Implementation record** (fill after completion): date/status; repo + commit SHA; files changed; tests/commands + results; review findings + fixes; deferred items/limitations.
