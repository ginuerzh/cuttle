# Local `count_tokens` Estimation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Answer Claude Code's `POST /v1/messages/count_tokens` locally for routes whose upstream has no native token counter, while preserving native pass-through for Anthropic routes.

**Architecture:** A `handler.Handler` wrapper around the existing `forwardlocal` handler inspects the first request on each connection. When the path is `count_tokens`, it reads the body, resolves the route through the ordered entrypoint model globs, and either estimates locally or replays the request to the wrapped handler. The estimate is a pure Go, offline-calibrated heuristic in `internal/tokenest`; no runtime tokenizer, no new modules, no config changes.

**Tech Stack:** Go 1.27, `go-gost` x `forwardlocal` handler, `llm-api-converter` (unchanged), standard library only.

**Spec:** `docs/superpowers/specs/2026-11-06-count-tokens-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off`; `go.mod` uses published tags and must not gain a local `replace`.
- Do not add a Go module, a CGO dependency, a YAML field, an admin API change, or a web change.
- Do not change `inboundGroups`, the x hop group/selector, x node names, or the routing chain (entrypoint → route → node → provider model).
- Anthropic targets pass `count_tokens` through unchanged. Malformed or non-JSON bodies pass through on every route.
- A locally answered `count_tokens` request returns `200 {"input_tokens": N}` with `N >= 1`; it makes no upstream request and writes no stats row or capture.
- The wrapper handles the first request per connection. `http.keepalive=false` in `Gateway.New` is an invariant; do not remove it.
- Code, comments, docs and commit messages are English.
- Commit after every task. Keep the task's **Implementation record** in this plan; follow the repo precedent of a separate `docs(plan): record task N` commit that names the code commit SHA.
- Never push.

## Review Focus

- **`?beta=true`:** Claude Code may append a query string; the wrapper must strip it before matching and still estimate. Test: Task 3 `TestCountTokensQueryString`.
- **Malformed or non-JSON body:** it must pass through unchanged, including on a Chat route, instead of becoming a 400. Tests: Task 3 `TestCountTokensInvalidBodyPassesThroughOnChatTarget` and the existing `TestCountTokensInvalidBodyPassesThrough`.
- **Mixed route:** a route with both Anthropic and non-Anthropic nodes must estimate for all `count_tokens` requests rather than sometimes forwarding to a 404. Test: Task 3 `TestCountTokensMixedRouteEstimates`.
- **Tool/image/document content:** the estimator must count `tools`, `tool_use`, `tool_result`, images and documents, not just plain text. Tests: Task 1 `TestEstimateAnthropicCountsToolUseAndResults` and `TestEstimateAnthropicImageAndDocument`.
- **Reload policy swap:** a reload from a Chat route to an Anthropic route must flip the decision without a restart. Test: Task 3 `TestCountTokensReloadFlipsPolicy`.

---

### Task 1: Add the calibrated token estimator

**Files:**
- Create: `internal/tokenest/estimate.go`
- Create: `internal/tokenest/estimate_test.go`
- Create: `internal/tokenest/testdata/calibration.jsonl`
- Create: `scripts/gen-count-tokens-fixtures.py`

**Interfaces:**
- Consumes: nothing outside the standard library.
- Produces:
  - `tokenest.EstimateAnthropic(body []byte, safetyFactor float64) (int, error)`
  - `tokenest.DefaultSafetyFactor = 1.1`
  - unexported `estimateText(text string) float64` (the calibration test calls it directly)

- [x] **Step 1: Write the failing tests**

Create `internal/tokenest/estimate_test.go`:

