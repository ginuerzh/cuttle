# Effort ceiling by target model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clamp a client's explicit reasoning-effort level to the ceiling the target model declares, so Cuttle models can expose a uniform `max` option.

**Architecture:** Provider catalogs gain normalized effort tiers; the gateway's existing catalog cache carries `{output, efforts}` per model into each route's `upstreamTarget`; the per-node rewriter clamps explicit effort fields before and after protocol conversion. A silent catalog means a `high` ceiling.

**Tech Stack:** Go 1.26, `github.com/ginuerzh/llm-api-converter` v0.7.4, x hop/rewriter machinery, SQLite-backed stats (unchanged).

**Spec:** `docs/superpowers/specs/2026-11-25-effort-ceiling-design.md`

## Global Constraints

- Run every Go command in `/root/code/go-gost/cuttle` with `GOWORK=off`.
- Do not edit `../core`, `../x`, `../llm-api-converter`; no local `replace` directives.
- Code, comments, docs and commit messages are English.
- No web/console change: `web/` and `internal/admin/dist` are untouched.
- Commit after each task, with that task's **Implementation record** filled in and committed in the same commit. Never push unless the user explicitly asks.
- The full Go gate (`GOWORK=off go build ./... && go vet ./... && go test ./... -count=1`) must pass before completion.
- Effort levels and ranks are fixed: `none` 0, `minimal` 1, `low` 2, `medium` 3, `high` 4, `xhigh` 5, `max` 6.
- Default ceiling is `high` (empty or unrecognized catalog effort list).

## Review Focus

Inputs and conditions the spec implies but does not enumerate; each gets its test in the owning task:

1. A body whose text mentions "effort" (message content, tool schema, key name) but carries no level field must come back byte-identical (Task 4).
2. A body carrying more than one explicit level field must have every present field clamped, not just the first (Task 4).
3. A non-JSON or SSE-framed request body must be returned unchanged, with no error (Task 4).
4. Catalog values in mixed case, duplicated, or unknown must normalize without failing the catalog or producing an empty ceiling (Tasks 1 and 2).
5. A build must not observe or mutate a catalog entry another goroutine can refresh: returned effort slices must be copies (Task 3).

---

### Task 1: Effort level package

**Files:**
- Create: `internal/effort/effort.go`
- Test: `internal/effort/effort_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `effort.Normalize(values []string) []string`, `effort.Rank(level string) (int, bool)`, `effort.Ceiling(values []string) string`, `effort.Intersect(a, b []string) []string`.

- [x] **Step 1: Write the failing test**

```go
package effort

import (
	"reflect"
	"testing"
)

func TestRank(t *testing.T) {
	for level, want := range map[string]int{"none": 0, " LOW ": 2, "high": 4, "xhigh": 5, "max": 6} {
		if got, ok := Rank(level); !ok || got != want {
			t.Fatalf("Rank(%q) = %d, %v; want %d, true", level, got, ok, want)
		}
	}
	if _, ok := Rank("extreme"); ok {
		t.Fatal("Rank(extreme) must be unknown")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{"max", "LOW", "max", "extreme", " ", "xhigh"})
	want := []string{"low", "xhigh", "max"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize = %v; want %v", got, want)
	}
}

