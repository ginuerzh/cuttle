# OpenRouter Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenRouter as a first-class llm-gw provider with native Chat, Responses and Anthropic endpoints, a rich public model catalog, and per-key usage/limit reporting.

**Architecture:** Register `openrouter` in the existing provider registry and add `openRouterAdapter` with bearer auth and static three-API classification. Extend the OpenAI-compatible catalog decoder to consume OpenRouter's `architecture`, `pricing`, `top_provider` and `endpoints` fields. Add a `GET /api/v1/key` usage parser, expose its unlimited-key note through the admin view and Usage page, and make the count_tokens route policy estimate locally because OpenRouter has no native counter.

**Tech Stack:** Go 1.27, `GOWORK=off` modules, net/http, React + Vite + antd + vitest.

**Spec:** `docs/superpowers/specs/2026-11-08-openrouter-provider-design.md`

## Global Constraints

- Run Go commands with `GOWORK=off`.
- Do not edit sibling repositories (`core/`, `x/`, `gost/`, etc.); only `llm-gw`.
- Conventional commits; commit after every completed task.
- Do not push unless the user explicitly says `上线`.
- Keep existing provider types, config behavior and protocol fallback unchanged.
- All new user-facing strings go through `t()` with both zh-CN and en-US entries.
- Raw provider keys never appear in API responses, logs, stats or the web bundle.

## Review Focus

- A model ID with `/` such as `anthropic/claude-sonnet-4` must compile and route without becoming part of a path or rewrite key.
- OpenRouter routes must estimate `count_tokens` locally; forwarding to `/v1/messages/count_tokens` would fail.
- The public `/api/v1/models` catalog must be fetched without an `Authorization` header.
- Per-token OpenRouter prices must become USD per million, including free `0/0` models.
- An unlimited key has `limit: null` and `limit_remaining: null`; the Usage page must show the note rather than a fake `$0` balance.

---

### Task 1: Register OpenRouter and its preset

**Files:**
- Modify: `internal/config/config.go` (provider type registry)
- Create: `internal/provider/adapter_openrouter.go`
- Modify: `internal/provider/provider.go` (factory switch)
- Modify: `internal/provider/adapters_test.go`
- Modify: `internal/provider/provider_test.go`
- Modify: `internal/admin/presets.go`
- Modify: `internal/admin/server_test.go`
- Modify: `web/src/pages/presets.ts`
- Modify: `web/src/pages/presets.test.ts`
- Modify: `internal/gateway/upstream_target_test.go`

**Interfaces:**
- Consumes: existing `adapter` interface (`classify`, `auth`, `listAll`, `keylessCatalog`), `config.IsProviderType`.
- Produces: provider type `openrouter`; server preset with Chat/Responses `https://openrouter.ai/api/v1` and Anthropic `https://openrouter.ai/api`; web `PROVIDER_TYPES` includes `openrouter`.

- [ ] **Step 1: Add failing provider tests**

In `internal/provider/adapters_test.go` add:
- `{"openrouter", "openrouter", "anthropic/claude-sonnet-4", []API{Chat, Responses, Anthropic}, true}`
- Auth cases for `openrouter` on Chat, Responses and Anthropic all expecting `Authorization` + `Bearer `.
- Add `openrouter` to the `TestAuthOverride` type list.

In `internal/provider/provider_test.go`, add `openrouter` to the `TestFactory` type list.

In `internal/gateway/upstream_target_test.go`, add
`TestCompileNodeTargetsOpenRouter` using a provider with the three preset URLs
and node model `anthropic/claude-sonnet-4`. Assert the default target is Chat
with base `https://openrouter.ai/api/v1`, `targetModel` keeps the slash, the
Responses variant uses the Chat base, the Anthropic variant uses
`https://openrouter.ai/api`, and the Anthropic auth is `Authorization` +
`Bearer `.

- [ ] **Step 2: Run the provider and gateway tests and confirm failure**

Run: `GOWORK=off go test ./internal/provider ./internal/gateway -run 'TestClassify|TestAuth|TestFactory|TestCompileNodeTargetsOpenRouter'`
Expected: FAIL because `openrouter` is an unknown provider type.

