# Google Gemini provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gemini` as a first-class cuttle provider type so Claude Code (Anthropic inbound) and OpenAI/Chat clients reach the Gemini API through the AI-Studio-style compatible endpoint, while native Gemini clients keep their current entrypoint behavior.

**Architecture:** A new vendor adapter (`internal/provider/adapter_gemini.go`) plus a tiny optional catalog-selection hook (`catalogAPIs() []API`) that `baseProvider.listAPIs()` honors, defaulting to current behavior. Catalog reads the **native** `GET /v1beta/models` page (`pageSize=1000`, `pageToken` loop, `supportedGenerationMethods` filter). Both preset base URLs are wired by a server preset; the compatible `/v1beta/openai` base is an upstream target only — never fetched for the catalog.

**Tech Stack:** Go (module `github.com/ginuerzh/cuttle`, built with `GOWORK=off`), React + Vite (`web/`), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-16-gemini-provider-design.md` (read it; this plan argues from it).

## Global Constraints

- English only in code, comments, tests and docs; bilingual wording belongs to the web console i18n layer.
- No secrets in the repo; keys arrive via `${ENV}` in example.yaml or the console.
- `config.providerTypes`, `provider.New`, the web `PROVIDER_TYPES` list and `internal/admin/presets.go` must stay in sync — the invariant family named "four registries".
- Non-`generic` providers keep preset base URLs immutable after creation; the UI does not need to special-case gemini for that (handled by `isBaseURLLocked`).
- Provider type is immutable at creation; the preset ships exactly two base URLs and no new fields.
- Wire-up must not change the gateway: Anthropic/Chat/Gemini conversion paths already exist; this plan adds no gateway branch for gemini traffic.
- Git safety in this repo: commit per task, never push unless the user asks.
- Follow the existing adapter pattern (zero-size struct, smaller file, English doc comments above each exported symbol).
- Implementation records go in this plan file after each task; one commit per task carrying that task's record.

## Review Focus

The five input classes a person using this software will hit that no existing test exercises, most likely first; each line names the task that owns the pin:

1. **A `gemini` provider whose catalog fails to list** (e.g. native endpoint 500s) — the operator expects the provider to either show last-good models or a clear catalog error, never a silently chat-compat list. → Task 1 (`TestModelsGeminiPartialFailureFailsAll` + `TestModelsGeminiCatalogFromNativeOnly`).
2. **Anthropic client pinned with `node.api: gemini`** — the known streaming breaker; must not become the default and must be documented. → Task 5 (README + example.yaml note) and the gateway path assertion in Task 5.
3. **Multi-page catalog where only the first page succeeds** — failure in page 3 must surface as "catalog error", not as a 2-page truncated list. → Task 1 (page-bound + all-or-nothing rule).
4. **Embedding-only models (`gemini-embedding-*`) in the catalog** — they 400 at request time; the catalog must exclude them. → Task 1 (`supportedGenerationMethods` filter test).
5. **Console "verify" button on the gemini preset** — probes the Chat base with Bearer (conscious decision, chat-first); the native endpoint is never exercised. → Task 2 (`TestProbeUsesChatBaseForGeminiPreset` pin).

---

### Task 1: Provider type `gemini` — adapter, catalog hook, paginated native catalog

**Files:**
- Create: `internal/provider/adapter_gemini.go`
- Create: `internal/provider/adapter_gemini_test.go`
- Modify: `internal/config/config.go` — `providerTypes` gains `"gemini"`
- Modify: `internal/provider/provider.go` — factory `case "gemini"` + optional hint interface
- Modify: `internal/provider/models.go` — hint-honoring `listAPIs`, Gemini pagination + filter
- Modify: `internal/provider/provider_test.go` — factory list gains `"gemini"`
- Modify: `internal/provider/models_test.go` — catalog fixtures

**Interfaces:**
- Consumes: `DefaultProviderTestFixtures` pattern from `models_test.go` (`newModelServer(t, handler)`, `mustProvider(t, cfg)`).
- Produces (used by later tasks): `New` accepts `type: gemini`; `UsageSupport("gemini")` contract point in Task 2; `Preset{Type:"gemini"}` wired in Task 3.

- [ ] **Step 1: Register `gemini` in the config package**

In `internal/config/config.go`, add `"gemini"` to the `providerTypes` list (after `"openrouter"`, before `"generic"`). No other config changes.

- [ ] **Step 2: Write the failing adapter tests**

In `internal/provider/adapter_gemini_test.go`:

```go
func TestGeminiClassifyAndResolve(t *testing.T) {
	p := mustProvider(t, config.Provider{Name: "gm", Type: "gemini", Gemini: "https://generativelanguage.googleapis.com/v1beta", Chat: "https://generativelanguage.googleapis.com/v1beta/openai"})
	m, ok := p.Resolve("gemini-2.5-pro")
	if !ok { t.Fatal("Resolve: ok = false, want true") }
	if !slices.Equal(m.APIs, []API{Chat, Gemini}) { t.Fatalf("APIs = %v, want [chat gemini]", m.APIs) }
	if _, ok := p.Resolve(""); ok { t.Fatal("Resolve(\"\"): ok = true, want false") }
}