func TestCeiling(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "high"},
		{[]string{"bogus"}, "high"},
		{[]string{"low", "max", "high"}, "max"},
		{[]string{"low", "xhigh"}, "xhigh"},
		{[]string{"low", "medium"}, "medium"},
	}
	for _, c := range cases {
		if got := Ceiling(c.in); got != c.want {
			t.Fatalf("Ceiling(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestIntersect(t *testing.T) {
	if got := Intersect([]string{"low", "high", "max"}, []string{"low", "high"}); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("intersection = %v", got)
	}
	if got := Intersect(nil, []string{"high", "low"}); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Fatalf("one-sided = %v", got)
	}
	if got := Intersect([]string{"low"}, nil); !reflect.DeepEqual(got, []string{"low"}) {
		t.Fatalf("one-sided = %v", got)
	}
	if got := Intersect([]string{"low"}, []string{"high"}); got != nil {
		t.Fatalf("disjoint = %v; want nil", got)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/effort -count=1`
Expected: build failure, `undefined: Rank` (and the other functions).

- [x] **Step 3: Implement `internal/effort/effort.go`**

```go
package effort

// levelOrder is the fixed reasoning-effort ladder, lowest first.
var levelOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// DefaultCeiling is used when a target declares no effort levels.
const DefaultCeiling = "high"

func Rank(level string) (int, bool)      // trim/lower, index into levelOrder
func Normalize(values []string) []string // rank, lowercase, dedupe, drop unknown, ascending
func Ceiling(values []string) string     // highest normalized value, or DefaultCeiling
func Intersect(a, b []string) []string   // nil-safe; empty side returns the other side
```

Use a map built from `levelOrder` for ranks. `Ceiling` must return `DefaultCeiling` when `Normalize` yields nothing.

- [x] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/effort -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/effort/effort.go internal/effort/effort_test.go docs/superpowers/plans/2026-11-25-effort-ceiling.md
git commit -m "feat(effort): add the reasoning-effort level ladder"
```

**Implementation record**
- Date/status: 2026-11-25 / DONE
- Repository + commit: `cuttle` — commit `60a6940f` (`feat(effort): add the reasoning-effort level ladder`)
- Files changed: `internal/effort/effort.go` (new), `internal/effort/effort_test.go` (new), `docs/superpowers/plans/2026-11-25-effort-ceiling.md` (this record)
- Tests/commands and results: `GOWORK=off go test ./internal/effort -count=1` failed before implementation with `undefined: Rank`, `Normalize`, `Ceiling`, and `Intersect`; the same command passed after implementation (`ok`, 0.003s). `GOWORK=off go test ./internal/effort ./internal/rewriter ./internal/provider -count=1` passed all three packages (0.002s / 0.006s / 0.048s). `gofmt -l internal/effort` produced no output.
- Review findings and fixes: None; the implementation uses the exact fixed ladder, returns fresh normalized slices, preserves nil for disjoint intersections, and defaults empty or unknown-only ceilings to `high`.
- Deferred items/limitations: None for Task 1; provider catalog parsing and rewriter clamping are covered by later tasks.

---

### Task 2: Provider catalog effort parsing

**Files:**
- Modify: `internal/provider/models.go` (`modelItem` ~165, `openAIModelJSON` ~280, `toItem` ~376, `Models` ~62, `mergeModelMetadata` ~625)
- Test: `internal/provider/models_test.go`

**Interfaces:**
- Consumes: `effort.Normalize`, `effort.Intersect` (Task 1).
- Produces: `modelItem.Efforts []string`; `Model.Efforts` populated by `Models()`; `parseEfforts(raw json.RawMessage) []string`.

- [x] **Step 1: Write the failing tests**

```go
func TestToItemParsesEfforts(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"deepseek object", `{"id":"m","effort":{"supported_levels":["low","high","max"],"default_level":"high"}}`, "low,high,max"},
		{"capabilities", `{"id":"m","capabilities":{"reasoning":{"supported":true,"effort":["low","high","max"]}}}`, "low,high,max"},
		{"bare array", `{"id":"m","effort":["high","low"]}`, "low,high"},
		{"string", `{"id":"m","effort":"low, HIGH"}`, "low,high"},
		{"malformed", `{"id":"m","effort":{}}`, ""},
		{"unknown only", `{"id":"m","effort":["extreme"]}`, ""},
		{"capabilities win", `{"id":"m","effort":["low"],"capabilities":{"reasoning":{"effort":["high","max"]}}}`, "high,max"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m openAIModelJSON
			if err := json.Unmarshal([]byte(c.body), &m); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(m.toItem().Efforts, ","); got != c.want {
				t.Fatalf("efforts = %q; want %q", got, c.want)
			}
		})
	}
}

func TestMergeModelMetadataIntersectsEfforts(t *testing.T) {
	dst := Model{Efforts: []string{"low", "high", "max"}}
	mergeModelMetadata(&dst, Model{Efforts: []string{"low", "high"}})
	if got := strings.Join(dst.Efforts, ","); got != "low,high" {
		t.Fatalf("merged = %q", got)
	}
	empty := Model{}
	mergeModelMetadata(&empty, Model{Efforts: []string{"max"}})
	if got := strings.Join(empty.Efforts, ","); got != "max" {
		t.Fatalf("empty dst = %q", got)
	}
}
```

Add `TestModelsPopulatesEfforts`: an `httptest` catalog server returning
`{"data":[{"id":"m","effort":{"supported_levels":["low","high","max"]}}]}`
whose `Models()` result has `Efforts == []string{"low","high","max"}`.

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/provider -run 'Effort' -count=1`
Expected: build failure, `m.toItem().Efforts undefined` and `Model.Efforts` not merged (or undefined field).

- [x] **Step 3: Implement the parsing**

- Add `Efforts []string` to `modelItem`.
- Extend `openAIModelJSON` with `Effort json.RawMessage `json:"effort"`` and `Capabilities openAIModelCapabilitiesJSON `json:"capabilities"``, where the new type is `struct { Reasoning struct { Effort []string `json:"effort"` } `json:"reasoning"` }`.
- `parseEfforts(raw json.RawMessage) []string` accepts an object (`supported_levels`), a string array, or a comma/space-separated string; everything else yields nil. Run the result through `effort.Normalize`.
- In `toItem`, use `capabilities.reasoning.effort` when non-empty, else `parseEfforts(m.Effort)`.
- In `Models()`, set `Efforts: item.Efforts` on the constructed `Model`.
- In `mergeModelMetadata`, set a missing `dst.Efforts` from `src`, and otherwise set `dst.Efforts = effort.Intersect(dst.Efforts, src.Efforts)`.
- `decodeGeminiPage` stays unchanged (no effort metadata).

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/provider -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/provider/models.go internal/provider/models_test.go docs/superpowers/plans/2026-11-25-effort-ceiling.md
git commit -m "feat(provider): parse effort levels from model catalogs"
```

**Implementation record**
- Date/status: 2026-11-25 / DONE
- Repository + commit: `cuttle` — original work `edd5beb6` (`feat(provider): parse effort levels from model catalogs`); review fixes are carried by this commit (`fix(provider): keep an empty effort intersection empty`)
- Files changed: original work: `internal/provider/models.go`, `internal/provider/models_test.go`, `docs/superpowers/plans/2026-11-25-effort-ceiling.md` (Task 1 commit correction and this record). Review fixes: `internal/effort/effort.go`, `internal/effort/effort_test.go`, `internal/provider/models.go`, `internal/provider/models_test.go`, and this record.
- Tests/commands and results: `GOWORK=off go test ./internal/provider -run 'Effort' -count=1` failed before implementation with `m.toItem().Efforts undefined`; it passed after implementation. Self-review added malformed-capabilities cases, which failed on `json: cannot unmarshal ...`, then passed after the lenient decoder. Review fixes were written test-first: `GOWORK=off go test ./internal/provider ./internal/effort -count=1` failed on the three-listing merge and on disjoint/known-empty intersections, then passed after the fix (`provider` 0.049s, `effort` 0.002s). The full gate `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1` passed. The web gate (`npm test`, `npx tsc --noEmit`, `npm run build`) passed 507 tests; it emitted existing React/Ant Design deprecation/act warnings and the Vite chunk-size warning. `gofmt -l` and `git diff --check` were clean.
- Review findings and fixes: Self-review found malformed optional `capabilities` metadata could fail the whole catalog, violating the design's leniency requirement; added a custom lenient `UnmarshalJSON` plus three regression cases. Task 2 review then found that a known empty effort intersection was mistaken for missing metadata and could be repopulated by a third listing, and that this record lacked a concrete SHA. Fixed `effort.Intersect` and `mergeModelMetadata` to preserve nil-versus-known-empty semantics, added disjoint/known-empty unit cases plus a three-listing regression test, and recorded the concrete commit references.
- Deferred items/limitations: None for Task 2; gateway catalog propagation and rewriter clamping remain in Tasks 3–4.

---

### Task 3: Carry catalog efforts into the route rewriter

**Files:**
- Modify: `internal/gateway/catalog_limits.go` (`catalogLimits` ~20, `fetchCatalogLimits` ~32, `fetchCatalogRequest` ~112, `mergeCatalogLimits` ~162)
- Modify: `internal/gateway/catalog_cache.go` (`catalogLimitsEntry` ~21, `catalogLimitsCache` ~33, `Prime` ~67, `Limits` ~106, clone helpers)
- Modify: `internal/gateway/gateway.go:55` and `:383`
- Modify: `internal/gateway/build.go` (`buildHopGroup` ~60, `buildRoutePlan` ~390, `registerRewriter` ~792)
- Modify: `internal/gateway/upstream.go` (`upstreamTarget` ~26, `compileNodeTargets` ~45, `compileTarget` ~99)
- Modify: `internal/rewriter/rewriter.go` (`Options` ~32 — field only; behavior in Task 4)
- Test: `internal/gateway/catalog_limits_test.go`, `internal/gateway/catalog_cache_test.go`, `internal/gateway/upstream_target_test.go`

**Interfaces:**
- Consumes: `provider.Model.Efforts` (Task 2).
- Produces: `catalogInfo map[string]map[string]catalogModel`, `catalogModel{Output int; Efforts []string}`; `catalogCache.Catalog(cfg, log) catalogInfo`; `upstreamTarget.efforts []string`; `rewriter.Options.UpstreamEfforts []string`.

- [x] **Step 1: Write the failing tests**

```go
func TestFetchCatalogInfoCollectsEfforts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":1000},"effort":{"supported_levels":["low","high","max"]}}]}`)
	}))
	defer srv.Close()
	// provider p1 (type generic, chat srv.URL) + node model glm-5.3, as in catalog_limits_test.go
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	got := info["p1"]["glm-5.3"]
	if got.Output != 1000 || strings.Join(got.Efforts, ",") != "low,high,max" {
		t.Fatalf("catalog = %+v", got)
	}
}
```

Add to `catalog_cache_test.go`:
- `TestCatalogCacheKeepsEffortsAfterRefreshFailure`: prime with a catalog that returns efforts, make the server fail, advance the cache clock past the TTL, call `Catalog`, and assert the efforts survive.
- `TestCatalogCacheCopiesEfforts`: mutate a slice returned by `Catalog` and assert a second `Catalog` call still returns the original values.

Add to `upstream_target_test.go`:

```go
func TestCompileNodeTargetsCarriesEfforts(t *testing.T) {
	catalog := catalogInfo{"p1": {"glm-5.3": {Output: 131072, Efforts: []string{"low", "xhigh"}}}}
	// same provider/node fixture as the existing compile tests
	targets, err := compileNodeTargets(provider, node, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(targets.defaultTarget.efforts, ",") != "low,xhigh" {
		t.Fatalf("efforts = %v", targets.defaultTarget.efforts)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'Effort|Catalog' -count=1`
Expected: build failure, `catalogInfo`/`fetchCatalogInfo` undefined.

- [x] **Step 3: Implement the plumbing**

- Define `catalogModel` and `catalogInfo`; rename `fetchCatalogLimits`→`fetchCatalogInfo`, `mergeCatalogLimits`→`mergeCatalogInfo`; `fetchCatalogRequest` returns `map[string]catalogModel` and fills both fields.
- Rename the cache to `catalogCache` with entry field `models map[string]catalogModel`; rename `Limits`→`Catalog`, `catalogLimitsEntry`→`catalogEntry`, and clone helpers to deep-copy `Efforts` slices (use `slices.Clone`).
- Rename `catalogLimits` parameters through `gateway.go`, `buildHopGroup`, `buildRoutePlan`, `compileNodeTargets`.
- `compileNodeTargets` reads `catalog[p.ID][targetModel]`, passes `Output` as `maxOutputTokens` and `Efforts` into `compileTarget`; `compileTarget` gains an `efforts []string` parameter and stores it on `upstreamTarget`.
- `registerRewriter` sets `UpstreamEfforts: target.efforts` in `gwrewriter.Options`.
- Add the `UpstreamEfforts []string` field to `rewriter.Options` (no behavior yet).
- Update existing tests mechanically for the renamed types.

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/gateway -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/gateway internal/rewriter/rewriter.go docs/superpowers/plans/2026-11-25-effort-ceiling.md
git commit -m "feat(gateway): carry target effort tiers into the rewriter"
```

**Implementation record**
- Date/status: 2026-11-25 / DONE
- Repository + commit: `cuttle` — code commit `7129dbd2` (`feat(gateway): carry target effort tiers into the rewriter`); record-only follow-up commit subject: `docs: record task 3 implementation`
- Files changed: `internal/gateway/catalog_limits.go`, `internal/gateway/catalog_cache.go`, `internal/gateway/gateway.go`, `internal/gateway/build.go`, `internal/gateway/upstream.go`, `internal/rewriter/rewriter.go`, and the tests in `internal/gateway/catalog_limits_test.go`, `internal/gateway/catalog_cache_test.go`, `internal/gateway/upstream_target_test.go`, `internal/gateway/build_cap_test.go`
- Tests/commands and results: RED: `GOWORK=off go test ./internal/gateway -run 'Effort|Catalog' -count=1` failed to build with `undefined: newCatalogCache`, `undefined: fetchCatalogInfo`, `undefined: catalogInfo`, and `targets.defaultTarget.efforts undefined`. GREEN: the same command passed (`ok`, 0.420s), then `GOWORK=off go test ./internal/gateway -count=1` passed (11.889s). The required gate `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./internal/gateway ./internal/provider ./internal/rewriter -count=1` passed (`gateway` 11.897s, `provider` 0.056s, `rewriter` 0.009s). The full Go gate `GOWORK=off go test ./... -count=1` passed every package (`gateway` 18.055s, `admin` 21.314s, `stats` 29.501s). `gofmt` and `git diff --check` were clean.
- Review findings and fixes: Self-review found no functional issues. The implementation deep-copies effort slices at the catalog fetch, merge, cache snapshot, and cache return boundaries, preserves nil versus known-empty effort metadata, and keeps catalog TTL, retry, coalescing, prime and last-good behavior unchanged. Self-review added two coverage cases beyond the brief: efforts without an output limit and a known-empty effort intersection surviving the catalog fetch.
- Deferred items/limitations: Actual effort clamping, logging and stats ordering are Task 4; this task only adds `Options.UpstreamEfforts` and plumbs the value into `registerRewriter`, so the end-to-end registered-rewriter assertion is intentionally left to Task 4's build test. Documentation is Task 5.

---

### Task 4: Clamp explicit effort in the rewriter

**Files:**
- Create: `internal/rewriter/effort.go`
- Modify: `internal/rewriter/rewriter.go` (`Rewrite` ~60, `requestEffort` stays as-is)
- Test: `internal/rewriter/effort_test.go`, `internal/rewriter/rewriter_test.go`, `internal/gateway/build_cap_test.go`

**Interfaces:**
- Consumes: `effort.Rank`, `effort.Ceiling` (Task 1); `Options.UpstreamEfforts` (Task 3); `engine.New`/`engine.Options`.
- Produces: `clampEffort(body []byte, ceiling string) ([]byte, error)`.

- [ ] **Step 1: Write the failing unit tests**

```go
func TestClampEffort(t *testing.T) {
	cases := []struct{ name, body, ceiling, want string }{
		{"chat above", `{"reasoning_effort":"max","model":"m"}`, "xhigh", `{"model":"m","reasoning_effort":"xhigh"}`},
		{"responses above", `{"reasoning":{"effort":"max"}}`, "high", `{"reasoning":{"effort":"high"}}`},
		{"anthropic above", `{"output_config":{"effort":"max"},"model":"m"}`, "xhigh", `{"model":"m","output_config":{"effort":"xhigh"}}`},
		{"gemini above", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"max"}}}`, "high", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`},
		{"chat at ceiling", `{"reasoning_effort":"max"}`, "max", `{"reasoning_effort":"max"}`},
		{"below ceiling", `{"reasoning_effort":"low"}`, "high", `{"reasoning_effort":"low"}`},
		{"both fields", `{"reasoning_effort":"max","output_config":{"effort":"max"}}`, "xhigh", `{"output_config":{"effort":"xhigh"},"reasoning_effort":"xhigh"}`},
		{"invalid json", `not-json`, "high", `not-json`},
		{"mention only", `{"messages":[{"role":"user","content":"what effort should I use?"}]}`, "high", `{"messages":[{"role":"user","content":"what effort should I use?"}]}`},
		{"empty ceiling", `{"reasoning_effort":"max"}`, "", `{"reasoning_effort":"max"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := clampEffort([]byte(c.body), c.ceiling)
			if err != nil {
				t.Fatal(err)
			}
			var g, w any
			_ = json.Unmarshal(got, &g)
			_ = json.Unmarshal([]byte(c.want), &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("got %s; want %s", got, c.want)
			}
			if c.want == c.body && string(got) != c.body {
				t.Fatalf("unchanged body was re-marshaled: %s", got)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/rewriter -run TestClampEffort -count=1`
Expected: build failure, `undefined: clampEffort`.

- [ ] **Step 3: Implement `internal/rewriter/effort.go`**

- `clampEffort`: return the body unchanged when `ceiling` has no rank, the body is empty, or the body contains neither `effort` nor `thinkingLevel` (byte pre-check).
- Unmarshal into `map[string]json.RawMessage`; clamp each present path `reasoning_effort`, `reasoning.effort`, `output_config.effort`, `generationConfig.thinkingConfig.thinkingLevel` when its string value ranks above the ceiling; re-marshal only when something changed; on any unmarshal/marshal error return the original body (and the error).
- Path traversal re-marshals only the innermost changed object, matching `max_tokens.go`'s raw-message style.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/rewriter -run TestClampEffort -count=1`
Expected: PASS.

- [ ] **Step 5: Write the failing rewriter tests**

Add to `rewriter_test.go`:

```go
func TestRewriteClampsEffortAndKeepsClientLevel(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{
		Sink: sink, Protocol: "chat",
		UpstreamEfforts: []string{"low", "high", "xhigh"},
	})
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-sonnet-5-5","reasoning_effort":"max"}`), requestMeta("/v1/chat/completions"))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedString(t, out, "reasoning_effort"); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q; want xhigh; body = %s", got, out)
	}
	if sink.effort != "max" {
		t.Fatalf("sink effort = %q; want the client level max", sink.effort)
	}
}

func TestRewriteClampsAfterConversion(t *testing.T) {
	eng := engine.New(engine.Options{ResolveModel: func(string, string) (string, string, string) {
		return "deepseek-v4.1-flash", "chat", ""
	}})
	rw := New(eng, Options{Protocol: "chat", UpstreamEfforts: []string{"low", "high", "xhigh"}})
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-sonnet-5-5","output_config":{"effort":"xhigh"},"messages":[{"role":"user","content":"hi"}],"max_tokens":64}`),
		requestMeta("/v1/messages"))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedString(t, out, "reasoning_effort"); got != "xhigh" {
		t.Fatalf("converted reasoning_effort = %q; want xhigh; body = %s", got, out)
	}
}