- [ ] **Step 3: Implement the adapter and registration**

Create `internal/provider/adapter_openrouter.go`:

```go
type openRouterAdapter struct{}

func (openRouterAdapter) classify(id string) []API {
    if strings.TrimSpace(id) == "" {
        return nil
    }
    return []API{Chat, Responses, Anthropic}
}

func (openRouterAdapter) auth(API) config.Auth { return bearerAuth() }

func (openRouterAdapter) listAll() bool { return false }

func (openRouterAdapter) keylessCatalog() bool { return true }
```

Add `"openrouter": {}` to `internal/config.providerTypes` and:
```go
case "openrouter":
    a = openRouterAdapter{}
```
to `provider.New`.

- [ ] **Step 4: Add the preset and web registration**

In `internal/admin/presets.go` add:

```go
{
    Name:      "openrouter",
    Type:      "openrouter",
    Chat:      "https://openrouter.ai/api/v1",
    Responses: "https://openrouter.ai/api/v1",
    Anthropic: "https://openrouter.ai/api",
},
```

Update `internal/admin/server_test.go` from 4 to 5 presets and add the exact OpenRouter URLs to its checks.

Add `openrouter` to `PROVIDER_TYPES` in `web/src/pages/presets.ts`. In `web/src/pages/presets.test.ts`, assert `PROVIDER_TYPES` contains `openrouter` and `isBaseURLLocked('openrouter')` is true.

- [ ] **Step 5: Run Task 1 verification**

Run:
```bash
GOWORK=off go test ./internal/provider ./internal/admin ./internal/gateway
cd web && npm test
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/provider/adapter_openrouter.go internal/provider/provider.go internal/provider/adapters_test.go internal/provider/provider_test.go internal/admin/presets.go internal/admin/server_test.go web/src/pages/presets.ts web/src/pages/presets.test.ts internal/gateway/upstream_target_test.go
git commit -m "feat(provider): register OpenRouter"
```

**Implementation record**
- Date/status: 2026-11-08, complete
- Repo/commit: pending (recorded in the follow-up plan commit)
- Files changed: `internal/config/config.go`, `internal/provider/adapter_openrouter.go` (new), `internal/provider/provider.go`, `internal/provider/adapters_test.go`, `internal/provider/provider_test.go`, `internal/admin/presets.go`, `internal/admin/server_test.go`, `internal/gateway/upstream_target_test.go`, `web/src/pages/presets.ts`, `web/src/pages/presets.test.ts`, plus the design/plan documents
- Tests/commands and results: `GOWORK=off go test ./internal/provider ./internal/admin ./internal/gateway` → pass; `npm test -- --run src/pages/presets.test.ts` → 60/60 pass
- Review findings/fixes: the web type constant was changed before its test, so the first run passed; reverted it, observed the expected RED (`PROVIDER_TYPES` missing `openrouter`), then restored it. Go tests failed first with `unknown type "openrouter"`.
- Deferred items/limitations: none

---

### Task 2: Parse OpenRouter model metadata and endpoints

**Files:**
- Modify: `internal/provider/models.go`
- Modify: `internal/provider/models_test.go`

**Interfaces:**
- Consumes: `modelItem`, `openAIModelJSON`, `lenientPrice`, `normalizeModalities`, `intersectAPIs`.
- Produces: `modelItem.APIs []API` and `modelItem.EndpointsKnown bool`; optional `architecture`, `pricing`, `top_provider` and `endpoints` parsing; OpenRouter per-token prices converted to USD per million.

- [ ] **Step 1: Add a failing OpenRouter catalog test**

Add `TestModelsOpenRouterMetadata` to `internal/provider/models_test.go` with an httptest server that checks:
- path is `/api/v1/models`
- `Authorization` is empty

Return entries:

```json
{"id":"anthropic/claude-sonnet-4","name":"Anthropic: Claude Sonnet 4","context_length":200000,
 "architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},
 "pricing":{"prompt":"0.000003","completion":"0.000015"},
 "top_provider":{"max_completion_tokens":64000},
 "endpoints":["/api/v1/chat/completions","/api/v1/responses","/api/v1/messages"]}
```