func TestGeminiAuth(t *testing.T) {
	p := mustProvider(t, config.Provider{Name: "gm", Type: "gemini", Chat: "https://x.example/v1beta/openai", Gemini: "https://x.example/v1beta"})
	if a := p.Auth(Chat); a.Header != "Authorization" || a.Prefix != "Bearer " { t.Fatalf("chat auth = %+v", a) }
	if a := p.Auth(Gemini); a.Header != "x-goog-api-key" || a.Prefix != "" { t.Fatalf("gemini auth = %+v", a) }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/provider/ -run 'TestGeminiClassifyAndResolve|TestGeminiAuth' -count=1 -v`
Expected: FAIL — `unknown type "gemini"`.

- [ ] **Step 4: Create `internal/provider/adapter_gemini.go`**

```go
// geminiAdapter covers the Google AI Studio / Gemini API key surface.
// Static classification advertises the OpenAI-compatible Chat endpoint and the
// native Gemini endpoint. The compatible endpoint is the target for clients
// that cannot speak Gemini SSE; the native endpoint serves Gemini clients.
type geminiAdapter struct{}

func (geminiAdapter) classify(id string) []API {
	if strings.TrimSpace(id) == "" { return nil }
	return []API{Chat, Gemini}
}

func (geminiAdapter) auth(api API) config.Auth { return defaultAuth(api) }

func (geminiAdapter) listAll() bool { return false }

func (geminiAdapter) keylessCatalog() bool { return false }

func (geminiAdapter) catalogAPIs() []API { return []API{Gemini} }
```

- [ ] **Step 5: Wire the factory and the catalog hint in `internal/provider/provider.go`**

Add next to the `adapter` interface:

```go
// catalogAPIsHint is implemented by adapters that must catalog from a
// different API than the default chat-first preference. baseProvider.listAPIs
// honors it when present, falling back to the existing logic.
type catalogAPIsHint interface{ catalogAPIs() []API }
```

Add `case "gemini": a = geminiAdapter{}` immediately after the `openrouter` case. `models.go` references this named interface (do not duplicate it as an anonymous `interface{ catalogAPIs() []API }`).

- [ ] **Step 6: Implement the hook + pagination/filter in `internal/provider/models.go`**

`listAPIs` becomes:

```go
if h, ok := p.adapter.(catalogAPIsHint); ok {
	if hint := h.catalogAPIs(); len(hint) > 0 {
		configured := p.configuredAPIs()
		var out []API
		for _, api := range configured {
			for _, want := range hint {
				if api == want { out = append(out, api) }
			}
		}
		if len(out) > 0 { return out }
	}
}
```

Place this block **before** the existing `!p.adapter.listAll()` branch (fallthrough to today's logic when absent or the intersection is empty).

Gemini pagination — extract the Gemini arm of `decodeModels` into:

```go
func decodeGeminiPage(r io.Reader) (items []modelItem, nextPageToken string, err error)
```

which then gets reused by `decodeModels` (passing through to the old shape, `nextPageToken` ignored) **and** by a new `(p *baseProvider) fetchGeminiModels` that loops `?pageSize=1000&pageToken=…` (URL-escape the token; page bound 100; any page error fails the whole listing). `fetchModels` dispatches `if api == Gemini { return p.fetchGeminiModels(ctx, base) }` before its single-request path.

Extend `modelListJSON` with `NextPageToken string \`json:"nextPageToken,omitempty"\``, extend `geminiModelJSON` with `SupportedGenerationMethods []string \`json:"supportedGenerationMethods,omitempty"\``, and drop every model whose `supportedGenerationMethods` is non-empty and does not contain `generateContent`.

- [ ] **Step 7: Catalog tests in `internal/provider/models_test.go`**

```go
func TestModelsGeminiCatalogFromNativeOnly(t *testing.T) {
	native := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("pageToken") {
		case "":
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}],"nextPageToken":"p2"}`)
		default: // "p2"
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash","supportedGenerationMethods":["generateContent"]}]}`)
		}
	})
	compat := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("compatible catalog endpoint must not be queried")
	})
	p := mustProvider(t, config.Provider{Name: "gm", Type: "gemini", Chat: compat.URL, Gemini: native.URL, Keys: []config.ProviderKey{{Name: "k", Key: "sk-test"}}})
	models, err := p.Models(context.Background())
	if err != nil { t.Fatal(err) }
	ids := []string{}; for _, m := range models { ids = append(ids, m.ID) }
	if !slices.Equal(ids, []string{"gemini-2.5-pro", "gemini-2.5-flash"}) {
		t.Fatalf("ids = %v", ids)
	}
}