func TestRewriteSkipsCountTokensEffort(t *testing.T) {
	rw := New(engine.New(engine.Options{}), Options{
		Protocol: "anthropic", UpstreamEfforts: []string{"low", "high"},
	})
	in := []byte(`{"model":"claude-sonnet-5-5","reasoning_effort":"max"}`)
	out, err := rw.Rewrite(context.Background(), in, requestMeta("/v1/messages/count_tokens"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("count_tokens body changed: %s", out)
	}
}
```

Use the existing `sinkStub`; add a small `requestMeta(uri string)` helper plus a `nestedString(t, body, key)` helper (or decode into `map[string]any`) in the same test file.

Add to `build_cap_test.go`:

```go
func TestBuildEffortCeilingReachesRegisteredRewriter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072},"capabilities":{"reasoning":{"supported":true,"effort":["low","high","xhigh"]}}}]}`)
	}))
	defer srv.Close()
	// reuse the provider/node/route fixture from TestBuildCapReachesRegisteredRewriter
	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	_, _, names, _, err := buildHopGroup(cfg, "effort-build", info, xlogger.Nop(), stats.NewFacts(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterRewriters(names) })
	rw := registry.RewriterRegistry().Get(buildRewriterName("effort-build", routeID, variantKey(""), nodeID))
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"glm-5.3","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedString(t, out, "reasoning_effort"); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q; want xhigh; body = %s", got, out)
	}
}
```