and an image-only model whose endpoints are only `/api/v1/images`.
Also include a free model with `pricing.prompt`/`completion` `"0"` and a
chat-only model with only `/api/v1/chat/completions`.

Create the provider with `Type: "openrouter"`, `Chat: srv.URL + "/api/v1"` and a key. Assert:
- the Claude model exists with APIs `[Chat, Responses, Anthropic]`
- `Context == 200000`, `Output == 64000`
- inputs `[text, image]`, outputs `[text]`
- price is `3` input / `15` output
- the free model has a non-nil `0/0` price
- the chat-only model has APIs `[Chat]`
- the image-only model is absent.

- [ ] **Step 2: Run the new test and confirm failure**

Run: `GOWORK=off go test ./internal/provider -run TestModelsOpenRouterMetadata`
Expected: FAIL because the metadata fields are not parsed and the image-only model is still listed.

- [ ] **Step 3: Extend the decoder**

In `internal/provider/models.go`:
- Add optional structs/fields to `openAIModelJSON`:
  `Architecture` (`input_modalities`, `output_modalities`), `Pricing` (`prompt`, `completion` as `lenientPrice`), `TopProvider` (`context_length`, `max_completion_tokens`) and `Endpoints []string`.
- Add `APIs []API` and `EndpointsKnown bool` to `modelItem`.
- In `toItem`, fill modalities from `Architecture` before the existing top-level fields, context from `top_provider.context_length` when missing, output from `top_provider.max_completion_tokens` when missing, and price from `pricing.prompt/completion * 1_000_000` when the existing `cost` metadata did not produce a price. Both zero prices remain valid.
- Add `apiFromEndpoint(endpoint string) (API, bool)` mapping:
  `/chat/completions` → `Chat`, `/responses` → `Responses`, `/messages` → `Anthropic`; ignore all others.
- Set `item.APIs` and `item.EndpointsKnown = len(m.Endpoints) > 0` in `toItem`.
- In `Models`, skip an item when endpoints are known but no supported API was found. Otherwise prefer `item.APIs`, then the adapter's static classification, then the source API.

- [ ] **Step 4: Run the model tests**