func TestModelsGeminiPageTokenForwarded(t *testing.T) {
	// page 1 checks r.URL.Query().Get("pageSize") == "1000"; page 2 checks pageToken == "abc".
}

func TestModelsGeminiPartialFailureFailsAll(t *testing.T) {
	// first page 200 with nextPageToken="abc", second page 500 → p.Models returns error, zero models returned.
}

func TestModelsGeminiFiltersEmbeddingOnly(t *testing.T) {
	fmt.Fprint(w, `{"models":[
		{"name":"models/gemini-embedding-exp-001","supportedGenerationMethods":["embedContent"]},
		{"name":"models/imagen-4.0"}
	]}`)
}
```

The Gemini 2.5 filter case: `imagen-4.0` has no `supportedGenerationMethods` → keep it (non-empty methods rule); `gemini-embedding` has `embedContent` only → dropped. Adjust the `supportedGenerationMethods` JSON in the fixture to what you assert.

Also update `internal/provider/provider_test.go:19` — add `"gemini"` to the factory list.

- [ ] **Step 8: Run tests and vet**

Run: `GOWORK=off go test ./internal/provider/ -count=1 && GOWORK=off go vet ./internal/provider/ ./internal/config/`
Expected: PASS, no vet findings. Existing `TestModelsGeminiNamesAreStripped` / `TestModelsGeminiLimits` must stay green (single-page Gemini fixture, no `nextPageToken`).

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/provider/*.go
git commit -m "feat(provider): add gemini vendor type with paginated native catalog"
```

Append the Task 1 implementation record to this plan before committing (see Implementation record format).

---

### Task 2: Admin probe pin + Gemini usage note

**Files:**
- Modify: `internal/admin/probe_test.go` — new test
- Modify: `internal/provider/usage.go` — `GeminiUsageNote` + `UsageSupport("gemini")`

- [ ] **Step 1: Write the probe pin test**

In `internal/admin/probe_test.go`:

```go
func TestProbeUsesChatBaseForGeminiPreset(t *testing.T) {
	p := config.Provider{
		Name: "gm", Type: "gemini",
		Chat:   "https://generativelanguage.googleapis.com/v1beta/openai",
		Gemini: "https://generativelanguage.googleapis.com/v1beta",
		Keys:   []config.ProviderKey{{Name: "k", Key: "sk-test"}},
	}
	base, api, _, _, ok := probeTarget(p)
	if !ok { t.Fatal("probeTarget: ok = false") }
	if api != provider.Chat || base != p.Chat { t.Fatalf("probe = (%s, %s), want chat base with Bearer", base, api) }
}
```

- [ ] **Step 2: Add the usage note**

In `internal/provider/usage.go`, add next to `OpenCodeZenUsageNote`:

```go
// GeminiUsageNote explains why there is no balance or quota endpoint to show.
const GeminiUsageNote = "Gemini API usage is billed per Google Cloud. AI Studio plan credits only apply inside AI Studio; there is no per-key balance API."
```

`UsageSupport` gains `case "gemini": return false, GeminiUsageNote`.

- [ ] **Step 3: Pin the note**

In `internal/provider/usage_test.go`, next to `TestUsageOpenRouterSupport`:

```go
func TestUsageGeminiSupport(t *testing.T) {
	if supported, note := UsageSupport("gemini"); supported || note != GeminiUsageNote {
		t.Fatalf("UsageSupport(gemini) = %v, %q", supported, note)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `GOWORK=off go test ./internal/admin/ ./internal/provider/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/probe_test.go internal/provider/usage.go internal/provider/usage_test.go
git commit -m "feat(admin): gemini preset probe pin and usage note"
```

Append the Task 2 record to this plan before committing.

---

### Task 3: Admin server preset + preset test 5→6

**Files:**
- Modify: `internal/admin/presets.go` — add gemini preset
- Modify: `internal/admin/server_test.go` — `TestPresetsMatchSpec` 5→6

- [ ] **Step 1: Write the failing server test expectation**

Update `TestPresetsMatchSpec` in `internal/admin/server_test.go`:
- `if len(got) != 5` → `6`
- add to `checks`: `{"gemini", "gemini", "https://generativelanguage.googleapis.com/v1beta/openai", "", "", "https://generativelanguage.googleapis.com/v1beta"}`
- the trailing "opencode-go.Chat != opencode-zen.Chat" sanity check stays.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/admin/ -run TestPresetsMatchSpec -count=1 -v`
Expected: FAIL (got 5, want 6).

- [ ] **Step 3: Add the preset**

Append to `presets` in `internal/admin/presets.go`:

```go
{
	Name:   "gemini",
	Type:   "gemini",
	Chat:   "https://generativelanguage.googleapis.com/v1beta/openai",
	Gemini: "https://generativelanguage.googleapis.com/v1beta",
},
```

- [ ] **Step 4: Run tests**

Run: `GOWORK=off go test ./internal/admin/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/presets.go internal/admin/server_test.go
git commit -m "feat(admin): add gemini provider preset"
```

Append the Task 3 record to this plan before committing.

---

### Task 4: Web preset wiring + dist rebuild

**Files:**
- Modify: `web/src/pages/presets.ts` — `PROVIDER_TYPES` gains `'gemini'`
- Modify: `web/src/pages/presets.test.ts` — assertions

- [ ] **Step 1: Write the failing web test**

In `web/src/pages/presets.test.ts`:
- `describe('PROVIDER_TYPES')` block: rename to "includes OpenRouter and Gemini" and add `expect(PROVIDER_TYPES).toContain('gemini');`
- Add, next to `opencodeZen`, a `gemini` fixture:
  ```ts
  const gemini: Preset = {
    name: 'gemini', type: 'gemini',
    chat: 'https://generativelanguage.googleapis.com/v1beta/openai',
    gemini: 'https://generativelanguage.googleapis.com/v1beta',
  };
  ```
  and one test asserting `presetFormValues(gemini)` fills exactly `type: 'gemini'`, `chat`, `gemini`, and empty `responses`/`anthropic`. (`Preset` allows all API URLs optional, so no cast is needed.)