`build_cap_test.go` already has `nestedInt`; add the string equivalent beside it. `buildHopGroup`'s capture-manager argument may be `nil` here, as in the existing test.

- [ ] **Step 6: Run the rewriter tests to verify they fail**

Run: `GOWORK=off go test ./internal/rewriter -run 'Effort|CountTokens' -count=1`
Expected: FAIL, effort not clamped (and/or stats recording the clamped level).

- [ ] **Step 7: Implement the hooks in `Rewriter.Rewrite`**

- Compute `isRequest`, `ceiling := effort.Ceiling(r.opts.UpstreamEfforts)` and `clientEffort := requestEffort(meta.Direction, b)` at the top of the request path.
- Before `r.eng.Rewrite`: when `isRequest && !isCountTokensURI(meta.URI)`, run `clampEffort(b, ceiling)`; on success replace `b`; when the bytes changed, log `requested`/`effective` (`requestEffort("request", clamped)`) at debug with sid, route, node.
- After a successful `r.eng.Rewrite`: run `clampEffort(res.Data, ceiling)` under the same guards and replace `res.Data`.
- Replace the trailing `if level := requestEffort(meta.Direction, b); ...` with `if clientEffort != "" { res.Effort = clientEffort }`.

- [ ] **Step 8: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/rewriter internal/gateway/build_cap_test.go docs/superpowers/plans/2026-11-25-effort-ceiling.md
git commit -m "feat(rewriter): clamp reasoning effort to the target ceiling"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: Document effort resolution