Run: `GOWORK=off go test ./internal/provider -run 'TestModels'`
Expected: PASS, including existing catalog tests.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/models.go internal/provider/models_test.go
git commit -m "feat(provider): parse OpenRouter model metadata"
```

**Implementation record**
- Date/status: pending
- Repo/commit: pending
- Files changed: pending
- Tests/commands and results: pending
- Review findings/fixes: pending
- Deferred items/limitations: pending

---

### Task 3: Query OpenRouter key usage and show it in the Usage page

**Files:**
- Modify: `internal/provider/usage.go`
- Modify: `internal/provider/usage_test.go`
- Modify: `internal/admin/handlers_usage.go`
- Modify: `internal/admin/usage_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/Usage.tsx`
- Modify: `web/src/pages/Usage.test.ts`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `UsageProvider`, `usageBody`, `usageNumber`, `usageClampPercent`, `AccountUsage`, `accountUsageView`.
- Produces: `UsageSupport("openrouter") == true`; `parseOpenRouterUsage(body []byte) ([]BalancePart, []QuotaWindow, string, error)`; `AccountUsage.Note` exposed as `note`; daily/key-limit window labels.

- [ ] **Step 1: Add failing usage tests**

In `internal/provider/usage_test.go` add:

`TestUsageOpenRouterLimitedKey`: httptest server asserts path `/api/v1/key` and `Authorization: Bearer sk-or-test`, returns:

```json
{"data":{"limit":100,"limit_remaining":74.5,"limit_reset":"monthly","usage":25.5,"usage_daily":1.2}}
```

Assert one USD balance of `74.5`, one `monthly` window with `25.5%`, no note, and a non-zero `CheckedAt`.

`TestUsageOpenRouterUnlimitedKey`: returns:

```json
{"data":{"limit":null,"limit_remaining":null,"usage":25.5}}
```

Assert no balance/windows and a note containing `unlimited key` and `$25.50`.

Also assert `UsageSupport("openrouter")` is true.

In `internal/admin/usage_test.go`, add
`TestProviderUsageOpenRouterNote`: configure an `openrouter` provider against
an httptest server returning the unlimited-key payload at `/api/v1/key`, call
`GET /api/provider-usage?refresh=1`, and assert the account is supported and
its `note` contains `unlimited key` and `$25.50`.

- [ ] **Step 2: Run the new tests and confirm failure**

Run: `GOWORK=off go test ./internal/provider ./internal/admin -run 'TestUsageOpenRouter|TestProviderUsageOpenRouterNote'`
Expected: FAIL because OpenRouter usage is unsupported and the parser is missing.

- [ ] **Step 3: Implement the usage query**

In `internal/provider/usage.go`:
- Add `openrouter` to `UsageSupport` and the `kind` switch.
- In `usageURL`, add:
  ```go
  case "openrouter":
      u.Path = strings.TrimRight(u.Path, "/") + "/key"
  ```
- Add `parseOpenRouterUsage`:
  - parse `data.limit`, `data.limit_remaining`, `data.limit_reset`, `data.usage`
  - if limit > 0 and remaining is present: balance `{Currency: "USD", Total: max(0, remaining)}`; window name `daily`/`weekly`/`monthly` or `key limit`; `UsedPercent = usageClampPercent(100*(limit-remaining)/limit)`
  - otherwise return a note `unlimited key · all-time usage $%.2f` (or `unlimited key` when usage is absent)
  - return an error when neither a usable limit nor usage is present
- Thread the note through `usageAccount`: add a `note` local, assign `acc.Note`, and keep existing error behavior.
- In `internal/admin/handlers_usage.go`, add `Note string `json:"note,omitempty"`` to `accountUsageView`, copy `acc.Note`, and preserve the old note in the stale fallback branch.

- [ ] **Step 4: Add the web note and labels**

- Add `note?: string` to `AccountUsage` in `web/src/api/types.ts`.
- Render `account.note` as secondary text under the key preview in `web/src/pages/Usage.tsx`.
- Export `AccountRow` and `windowLabel`; add `daily` → `每日`, `key limit` → `密钥限额`.
- Add `'每日': 'Daily'` and `'密钥限额': 'Key limit'` to `web/src/locales/en-US.ts`.
- In `web/src/pages/Usage.test.ts`, set the active language to zh-CN and assert both new labels. Render `AccountRow` with an account containing `note: 'unlimited key · all-time usage $25.50'` via `createElement` + `renderToStaticMarkup` and assert the HTML contains `$25.50`.

- [ ] **Step 5: Run Task 3 verification**

Run:
```bash
GOWORK=off go test ./internal/provider ./internal/admin
cd web && npm test
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/usage.go internal/provider/usage_test.go internal/admin/handlers_usage.go internal/admin/usage_test.go web/src/api/types.ts web/src/pages/Usage.tsx web/src/pages/Usage.test.ts web/src/locales/en-US.ts
git commit -m "feat(usage): report OpenRouter key limits"
```

**Implementation record**
- Date/status: pending
- Repo/commit: pending
- Files changed: pending
- Tests/commands and results: pending
- Review findings/fixes: pending
- Deferred items/limitations: pending

---

### Task 4: Estimate count_tokens for OpenRouter routes

**Files:**
- Create: `internal/provider/capabilities.go`
- Create: `internal/provider/capabilities_test.go`
- Modify: `internal/gateway/count_tokens_policy.go`
- Modify: `internal/gateway/count_tokens_policy_test.go`

**Interfaces:**
- Consumes: `nodePlan.provider.Type`, `provider.Anthropic`, `routeNeedsCountTokensEstimate`.
- Produces: `provider.SupportsAnthropicCountTokens(typ string) bool` (false for `openrouter`, true otherwise); OpenRouter routes set `countTokensEstimate` to true even when an Anthropic target exists.

- [ ] **Step 1: Add failing capability and policy tests**

In `internal/provider/capabilities_test.go`:
```go
if SupportsAnthropicCountTokens("openrouter") {
    t.Fatal("openrouter has no native count_tokens endpoint")
}
if !SupportsAnthropicCountTokens("deepseek") {
    t.Fatal("deepseek keeps native count_tokens")
}
if !SupportsAnthropicCountTokens("") {
    t.Fatal("unknown/empty types default to native")
}
```

In `internal/gateway/count_tokens_policy_test.go`, add:
```go
openRouterAnthropic := nodeTargets{defaultTarget: upstreamTarget{api: provider.Anthropic}}
{"openrouter anthropic target estimates", []nodePlan{{
    provider: gwconfig.Provider{Type: "openrouter"},
    targets:  openRouterAnthropic,
}}, true}
```
Keep the existing all-Anthropic case with no provider type expecting `false`.

- [ ] **Step 2: Run the tests and confirm failure**

Run: `GOWORK=off go test ./internal/provider -run TestSupportsAnthropicCountTokens` and `GOWORK=off go test ./internal/gateway -run TestRouteNeedsCountTokensEstimate`
Expected: FAIL because the helper does not exist and OpenRouter is treated as native.

- [ ] **Step 3: Implement the capability**

Create `internal/provider/capabilities.go`:

```go
func SupportsAnthropicCountTokens(typ string) bool {
    return strings.ToLower(strings.TrimSpace(typ)) != "openrouter"
}
```

In `routeNeedsCountTokensEstimate`, return `true` when the selected target is not Anthropic **or** when `!provider.SupportsAnthropicCountTokens(np.provider.Type)`.

- [ ] **Step 4: Run Task 4 verification**

Run:
```bash
GOWORK=off go test ./internal/provider ./internal/gateway
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/capabilities.go internal/provider/capabilities_test.go internal/gateway/count_tokens_policy.go internal/gateway/count_tokens_policy_test.go
git commit -m "fix(gateway): estimate count_tokens for OpenRouter"
```

**Implementation record**
- Date/status: pending
- Repo/commit: pending
- Files changed: pending
- Tests/commands and results: pending
- Review findings/fixes: pending
- Deferred items/limitations: pending

---

### Task 5: Document OpenRouter and run the full gate

**Files:**
- Modify: `README.md`
- Modify: `example.yaml`
- Modify: `/root/code/go-gost/.memory/notes/llm-gw-provider-capabilities.md`
- Modify: `/root/code/go-gost/.memory/notes/llm-gw-provider-usage.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: operator documentation for the new type, preset, catalog metadata, per-key usage and count_tokens behavior.