- [ ] **Step 2: Run web test to verify it fails**

Run: `cd web && npm test -- presets.test.ts 2>&1 | tail -20`
Expected: FAIL (`gemini` missing from `PROVIDER_TYPES` / fixture missing).

- [ ] **Step 3: Do the edits**

`web/src/pages/presets.ts`: `export const PROVIDER_TYPES = ['deepseek', 'opencode-go', 'opencode-zen', 'commandcode', 'openrouter', 'gemini', 'generic'] as const;` (gemini before `generic`).

- [ ] **Step 4: Web gates**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS; `internal/admin/dist/` regenerated.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/presets.ts web/src/pages/presets.test.ts internal/admin/dist
git commit -m "feat(web): accept gemini provider type"
```

Append the Task 4 record to this plan before committing.

---

### Task 5: Gateway capability pin + example.yaml + README

**Files:**
- Modify: `internal/gateway/provider_capabilities_test.go` — Gemini-type auto→chat case
- Modify: `example.yaml` — commented gemini block
- Modify: `README.md` — provider table row, Gemini subsection, balance bullet

- [ ] **Step 1: Add the gateway pin**

In `internal/gateway/provider_capabilities_test.go`, add after the existing `/v1/chat/completions`-hit cases (reuse `newCapabilityUpstream`):

```go
t.Run("gemini type auto falls back to compatible chat base", func(t *testing.T) {
	// provider type=gemini, chat=<upstream>/v1beta/openai, gemini=<upstream>/v1beta,
	// node model gemini-2.5-pro, no node.api pin; POST /v1/messages with stream:true.
	// Assert upstream hit path == "/v1beta/openai/chat/completions" — not the gemini
	// native path — and that the response content-type is text/event-stream.
})
```

- [ ] **Step 2: Run the new test; expect PASS**

Run: `GOWORK=off go test ./internal/gateway/ -run 'gemini type' -count=1 -v`
Expected: PASS. (If it fails, that is the bug this plan exists to catch — stop and fix before continuing.)

- [ ] **Step 3: example.yaml**

After the existing commented openrouter block, add a commented-out
`# providers:` / `#   - name: gemini` fragment with
`type: gemini`, `chat: https://generativelanguage.googleapis.com/v1beta/openai`,
`gemini: https://generativelanguage.googleapis.com/v1beta`,
`keys: … key: "${GEMINI_API_KEY}"`, plus a one-line warning comment:
`# node.api must stay unset (auto) for Claude Code; pinning 'gemini' breaks streaming.`

- [ ] **Step 4: README**

1. Provider `type` row in the field table (line ~799) gains `gemini`.
2. New short subsection under Providers: what the preset wires, that AI Studio Pro/Ultra plan credits apply only inside AI Studio (API keys draw Cloud Billing against $5 prepay plus redeemed Google Cloud credits), that embedding-only models are filtered from the catalog, that the catalog has no pricing, and that Claude Code must not pin `node.api: gemini` (streaming breaks); Gemini native clients reach it only through `*` entrypoints.
3. `## Provider usage and balances` gains a bullet: `**Gemini**: no per-key balance API; the card shows a note. API keys consume Cloud Billing.`

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/provider_capabilities_test.go example.yaml README.md
git commit -m "docs(gemini): document gemini provider; pin gateway fallback"
```

Append the Task 5 record to this plan before committing.

---

### Task 6: Full gates + maintainer smoke bar

- [ ] **Step 1: Go gates**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS. (Wildcard suites hang in this workspace — if `go test ./...` hangs, re-run failing suites per package with `-p 1` and report.)

- [ ] **Step 2: Web gates**

Run: `cd web && npm test && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 3: Manual verification bar (maintainer only, before merge)**

Checklist to hand to the maintainer:
1. Build & run the binary; web console → Providers → New → Gemini preset with a real Gemini API key.
2. Point a Claude Code-facing entrypoint/route at a Gemini model on that provider (`auto`, no `node.api` pin).
3. Run a real Claude Code session with at least one agentic tool-use turn; confirm streaming and token usage stats.
4. Only after all three pass: tag/release or push.