**Files:**
- Modify: `README.md` (new `## Reasoning effort` section after `## Token counting`, before `## Connection warming`)

**Interfaces:**
- Consumes: behavior shipped by Tasks 1–4.
- Produces: user-facing documentation only.

- [ ] **Step 1: Add the section**

Content must state: explicit levels `low|medium|high|xhigh|max` are read from `reasoning_effort`, `reasoning.effort`, `output_config.effort` and `generationConfig.thinkingConfig.thinkingLevel`; the ceiling comes from the target model's catalog; `max` resolves to `max`, else `xhigh`, else `high`; a catalog with no level metadata is treated as `high`; the clamp happens before and after protocol conversion; stats keep the client-selected level.

- [ ] **Step 2: Verify the section renders and the claims match the code**

Run: `grep -n "Reasoning effort" README.md` and re-read the section against `internal/rewriter/effort.go` and `internal/effort/effort.go`.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/superpowers/plans/2026-11-25-effort-ceiling.md
git commit -m "docs: document reasoning-effort resolution"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: Apply the client option and deploy (approval-gated)

**Files:**
- Modify (outside the repository): `~/.config/opencode/opencode.json`
- Modify (workspace memory, separate repository): `/root/code/go-gost/.memory/notes/opencode-custom-providers.md`, `/root/code/go-gost/.memory/notes/cuttle-effort-display.md`, `/root/code/go-gost/.memory/MEMORY.md`