```go
package tokenest

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestEstimateAnthropicMinimumOne(t *testing.T) {
	cases := []string{
		`{}`,
		`{"model":"m","messages":[]}`,
		`{"model":"m","system":"","messages":[{"role":"user","content":""}]}`,
		`{"model":"m","messages":[{"role":"user","content":[]}],"tools":[]}`,
	}
	for _, body := range cases {
		n, err := EstimateAnthropic([]byte(body), DefaultSafetyFactor)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if n < 1 {
			t.Fatalf("%s: n = %d, want >= 1", body, n)
		}
	}
}

func TestEstimateAnthropicCountsMessagesAndTools(t *testing.T) {
	base := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	longer := []byte(`{"model":"m","messages":[{"role":"user","content":"hello hello hello hello hello hello hello hello"}]}`)
	withTool := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"get_weather","description":"Get the current weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]}`)

	nb, err := EstimateAnthropic(base, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nl, err := EstimateAnthropic(longer, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nt, err := EstimateAnthropic(withTool, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if !(nb < nl && nl < nt) {
		t.Fatalf("estimates = %d, %d, %d; want base < longer < withTool", nb, nl, nt)
	}
}

func TestEstimateAnthropicCountsToolUseAndResults(t *testing.T) {
	without := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"}]}]}`)
	with := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t","name":"get_weather","input":{"city":"San Francisco"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"sunny, 22C"}]}]}`)

	nw, err := EstimateAnthropic(without, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	n, err := EstimateAnthropic(with, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if n < nw+10 {
		t.Fatalf("estimate with tool use = %d, without = %d; want at least +10", n, nw)
	}
}

func TestEstimateAnthropicImageAndDocument(t *testing.T) {
	text := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	image := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`)
	document := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"document","source":{"type":"text","media_type":"text/plain","data":"..."}}]}]}`)

	nt, err := EstimateAnthropic(text, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	ni, err := EstimateAnthropic(image, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nd, err := EstimateAnthropic(document, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if ni < nt+1600 {
		t.Fatalf("image estimate = %d, text = %d; want at least +1600", ni, nt)
	}
	if nd < nt+2000 {
		t.Fatalf("document estimate = %d, text = %d; want at least +2000", nd, nt)
	}
}

func TestEstimateAnthropicRejectsInvalidBody(t *testing.T) {
	for _, body := range []string{"", "not-json", "[]", `"text"`} {
		if _, err := EstimateAnthropic([]byte(body), DefaultSafetyFactor); err == nil {
			t.Fatalf("%q: expected an error", body)
		}
	}
}

func TestEstimateTextMonotonic(t *testing.T) {
	text := []rune("The quick brown fox 你好，世界")
	prev := estimateText("")
	for i := 1; i <= len(text); i++ {
		got := estimateText(string(text[:i]))
		if got < prev {
			t.Fatalf("estimate decreased at rune %d: %f < %f", i, got, prev)
		}
		prev = got
	}
}

func TestCalibration(t *testing.T) {
	f, err := os.Open("testdata/calibration.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var rows int
	var sumErr float64
	var under int
	for sc.Scan() {
		var row struct {
			Text   string `json:"text"`
			Tokens int    `json:"tokens"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		got := int(math.Ceil(estimateText(row.Text) * DefaultSafetyFactor))
		if got < 1 {
			t.Fatalf("row %d: estimate = %d, want >= 1", rows, got)
		}
		if row.Tokens <= 0 {
			t.Fatalf("row %d: fixture tokens = %d", rows, row.Tokens)
		}
		sumErr += math.Abs(float64(got-row.Tokens)) / float64(row.Tokens)
		if got < row.Tokens {
			under++
		}
		rows++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 141 {
		t.Fatalf("fixture rows = %d, want 141", rows)
	}
	if mape := sumErr / float64(rows); mape > 0.16 {
		t.Fatalf("MAPE = %.3f, want <= 0.16", mape)
	}
	if ratio := float64(under) / float64(rows); ratio > 0.08 {
		t.Fatalf("under-estimated = %.3f, want <= 0.08", ratio)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/tokenest -run 'TestEstimate|TestCalibration' -count=1`

Expected: FAIL because `EstimateAnthropic`, `DefaultSafetyFactor` and `estimateText` are undefined.

- [x] **Step 3: Implement the estimator**

Create `internal/tokenest/estimate.go` with:

```go
const DefaultSafetyFactor = 1.1

const (
	requestOverhead = 3
	messageOverhead = 4
	toolOverhead    = 8
	imageTokens     = 1600
	documentTokens  = 2000
)

// Classes: 0 letter, 1 digit, 2 space, 3 punct, 4 CJK, 5 other.
var charCoef = [6]float64{0.0817, 0.7438, 0.2590, 0.1416, 0.7617, 1.0000}
var runCoef  = [6]float64{0.4920, 0.0000, 0.0000, 0.9205, 0.0000, 0.3490}
```

- `EstimateAnthropic` trims the body, requires the first byte to be `{`, unmarshals into a struct with `System json.RawMessage`, `Messages []struct{ Content json.RawMessage }`, and `Tools []json.RawMessage`. It adds `requestOverhead`, the system and message content, `messageOverhead` per message, `toolOverhead` plus `estimateText(string(tool))` per tool, then `max(1, ceil(total*safetyFactor))`. A `safetyFactor <= 0` uses `DefaultSafetyFactor`.
- `classOf(r rune) int`: ASCII letters/digits/space/other; CJK ranges `0x4E00-0x9FFF`, `0x3040-0x30FF`, `0xAC00-0xD7AF`; everything else is class 5.
- `estimateText` accumulates per-class character and run counts and returns the weighted sum.
- `countContent(json.RawMessage) float64` handles a JSON string or an array of blocks.
- `countBlock(json.RawMessage) float64`: `text` → text; `thinking`/`redacted_thinking` → thinking; `tool_use` → name + input JSON; `tool_result` → nested content; `image` → `imageTokens`; `document` → `documentTokens`; default → the block's raw JSON.
- `EstimateAnthropic` returns an error for an empty body, a non-object body, or invalid JSON.

- [x] **Step 4: Run the focused tests**

Run: `GOWORK=off go test ./internal/tokenest -run 'TestEstimate|TestEstimateText' -count=1`

Expected: PASS. `TestCalibration` still fails because the fixture is missing.

- [x] **Step 5: Add the fixture generator and generate the fixture**

Create `scripts/gen-count-tokens-fixtures.py`:

```python
#!/usr/bin/env python3
"""Generate internal/tokenest/testdata/calibration.jsonl.

Provenance tool only; it needs the official DeepSeek V4 tokenizer and the
`tokenizers` Python package. It never runs in CI.
"""
import argparse, json, random, re
from pathlib import Path
from tokenizers import Tokenizer

ROOT = Path(__file__).resolve().parents[1]
FILES = [
    "README.md",
    "DOCKER.md",
    "internal/gateway/build.go",
    "internal/gateway/upstream.go",
    "internal/rewriter/rewriter.go",
    "../llm-api-converter/README.zh-CN.md",
    "../llm-api-converter/README.md",
]
TOOL_NAMES = ["get_weather", "search_web", "read_file", "run_command", "create_issue", "send_email"]
EMOJI = ["😀🎉🚀👨‍👩‍👧‍👦 表情符号测试。", "Mixed emoji 😀 and 中文 and English.", "🇨🇳🇺🇸🏳️‍🌈 flags and 👍🏽 skin tones"]


def chunks():
    out = []
    for rel in FILES:
        text = re.sub(r"\n{3,}", "\n\n", (ROOT / rel).read_text(encoding="utf-8"))
        for i in range(0, len(text), 700):
            chunk = text[i:i + 700]
            if len(chunk) >= 120:
                out.append(chunk)
    rnd = random.Random(7)
    for name in TOOL_NAMES:
        props = {
            f"param_{j}": {
                "type": ["string", "number", "boolean"][j % 3],
                "description": f"Parameter {j} for {name}",
            }
            for j in range(rnd.randint(2, 8))
        }
        out.append(json.dumps({
            "name": name,
            "description": f"Tool {name} used by the agent to perform an operation",
            "input_schema": {"type": "object", "properties": props, "required": list(props)[:2]},
        }, ensure_ascii=False))
    out.extend(EMOJI)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tokenizer", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    tok = Tokenizer.from_file(args.tokenizer)
    rows = chunks()
    with open(args.out, "w", encoding="utf-8") as f:
        for text in rows:
            f.write(json.dumps({"text": text, "tokens": len(tok.encode(text).ids)}, ensure_ascii=False) + "\n")
    print(f"wrote {len(rows)} rows to {args.out}")


if __name__ == "__main__":
    main()
```

Generate the fixture. This workspace already has the tokenizer at `/tmp/opencode/ds-tokenizer/deepseek_v4_tokenizer/tokenizer.json`; otherwise download `https://cdn.deepseek.com/api-docs/deepseek_v4_tokenizer.zip` and unzip it. Use:

```bash
python3 -m venv /tmp/ds-tokenizer-venv
/tmp/ds-tokenizer-venv/bin/pip install tokenizers
/tmp/ds-tokenizer-venv/bin/python scripts/gen-count-tokens-fixtures.py \
  --tokenizer /tmp/opencode/ds-tokenizer/deepseek_v4_tokenizer/tokenizer.json \
  --out internal/tokenest/testdata/calibration.jsonl
```

Expected: `wrote 141 rows to internal/tokenest/testdata/calibration.jsonl`.

Do not regenerate the fixture after later tasks change these files; it is a fixed regression corpus.

- [x] **Step 6: Run the calibration test**

Run: `GOWORK=off go test ./internal/tokenest -run TestCalibration -count=1 -v`

Expected: PASS with `fixture rows = 141`.

- [x] **Step 7: Run the package gate**

Run: `GOWORK=off go test ./internal/tokenest -count=1 && GOWORK=off go vet ./internal/tokenest`

Expected: PASS and clean vet.

- [x] **Step 8: Commit**

```bash
git add internal/tokenest scripts/gen-count-tokens-fixtures.py
git commit -m "feat(tokenest): add conservative anthropic token estimator"
```

- [x] **Step 9: Record the task**

Fill the Implementation record below with the code commit SHA from Step 8, the files changed, the commands run, and the results. Then:

```bash
git add docs/superpowers/plans/2026-11-06-count-tokens.md
git commit -m "docs(plan): record task 1"
```

**Implementation record:**
- Date/status: 2026-11-06 / complete
- Repository/commit: `llm-gw` `6e69ad93` (code); this docs commit follows
- Files changed: `internal/tokenest/estimate.go`, `internal/tokenest/estimate_test.go`, `internal/tokenest/testdata/calibration.jsonl`, `scripts/gen-count-tokens-fixtures.py`
- Tests/commands/results:
  - RED: `GOWORK=off go test ./internal/tokenest -run 'TestEstimate|TestCalibration' -count=1` → build failed with `undefined: EstimateAnthropic` and `undefined: DefaultSafetyFactor`.
  - GREEN (non-calibration): the same command after the implementation → `ok`, with `TestCalibration` still missing its fixture.
  - Fixture: `scripts/gen-count-tokens-fixtures.py --tokenizer /tmp/opencode/ds-tokenizer/deepseek_v4_tokenizer/tokenizer.json --out internal/tokenest/testdata/calibration.jsonl` → `wrote 141 rows`; `testdata/` was created first with `mkdir -p`.
  - Calibration: `GOWORK=off go test ./internal/tokenest -run TestCalibration -count=1 -v` → PASS; measured MAPE 14.12%, under-estimated 4.0%.
  - Package gate: `GOWORK=off go test ./internal/tokenest -count=1` → PASS; `GOWORK=off go vet ./internal/tokenest` → clean.
- Review findings/fixes: none; the only adjustment was creating `testdata/` before generation.
- Deferred/limitations: the heuristic is calibrated on DeepSeek V4 text, not full chat templates; images use a fixed 1600 and documents a fixed 2000; no runtime tokenizer.

---

### Task 2: Compile the per-route `count_tokens` policy

**Files:**
- Create: `internal/gateway/count_tokens_policy.go`
- Create: `internal/gateway/count_tokens_policy_test.go`
- Modify: `internal/gateway/build.go`
- Modify: `internal/gateway/gateway.go`

**Interfaces:**
- Consumes: `modelGlobPattern(match []string) (pattern string, catchAll bool, err error)` from `protocol.go`; `nodePlan`, `routePlan`, `nodeTargets`, `upstreamTarget` from `build.go`/`upstream.go`.
- Produces:
  - `type countTokensPolicy struct { entries []countTokensEntry }`
  - `type countTokensEntry struct { matcher *regexp.Regexp; estimate bool }`
  - `func (p *countTokensPolicy) estimateFor(model string) bool`
  - `func routeNeedsCountTokensEstimate(plans []nodePlan) bool`
  - `func buildCountTokensPolicy(cfg *gwconfig.Config, plans map[string]*routePlan) (*countTokensPolicy, error)`
  - `routePlan.countTokensEstimate bool`
  - `buildHopGroup(...) (hop.Hop, []string, *countTokensPolicy, error)`
  - `Gateway.countTokens atomic.Pointer[countTokensPolicy]`

- [ ] **Step 1: Write the failing tests**

Create `internal/gateway/count_tokens_policy_test.go`:

```go
package gateway

import (
	"regexp"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

func TestRouteNeedsCountTokensEstimate(t *testing.T) {
	anthropic := nodeTargets{defaultTarget: upstreamTarget{api: provider.Anthropic}}
	chat := nodeTargets{defaultTarget: upstreamTarget{api: provider.Chat}}
	gemini := nodeTargets{defaultTarget: upstreamTarget{api: provider.Gemini}}
	anthropicVariant := nodeTargets{
		defaultTarget: upstreamTarget{api: provider.Chat},
		variants: map[provider.API]upstreamTarget{
			provider.Anthropic: {api: provider.Anthropic},
		},
	}

	cases := []struct {
		name  string
		plans []nodePlan
		want  bool
	}{
		{"empty", nil, false},
		{"all anthropic", []nodePlan{{targets: anthropic}}, false},
		{"chat", []nodePlan{{targets: chat}}, true},
		{"gemini", []nodePlan{{targets: gemini}}, true},
		{"anthropic variant wins", []nodePlan{{targets: anthropicVariant}}, false},
		{"mixed", []nodePlan{{targets: anthropic}, {targets: chat}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeNeedsCountTokensEstimate(tc.plans); got != tc.want {
				t.Fatalf("routeNeedsCountTokensEstimate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountTokensPolicyPrecedence(t *testing.T) {
	p := &countTokensPolicy{entries: []countTokensEntry{
		{matcher: regexp.MustCompile(`(?i)^claude-.*$`), estimate: false},
		{matcher: nil, estimate: true},
	}}
	if p.estimateFor("claude-opus-4-5") {
		t.Fatal("claude model should pass through")
	}
	if !p.estimateFor("gpt-4") {
		t.Fatal("non-claude model should estimate")
	}
	if !p.estimateFor("") {
		t.Fatal("catch-all should match an empty model")
	}

	empty := &countTokensPolicy{}
	if empty.estimateFor("gpt-4") {
		t.Fatal("empty policy should pass through")
	}
	var nilPolicy *countTokensPolicy
	if nilPolicy.estimateFor("gpt-4") {
		t.Fatal("nil policy should pass through")
	}
}

func TestBuildCountTokensPolicyFromConfig(t *testing.T) {
	cfg := &gwconfig.Config{Entrypoints: []gwconfig.Entrypoint{
		{Name: "anthropic", Match: []string{"claude-*"}, Route: "a"},
		{Name: "rest", Match: []string{"*"}, Route: "r"},
	}}
	plans := map[string]*routePlan{
		"a": {countTokensEstimate: false},
		"r": {countTokensEstimate: true},
	}
	p, err := buildCountTokensPolicy(cfg, plans)
	if err != nil {
		t.Fatal(err)
	}
	if p.estimateFor("claude-opus-4-5") {
		t.Fatal("claude model should pass through")
	}
	if !p.estimateFor("gpt-4") {
		t.Fatal("non-claude model should estimate")
	}
}

func TestGatewayCompilesCountTokensPolicy(t *testing.T) {
	up, _ := captureUpstream(t)

	chat := startGateway(t, matrixConfig(up.URL, provider.Chat))
	if p := chat.countTokens.Load(); p == nil || !p.estimateFor("claude-opus-4-5") {
		t.Fatal("chat route should estimate")
	}

	anth := startGateway(t, matrixConfig(up.URL, provider.Anthropic))
	if p := anth.countTokens.Load(); p == nil || p.estimateFor("claude-opus-4-5") {
		t.Fatal("anthropic route should pass through")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestRouteNeedsCountTokensEstimate|TestCountTokensPolicy|TestBuildCountTokensPolicy|TestGatewayCompilesCountTokensPolicy' -count=1`

Expected: FAIL because the policy types and `Gateway.countTokens` are undefined.

- [ ] **Step 3: Implement the policy file**

Create `internal/gateway/count_tokens_policy.go`:

- `estimateFor(model string) bool` returns the first matching entry in order; a nil matcher is a catch-all; a nil or empty policy returns false.
- `routeNeedsCountTokensEstimate(plans []nodePlan) bool` uses the Anthropic variant when present else the default target, and returns true for the first non-`provider.Anthropic` target.
- `buildCountTokensPolicy(cfg, plans)` compiles `modelGlobPattern` for each entrypoint in order, looks up `plans[e.Route]`, sets `matcher` only when the pattern is not a catch-all, and returns an error naming the entrypoint or route when either is missing.

- [ ] **Step 4: Wire the policy into route compilation**

In `internal/gateway/build.go`:

- Add `countTokensEstimate bool` to `routePlan`.
- After `plans` is built in `buildRoutePlan`, set `plan.countTokensEstimate = routeNeedsCountTokensEstimate(plans)`.
- Change `buildHopGroup` to return `(hop.Hop, []string, *countTokensPolicy, error)`. Build the policy after the route plans are complete with `buildCountTokensPolicy(cfg, routePlans)` and return it. Every error return gains a `nil` policy.

- [ ] **Step 5: Store the policy on the gateway**

In `internal/gateway/gateway.go`:

- Add `countTokens atomic.Pointer[countTokensPolicy]` to `Gateway` and import `sync/atomic`.
- In `apply`, receive the policy from `buildHopGroup` and call `g.countTokens.Store(policy)` after the hop swap.

- [ ] **Step 6: Run the focused tests**

Run: `GOWORK=off go test ./internal/gateway -run 'TestRouteNeedsCountTokensEstimate|TestCountTokensPolicy|TestBuildCountTokensPolicy|TestGatewayCompilesCountTokensPolicy' -count=1`

Expected: PASS.

- [ ] **Step 7: Run the gateway package**

Run: `GOWORK=off go test ./internal/gateway -count=1`

Expected: PASS (the existing `count_tokens is not converted` test still passes because the wrapper does not exist yet).

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/count_tokens_policy.go internal/gateway/count_tokens_policy_test.go internal/gateway/build.go internal/gateway/gateway.go
git commit -m "feat(gateway): compile count_tokens estimate policy"
```

- [ ] **Step 9: Record the task**

Fill the Implementation record below with the code commit SHA from Step 8, the files changed, the commands run, and the results. Then:

```bash
git add docs/superpowers/plans/2026-11-06-count-tokens.md
git commit -m "docs(plan): record task 2"
```

**Implementation record:**
- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands/results:
- Review findings/fixes:
- Deferred/limitations:

---

### Task 3: Intercept and answer `count_tokens`

**Files:**
- Create: `internal/gateway/count_tokens.go`
- Create: `internal/gateway/count_tokens_test.go`
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/protocol_test.go`

**Interfaces:**
- Consumes: `tokenest.EstimateAnthropic`, `tokenest.DefaultSafetyFactor`, `countTokensPolicy.estimateFor`, `Gateway.countTokens`.
- Produces:
  - `func newCountTokensHandler(next handler.Handler, policy *atomic.Pointer[countTokensPolicy], log logger.Logger) *countTokensHandler`
  - `countTokensHandler` implements `handler.Handler` and delegates `Init` to `next`.

- [ ] **Step 1: Write the failing integration tests**

Create `internal/gateway/count_tokens_test.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
	"github.com/go-gost/llm-gw/internal/stats"
)

const countTokensBody = `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`

func postGateway(t *testing.T, gw *Gateway, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func inputTokens(t *testing.T, body string) int {
	t.Helper()
	var v struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body = %s: %v", body, err)
	}
	return v.InputTokens
}

func TestCountTokensEstimatesForChatTarget(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensPassesThroughForAnthropicTarget(t *testing.T) {
	got := &capturedRequest{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.body = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"input_tokens":42}`)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, matrixConfig(up.URL, provider.Anthropic))
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n != 42 {
		t.Fatalf("input_tokens = %d, want 42", n)
	}
	if got.path != "/v1/messages/count_tokens" {
		t.Fatalf("upstream path = %q", got.path)
	}
	if got.body != countTokensBody {
		t.Fatalf("upstream body changed: %s", got.body)
	}
}

func TestCountTokensMixedRouteEstimates(t *testing.T) {
	chatUp, chatGot := captureUpstream(t)
	anthUp, anthGot := captureUpstream(t)

	cfg := matrixConfig(chatUp.URL, provider.Chat)
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name: "anth", Type: "generic", Keys: []string{"k"}, Anthropic: anthUp.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "anth", Provider: "anth", Model: "deepseek-v4-flash"})
	cfg.Routes[0].Nodes = append(cfg.Routes[0].Nodes, "anth")

	gw := startGateway(t, cfg)
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if chatGot.path != "" || anthGot.path != "" {
		t.Fatalf("upstream was called: chat=%q anth=%q", chatGot.path, anthGot.path)
	}
}

func TestCountTokensQueryString(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, body := postGateway(t, gw, "/v1/messages/count_tokens?beta=true", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensInvalidBodyPassesThroughOnChatTarget(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, _ := postGateway(t, gw, "/v1/messages/count_tokens", "not-json")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got.body != "not-json" {
		t.Fatalf("upstream body = %q, want the invalid body unchanged", got.body)
	}
}

func TestCountTokensSequentialRequests(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	for i := 0; i < 2; i++ {
		status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
		if status != http.StatusOK {
			t.Fatalf("request %d: status = %d body = %s", i, status, body)
		}
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensWritesNoStats(t *testing.T) {
	up, _ := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
	cfg.Stats = gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")}
	gw := startGateway(t, cfg)

	if status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody); status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	rows, err := gw.Stats().Query(context.Background(), stats.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("stats rows = %d, want 0", len(rows))
	}
}

func TestCountTokensReloadFlipsPolicy(t *testing.T) {
	chatUp, chatGot := captureUpstream(t)
	anthGot := &capturedRequest{}
	anthUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		anthGot.path, anthGot.body = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"input_tokens":42}`)
	}))
	t.Cleanup(anthUp.Close)

	gw := startGateway(t, matrixConfig(chatUp.URL, provider.Chat))
	if status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody); status != http.StatusOK {
		t.Fatalf("before reload: status = %d body = %s", status, body)
	}
	if chatGot.path != "" {
		t.Fatalf("before reload: upstream was called: %s", chatGot.path)
	}

	if err := gw.Reload(matrixConfig(anthUp.URL, provider.Anthropic)); err != nil {
		t.Fatal(err)
	}
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("after reload: status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n != 42 {
		t.Fatalf("after reload: input_tokens = %d, want 42", n)
	}
	if anthGot.path != "/v1/messages/count_tokens" {
		t.Fatalf("after reload: upstream path = %q", anthGot.path)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestCountTokens(Estimates|PassesThrough|MixedRoute|QueryString|InvalidBody|Sequential|Reload|NoStats)' -count=1`

Expected: FAIL because the wrapper does not exist; Chat `count_tokens` reaches the mock upstream and the Anthropic pass-through assertions fail at the local 404/mock response.

- [ ] **Step 3: Implement the wrapper**

Create `internal/gateway/count_tokens.go`:

- Constants: `countTokensPath = "/v1/messages/count_tokens"`, `countTokensBodyLimit = 32 << 20`, `countTokensReadTimeout = 15 * time.Second`.
- `countTokensHandler{next handler.Handler; policy *atomic.Pointer[countTokensPolicy]; log logger.Logger}`.
- `Init(md)` delegates to `next.Init(md)`.
- `Handle(ctx, conn, opts...)`:
  1. `defer conn.Close()`.
  2. Set a read deadline, `br := bufio.NewReader(conn)`, `Peek(5)`. If the bytes are not `POST `, clear the deadline and delegate through a `bufferedConn` (a `net.Conn` whose `Read` drains `br`).
  3. `ReadSlice('\n')`. If it is not the `count_tokens` path after stripping `?`, clear the deadline, replay the consumed line plus `br` through a `replayConn`, and delegate.
  4. Clear the deadline. Rebuild the request with `http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(line), br)))`.
  5. If `Expect: 100-continue` is present, write `HTTP/1.1 100 Continue\r\n\r\n`.
  6. Read the body with `io.ReadAll(io.LimitReader(req.Body, countTokensBodyLimit+1))`. Over the limit returns 413 with the Anthropic error envelope.
  7. If `Content-Encoding` is set and not `identity`, replay and delegate.
  8. `tokenest.EstimateAnthropic(body, tokenest.DefaultSafetyFactor)`. On error, replay and delegate.
  9. Unmarshal the top-level `model`; if `policy.Load().estimateFor(model)` is false, replay and delegate.
  10. Log at debug: model, tokens, bytes. Write `200`, `application/json`, `Connection: close`, `{"input_tokens": N}` and return.
- Replay: set `req.Body` from the captured bytes, `req.ContentLength`, clear `req.TransferEncoding`, `req.Write` into a buffer, and delegate through a `replayConn` over the buffer plus `br`.
- `isCountTokensLine(line []byte) bool` parses `strings.Fields`, requires `POST`, strips the query, and compares with `countTokensPath`.
- Response helpers set `ProtoMajor/Minor` from the request, `Content-Type: application/json`, `Connection: close`, `ContentLength`, and use `resp.Write(conn)`.
- The error helper writes `{"type":"error","error":{"type":"invalid_request_error","message":...}}`.

- [ ] **Step 4: Wire the wrapper into the gateway**

In `internal/gateway/gateway.go`:

- Replace the direct `h := forwardlocal.NewHandler(opts...)` wiring so the wrapper is created after `g`:
  - `base := forwardlocal.NewHandler(opts...)`
  - `g := &Gateway{...}`
  - `h := newCountTokensHandler(base, &g.countTokens, log)`
  - `h.Init(xmetadata.NewMetadata(...))`
  - `fwd, ok := base.(handler.Forwarder)`
  - `g.handler = h`, `g.forwarder = fwd`, and `g.svc = xservice.NewService("llm-gw", ln, h, ...)`.
- `Gateway.countTokens` was added in Task 2; `New` calls `apply` before `Start`, so the policy is loaded before the first request.

- [ ] **Step 5: Run the focused integration tests**

Run: `GOWORK=off go test ./internal/gateway -run 'TestCountTokens(Estimates|PassesThrough|MixedRoute|QueryString|InvalidBody|Sequential|Reload|NoStats)' -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Update the old protocol matrix assertion**

In `internal/gateway/protocol_test.go`, replace the `count_tokens is not converted` subtest with:

```go
t.Run("count_tokens is estimated for a chat target", func(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`
	status, resp := postGateway(t, gw, "/v1/messages/count_tokens", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, resp)
	}
	if n := inputTokens(t, resp); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
})
```

- [ ] **Step 7: Run the gateway package and the existing invalid-body test**

Run: `GOWORK=off go test ./internal/gateway -count=1`

Expected: PASS, including `TestCountTokensInvalidBodyPassesThrough` and `TestProtocolMatrix`.

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/count_tokens.go internal/gateway/count_tokens_test.go internal/gateway/gateway.go internal/gateway/protocol_test.go
git commit -m "feat(gateway): estimate count_tokens for non-anthropic routes"
```

- [ ] **Step 9: Record the task**

Fill the Implementation record below with the code commit SHA from Step 8, the files changed, the commands run, and the results. Then:

```bash
git add docs/superpowers/plans/2026-11-06-count-tokens.md
git commit -m "docs(plan): record task 3"
```

**Implementation record:**
- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands/results:
- Review findings/fixes:
- Deferred/limitations:

---

### Task 4: Document behavior and run the full gate

**Files:**
- Modify: `README.md`
- Modify: this plan (implementation records)
- Create: `/root/code/go-gost/.memory/notes/llm-gw-count-tokens.md`
- Modify: `/root/code/go-gost/.memory/MEMORY.md`

**Interfaces:**
- Consumes: the behavior implemented in Tasks 1–3.
- Produces: user-facing documentation and the final verification record.

- [ ] **Step 1: Add the README section**

Add a `## Token counting` section after `## Request correlation and capture` and before `## Provider usage and balances`:

```markdown
## Token counting

Claude Code calls `POST /v1/messages/count_tokens` to size tool definitions,
skills and conversation fragments.

- Routes whose target API is Anthropic pass the request through unchanged.
- Routes whose target API is Chat Completions, Responses or Gemini are
  answered locally with a conservative estimate. No upstream request is made,
  and the value is an approximation calibrated on DeepSeek V4; it is
  intentionally biased high so context compaction is not delayed.
- A route that mixes Anthropic and non-Anthropic nodes estimates for all
  `count_tokens` requests.
- Locally answered requests are not recorded in stats or captures.
```

- [ ] **Step 2: Run the full Go gate**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
git diff --check
```

Expected: all commands pass and `git diff --check` is silent. No web gate is required because no web source changed.

- [ ] **Step 3: Commit the documentation**

```bash
git add README.md
git commit -m "docs: describe local token counting"
```

- [ ] **Step 4: Record the memory note**

Create `/root/code/go-gost/.memory/notes/llm-gw-count-tokens.md` with the durable facts: the handler-wrapper interception, the per-route `auto` policy (all Anthropic → pass through, otherwise estimate), the calibrated heuristic and its gate, the no-stats/no-capture rule, and the deferred items from the spec. Add a link in `.memory/MEMORY.md`.

Commit in the memory repository:

```bash
cd /root/code/go-gost
git add .memory/notes/llm-gw-count-tokens.md .memory/MEMORY.md
git commit -m "docs(memory): record local count_tokens estimation"
```

- [ ] **Step 5: Record the task and commit the plan**

Fill this task's Implementation record below with its date, repository/commit, files, commands/results, review findings and deferrals. Then:

```bash
cd /root/code/go-gost/llm-gw
git add docs/superpowers/plans/2026-11-06-count-tokens.md
git commit -m "docs(plan): record count_tokens implementation"
```

**Implementation record:**
- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands/results:
- Review findings/fixes:
- Deferred/limitations:

---

## Final verification and whole-branch review

After Task 4, run one fresh whole-branch review before declaring the branch done.

1. Generate the review package from the branch base:

```bash
cd /root/code/go-gost/llm-gw
mb=$(git merge-base main HEAD)
git diff --stat "$mb"..HEAD
git diff "$mb"..HEAD > /tmp/llm-gw-count-tokens-review.diff
```

2. Dispatch a fresh reviewer with the spec, this plan, and `/tmp/llm-gw-count-tokens-review.diff`. Ask for Critical/Important/Minor findings and a merge verdict. The reviewer is read-only.

3. Fix every Critical and Important finding in one pass, add a regression test for each, and rerun the full gate from Task 4.

4. Record the review findings, fixes, and final commit SHA in this plan, then commit the record.

5. Do not push.