- [ ] **Step 4: Final record**

Append the Task 6 record (commands + results + state of the smoke checklist) to this plan; commit.

---

## Implementation record

(Each task appends its record here: date/status, repo+SHA, files actually
changed, tests/commands and results, review findings and fixes, deferred
items/limitations. The record and the task's files land in that task's one
commit.)

### Task 1: Provider type `gemini` — adapter, catalog hook, paginated native catalog

- **Date/status:** 2026-11-16 — complete (no reviewer yet).
- **Repository + commit:** cuttle, code committed as `4161cd9d`, then folded with this record via `git commit --amend --no-edit` per plan procedure (a commit cannot contain its own final SHA; see `git log --oneline -1` for the final SHA).
- **Files actually changed:** `internal/config/config.go` (`providerTypes` gains `"gemini"` after `"openrouter"`), `internal/provider/adapter_gemini.go` (new: `geminiAdapter`, static `[chat gemini]` classification, `defaultAuth`, `listAll=false`, `keylessCatalog=false`, `catalogAPIs()=[Gemini]`), `internal/provider/adapter_gemini_test.go` (new: `TestGeminiClassifyAndResolve`, `TestGeminiAuth`), `internal/provider/provider.go` (`catalogAPIsHint` interface next to `adapter`; factory `case "gemini"` after `openrouter`), `internal/provider/models.go` (`listAPIs` honors `catalogAPIsHint` before the existing chat-first logic, falling through when absent or the intersection with configured APIs is empty; Gemini arm of `decodeModels` extracted into `decodeGeminiPage` with `NextPageToken` and `SupportedGenerationMethods` filtering; new `fetchGeminiModels` loops `?pageSize=1000&pageToken=…` with a URL-escaped token, a 100-page bound, and all-or-nothing failure semantics; `fetchModels` dispatches `api == Gemini` before its single-request path; `modelListJSON` gains `nextPageToken`), `internal/provider/models_test.go` (+`TestModelsGeminiCatalogFromNativeOnly`, `TestModelsGeminiPageTokenForwarded`, `TestModelsGeminiPartialFailureFailsAll`, `TestModelsGeminiFiltersEmbeddingOnly`), `internal/provider/provider_test.go` (factory list gains `"gemini"`).
- **Tests/commands and results:**
  - `GOWORK=off go test ./internal/provider/ -run 'TestGeminiClassifyAndResolve|TestGeminiAuth' -count=1 -v` (before implementation) → FAIL, `provider "gm": unknown type "gemini"` as expected.
  - `GOWORK=off go test ./internal/provider/ -count=1` → `ok  github.com/ginuerzh/cuttle/internal/provider	0.050s`
  - `GOWORK=off go vet ./internal/provider/ ./internal/config/` → no findings.
  - `GOWORK=off go test ./internal/provider/ ./internal/config/ -count=1` → `ok  github.com/ginuerzh/cuttle/internal/provider	0.052s`; `ok  github.com/ginuerzh/cuttle/internal/config	0.773s`
  - `GOWORK=off go test ./internal/provider/ -run 'TestGeminiClassifyAndResolve|TestGeminiAuth|TestModelsGemini' -count=1 -v` → all 8 tests PASS (incl. pre-existing `TestModelsGeminiNamesAreStripped`, `TestModelsGeminiLimits`).
- **Review findings and fixes:** none (no reviewer run yet).
- **Deferred items/limitations:** none. Semantics pinned by this task: catalog reads the native endpoint only when both a `gemini` type provider and a native `Gemini` base are configured; a Gemini-hint adapter whose `catalogAPIs()` intersects no configured API falls back to the existing chat-first behavior (per plan). Partial multi-page failure fails the whole listing (zero models); `imagen`-style entries with no `supportedGenerationMethods` are kept, embedding-only entries are dropped.