**Interfaces:**
- Consumes: the deployed gateway behavior.
- Produces: OpenCode exposing `low|high|max` for the four Cuttle models; durable notes.

- [ ] **Step 1: Add the effort capability to all four Cuttle models**

For `claude-fable-5`, `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-5-5` add to the existing `capabilities` object:

```jsonc
"reasoning": { "supported": true, "effort": ["low", "high", "max"] }
```

- [ ] **Step 2: Verify OpenCode sees the new variants**

Use the OpenCode models tool for provider `cuttle`; each model must list variants `low`, `high`, `max` (no `medium`).

- [ ] **Step 3: Stop for deployment approval**

Pushing the branch publishes a `cuttle` image; the production rollout touches live traffic. Do not push, build the image or run `kubectl rollout` until the user explicitly approves.

- [ ] **Step 4: After approval, deploy and verify**

Push, wait for the docker workflow, then `kubectl -n llm rollout restart deploy/llm-gw` and `kubectl -n llm rollout status deploy/llm-gw`. Verify `http://llm-gw.home.pi/api/health`, then confirm `GET /api/providers/{id}/models` shows non-empty `efforts` for DeepSeek and opencode-go providers. Send one real request with `reasoning_effort:max` to `cuttle/claude-sonnet-5-5` (expect `effort=max` in stats, no clamp log) and one to `cuttle/claude-fable-5` (expect `effort=max` in stats and a debug clamp log showing effective `high`).

- [ ] **Step 5: Update workspace memory**

Add the effort-ceiling decision and the client variant list to the existing notes, add wikilinks, and commit in `/root/code/go-gost` as a separate commit.

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