- [ ] **Step 1: Update README**

- Add `openrouter` to the provider `type` table.
- Add a short OpenRouter example with the three base URLs and `OPENROUTER_API_KEY`.
- State that all three APIs use `Authorization: Bearer`.
- Add OpenRouter to the Usage supported-upstream list:
  `GET https://openrouter.ai/api/v1/key` returns per-key limit/remaining/usage;
  account-wide `/credits` requires a management key and is not queried.
- Document that OpenRouter does not support `/v1/messages/count_tokens`, so
  these routes are estimated locally.

- [ ] **Step 2: Update example.yaml**

Add a commented `openrouter` provider block after the DeepSeek example, with
the three preset URLs and `keys: ["${OPENROUTER_API_KEY}"]`.

- [ ] **Step 3: Update project memory**

In `/root/code/go-gost/.memory/notes/llm-gw-provider-capabilities.md`, record the
new adapter, base URLs, bearer auth and catalog endpoint metadata. In
`llm-gw-provider-usage.md`, record the `/key` contract and unlimited-key note.
Do not commit unrelated workspace-root changes.

- [ ] **Step 4: Run the full gate**

Run:
```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npm run build
```
Expected: all pass.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md example.yaml
git commit -m "docs: document OpenRouter provider"
```

**Implementation record**
- Date/status: pending
- Repo/commit: pending
- Files changed: pending
- Tests/commands and results: pending
- Review findings/fixes: pending
- Deferred items/limitations: pending

---

## Final verification

After Task 5, run `git status` and confirm only expected files changed. Record
the final commit SHAs in the implementation records above. Do not push until
the user explicitly requests deployment.
