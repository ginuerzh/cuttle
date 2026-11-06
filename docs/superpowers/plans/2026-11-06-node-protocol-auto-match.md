# Node Protocol Auto-Match Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every node prefer the upstream wire API that matches the client's inbound protocol, while allowing an explicit per-node API pin.

**Architecture:** Entrypoints expand into protocol-specific hop entries (`chat`, `responses`, `anthropic`, `gemini`) plus the existing fallback entry. Each hop entry points to a route variant compiled for that inbound protocol; each node in the variant uses the matching provider API when available and its default API otherwise. Route plans, rewriters and path rewrites are built from one shared compilation pass so they cannot diverge.

**Tech Stack:** Go 1.27, x hop group/selector and routing matchers, `llm-api-converter`, React + Ant Design + Vite admin console.

**Spec:** `docs/superpowers/specs/2026-11-06-node-protocol-auto-match-design.md`

## Global Constraints

- Run Go commands with `GOWORK=off` inside `llm-gw`; `go.mod` uses published tags and must not gain a local `replace`.
- Do not edit sibling repositories (`../core`, `../x`, `../llm-api-converter`).
- Config stays strict YAML (`yaml.KnownFields(true)`); unknown fields must keep failing loudly.
- Keep the routing chain: entrypoint → route → node → provider model. Entrypoint declaration order remains the only precedence rule.
- x node names stay `<route>/<provider>/<node>[/<keyIndex>]`; `stats.parseNodeName` must not change.
- Do not replace the x hop group/FIFO selector with a custom dispatcher.
- Code, comments, docs and commit messages are English. Admin UI source strings are Chinese and translated in `web/src/locales/en-US.ts`.
- Every task ends with its **Implementation record** filled in and committed with the task.
- Never push.

## Review Focus

- **Pinned API rescue:** a node pinned to an API that is not the model's static default must compile even when the default API has no usable base URL.
- **Unknown inbound paths:** paths such as `/v1/models` or telemetry must use the fallback entry, keep the default upstream API, and pass through the path unchanged.
- **Entrypoint precedence:** an earlier entrypoint must still win over a later one after protocol entries are expanded; path matching must not reorder entrypoints.
- **count_tokens robustness:** a non-JSON or model-less count_tokens body must pass through unchanged instead of failing the request.
- **Reload safety:** a reload whose pinned API has no configured base URL must keep the old data plane serving.

---

### Task 1: Add `node.api` configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/admin/server_test.go`

**Interfaces:**
- Consumes: existing strict YAML load/validate pipeline and `config.ApplyDefaults`.
- Produces: `config.Node.API string` with YAML/JSON key `api`, omitted when auto; validation accepts `auto`, `chat`, `responses`, `anthropic`, `gemini`; `auto` normalizes to `""`.

- [x] **Step 1: Write failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestNodeAPIAcceptedAndNormalized(t *testing.T) {
    // YAML with three nodes: api: AUTO, api: Anthropic, and no api field.
    // Load must succeed and the API fields must be "", "anthropic", "".
}

func TestNodeAPIRejectsUnknown(t *testing.T) {
    // YAML with api: grpc must fail with an error naming the node.
}
```

Add to `internal/admin/server_test.go` a test that creates a provider and a node with `"api":"anthropic"`, then `GET /api/nodes` and asserts the decoded node still has `api: "anthropic"`; then updates the node with `"api":"auto"` and asserts the stored config has an empty API.

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/config ./internal/admin -run 'TestNodeAPI' -count=1`

Expected: FAIL because `config.Node` has no `API` field and the unknown value is accepted.

- [x] **Step 3: Implement the field, normalization and validation**

In `internal/config/config.go`:

```go
type Node struct {
    Name     string `yaml:"name" json:"name"`
    Provider string `yaml:"provider" json:"provider"`
    Model    string `yaml:"model" json:"model"`
    API      string `yaml:"api,omitempty" json:"api,omitempty"`
    Backup   bool   `yaml:"backup,omitempty" json:"backup,omitempty"`
    Weight   int    `yaml:"weight,omitempty" json:"weight,omitempty"`
    Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}
```

In node defaults, trim/lower the value; treat `auto` as empty. In node validation, accept only `""`, `chat`, `responses`, `anthropic`, `gemini` and return an error that names the node for anything else.

- [x] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./internal/config ./internal/admin -run 'TestNodeAPI' -count=1`

Expected: PASS.

- [x] **Step 5: Run the package gates**

Run: `GOWORK=off go test ./internal/config ./internal/admin -count=1`

Expected: PASS.

- [x] **Step 6: Fill the Implementation record and commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/admin/server_test.go docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md docs/superpowers/specs/2026-11-06-node-protocol-auto-match-design.md
git commit -m "feat(config): add node api selection"
```

**Implementation record** (fill after completion):
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` — this task's commit (see `git log -1`)
- Files changed: `internal/config/config.go`, `internal/config/config_test.go`, `internal/admin/server_test.go`, design spec, plan
- Tests/commands and results: `GOWORK=off go test ./internal/config ./internal/admin -run TestNodeAPI -count=1` PASS; `GOWORK=off go test ./internal/config ./internal/admin -count=1` PASS.
- Review findings and fixes: none.
- Deferred items/limitations: none.

---

### Task 2: Compile default and per-protocol node targets

**Files:**
- Modify: `internal/gateway/upstream.go`
- Create: `internal/gateway/upstream_target_test.go`

**Interfaces:**
- Consumes: `config.Node.API` from Task 1; existing `provider.Provider`, `selectAPI`, `upstreamTarget`.
- Produces:
  ```go
  type nodeTargets struct {
      defaultTarget upstreamTarget
      variants      map[provider.API]upstreamTarget // inbound group -> target, only when api differs
  }

  func compileNodeTargets(p gwconfig.Provider, n gwconfig.Node) (nodeTargets, error)
  func compileTarget(prov provider.Provider, p gwconfig.Provider, n gwconfig.Node, api provider.API) (upstreamTarget, error)
  ```
  `compileNodeTargets` must skip `selectAPI` entirely when `n.API` is pinned.

- [x] **Step 1: Write failing target tests**

Add `internal/gateway/upstream_target_test.go` with table tests:

```go
func TestCompileNodeTargetsAutoDeepSeek(t *testing.T)
// Provider type deepseek with Chat/Responses/Anthropic bases.
// Node model deepseek-v4-flash, API "".
// Expect default api "chat"; variants contain "responses" and "anthropic".

func TestCompileNodeTargetsGenericUnknownModel(t *testing.T)
// Provider type generic with Chat/Anthropic bases, unknown model, API "".
// Expect default "chat"; variants contain "anthropic".

func TestCompileNodeTargetsStaticClassificationExcludes(t *testing.T)
// Provider type opencode-go with Chat/Anthropic bases, model deepseek-v4-flash.
// Expect default "chat"; no "anthropic" variant.

func TestCompileNodeTargetsPinnedRescuesDefault(t *testing.T)
// Provider type commandcode with only a Chat base, model claude-sonnet-4,
// Node API "chat". Static classification is [anthropic] but the pin must
// compile and produce a chat target with no variants.

func TestCompileNodeTargetsPinnedMissingBase(t *testing.T)
// Provider with only a Chat base, pinned API "anthropic": expect an error
// naming the node and API.
```

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run TestCompileNodeTargets -count=1`

Expected: FAIL because `compileNodeTargets` and `nodeTargets` do not exist.

- [x] **Step 3: Implement target compilation**

In `internal/gateway/upstream.go`:

- Add `nodeTargets` and `compileNodeTargets`.
- If `n.API` is non-empty: build one target with `compileTarget` and return it as the default with no variants.
- Otherwise call `selectAPI` for the default, then for each inbound API:
  - skip when it equals the default;
  - skip when the provider has no base URL for it;
  - skip when `provider.Resolve(model)` returns a non-empty `APIs` list that does not contain it;
  - otherwise compile and store it in `variants`.
- `compileTarget` validates the base URL and returns `upstreamTarget{baseURL, api, targetModel, auth: prov.Auth(api)}`.
- Keep the existing `compileRouteNode` call shape temporarily by making it return `compileNodeTargets(...).defaultTarget`, so `build.go` still compiles until Task 3.

- [x] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./internal/gateway -run 'TestCompileNodeTargets|TestBuildNodes|TestProtocolMatrix' -count=1`

Expected: PASS.

- [x] **Step 5: Fill the Implementation record and commit**

```bash
git add internal/gateway/upstream.go internal/gateway/upstream_target_test.go docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md
git commit -m "feat(gateway): plan per-protocol node targets"
```

**Implementation record** (fill after completion):
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` — this task's commit (see ledger)
- Files changed: `internal/gateway/upstream.go`, `internal/gateway/upstream_target_test.go`, plan
- Tests/commands and results: `GOWORK=off go test ./internal/gateway -run TestCompileNodeTargets -count=1` FAILED before implementation (undefined symbols), PASS after; `GOWORK=off go test ./internal/gateway -run 'TestCompileNodeTargets|TestBuildNodes|TestProtocolMatrix' -count=1` PASS; `GOWORK=off go test ./internal/gateway -count=1` PASS.
- Review findings and fixes: none.
- Deferred items/limitations: `compileRouteNode` is kept only as a temporary wrapper until Task 3.

---

### Task 3: Compile protocol-aware entrypoints and route variants

**Files:**
- Create: `internal/gateway/protocol.go`
- Modify: `internal/gateway/build.go`
- Modify: `internal/gateway/provider_capabilities_test.go`
- Modify: `internal/gateway/provider_capabilities_test.go:TestRewriterNameCannotCollide`
- Create: `internal/gateway/protocol_auto_test.go`

**Interfaces:**
- Consumes: `compileNodeTargets`, `nodeTargets`, `compileTarget` from Task 2.
- Produces:
  ```go
  type inboundGroup struct {
      api         provider.API
      pathPattern string
  }
  var inboundGroups = []inboundGroup{...}

  func buildProtocolMatcher(match []string, pathPattern string) (corerouting.Matcher, error)

  type routePlan struct {
      hops map[provider.API]hop.Hop // key "" is the default/fallback variant
  }
  func buildRoutePlan(cfg *gwconfig.Config, r gwconfig.Route, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (*routePlan, error)
  func rewriterName(route, variant, node string) string
  ```
  `rewriterName("r", "default", "n")` returns `"r/r/default/n"`; variant is `"default"` or the API name.

- [x] **Step 1: Write failing gateway tests**

Create `internal/gateway/protocol_auto_test.go`. Use one `httptest` upstream whose base paths distinguish the APIs, for example:

```go
p := gwconfig.Provider{
    Name: "ds", Type: "deepseek",
    Chat: up.URL + "/chat", Responses: up.URL + "/responses",
    Anthropic: up.URL + "/anthropic", Keys: []string{"sk-test"},
}
```

Tests and assertions:

```go
func TestAutoProtocolRouting(t *testing.T)
// Entrypoint "*" -> route -> auto node deepseek-v4-flash.
// /v1/chat/completions -> /chat/chat/completions
// /v1/responses       -> /responses/responses
// /v1/messages        -> /anthropic/v1/messages
// Each request is 200 and the upstream body model is deepseek-v4-flash.
// Stats count protocols {chat:1, responses:1, anthropic:1} and inbound
// protocols {openai-chat:1, openai-responses:1, anthropic:1}.

func TestAutoProtocolUnknownPathFallback(t *testing.T)
// /v1/models with a model body reaches the upstream unchanged and records
// upstream protocol "chat" (the default).

func TestAutoProtocolStaticClassificationExcludes(t *testing.T)
// opencode-go provider with Chat/Anthropic bases, model deepseek-v4-flash.
// /v1/messages must convert to chat and hit /chat/chat/completions.

func TestGenericAutoProtocolUsesConfiguredBase(t *testing.T)
// generic provider with Chat/Anthropic bases, unknown model.
// /v1/messages must hit /anthropic/v1/messages.

func TestPinnedNodeAPIOverridesInbound(t *testing.T)
// deepseek provider with Chat/Anthropic bases, node api "chat".
// /v1/messages must hit /chat/chat/completions and record protocol "chat".

func TestEntrypointOrderWithProtocolVariants(t *testing.T)
// Two entrypoints: first match ["*"] points to route "a", second points to
// route "b"; both routes have an anthropic-capable node but different base
// path prefixes. An Anthropic request must hit route "a"'s upstream.

func TestRouteVariantKeepsBackupMetadata(t *testing.T)
// Build a route plan directly with two nodes: a primary auto deepseek node
// and a backup node (Backup: true). Assert plan.hops[provider.Anthropic]
// exists, its Nodes() include both nodes, the backup node's metadata has
// backup=true, and a disabled node is absent.

func TestGeminiAutoProtocol(t *testing.T)
// Entrypoint "*" -> auto node with a Gemini base. POST
// /v1beta/models/gemini-2.5-pro:generateContent reaches the Gemini target
// path and records upstream protocol "gemini".

func TestReloadKeepsOldDataPlaneWhenPinnedAPIHasNoBase(t *testing.T)
// Start with a valid auto config, then call gw.Reload with a node pinned to
// "anthropic" while the provider only has a chat base. Reload must return an
// error and the original gateway must still serve a chat request.
```

Update `TestRewriterNameCannotCollide` to the three-argument signature and the `TestBuildNodes*` helpers in `provider_capabilities_test.go` to pass a rewriter name.

Update the existing generic test in `provider_capabilities_test.go` (`generic routing ignores catalog APIs and uses canonical order`) to expect the inbound-matching API now: a Responses request with Chat and Responses bases must hit `/v1/responses`, and rename the subtest to describe auto matching.

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestAutoProtocol|TestGenericAutoProtocol|TestPinnedNodeAPI|TestEntrypointOrderWithProtocolVariants|TestRouteVariantKeepsBackupMetadata|TestGeminiAutoProtocol|TestReloadKeepsOldDataPlane' -count=1`

Expected: FAIL with the old chat-only upstream paths.

- [x] **Step 3: Add protocol group and matcher helpers**

In `internal/gateway/protocol.go`:

```go
var inboundGroups = []inboundGroup{
    {provider.Chat, `^/v1/chat/completions$`},
    {provider.Responses, `^/v1/responses$`},
    {provider.Anthropic, `^(/v1/messages|/v1/messages/count_tokens)$`},
    {provider.Gemini, `^/v1beta/models/[^/]+:(generateContent|streamGenerateContent)$`},
}
```

Extract the model-glob parsing from `buildEntrypointMatcher` into:

```go
func modelGlobPattern(match []string) (pattern string, catchAll bool, err error)
```

`buildEntrypointMatcher` keeps returning `nil` for catch-all and otherwise builds `BodyJSON("model", pattern)`.
`buildProtocolMatcher` returns `PathRegexp(pathPattern)` for catch-all and `PathRegexp(pathPattern) && BodyJSON("model", pattern)` otherwise. Use `%q` for quoting in the DSL.

- [x] **Step 4: Replace rewriter pre-registration with route plans**

In `internal/gateway/build.go`:

- Remove `registerRewriters`; move rewriter registration into route-variant construction.
- `buildHopGroup` caches a `*routePlan` per route name. For each entrypoint, append one entry per existing protocol variant (chat, responses, anthropic, gemini order) using `buildProtocolMatcher`, then the fallback entry using `buildEntrypointMatcher`.
- `buildRoutePlan` resolves providers/nodes, skips disabled providers/nodes, calls `compileNodeTargets`, skips `errModelDisabled` nodes with a warning (as today), and builds the default variant plus only the variants whose API appears in at least one node's `variants` map.
- `buildVariantHop` selects `defaultTarget` or `variants[inbound]` per node, registers the rewriter `rewriterName(route, variantKey(inbound), node)` with `rewriter.Options{Provider, Protocol: string(target.api), Route, Node, Sink: facts, Capture: captureMgr, Logger: log}`, then calls `buildNodes`.
- `buildNodes` gains a `rewriter string` parameter and uses it in `HTTPBodyRewriteConfig.Rewriter`.
- Remove the temporary `compileRouteNode` wrapper once `build.go` uses `compileNodeTargets` directly.
- Hop names become `route/<route>/<variant>` so logs distinguish variants.
- `registerHeaderRewriter` stays unchanged.

- [x] **Step 5: Run focused tests**

Run: `GOWORK=off go test ./internal/gateway -run 'TestAutoProtocol|TestGenericAutoProtocol|TestPinnedNodeAPI|TestEntrypointOrderWithProtocolVariants|TestRouteVariantKeepsBackupMetadata|TestGeminiAutoProtocol|TestReloadKeepsOldDataPlane|TestProtocolMatrix|TestEntrypoint|TestRewriterName' -count=1`

Expected: PASS.

- [x] **Step 6: Run the full gateway package**

Run: `GOWORK=off go test ./internal/gateway -count=1`

Expected: PASS.

- [x] **Step 7: Fill the Implementation record and commit**

```bash
git add internal/gateway/protocol.go internal/gateway/build.go internal/gateway/protocol_auto_test.go internal/gateway/provider_capabilities_test.go internal/gateway/model_disabled_test.go docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md
git commit -m "feat(gateway): match upstream api to inbound protocol"
```

**Implementation record** (fill after completion):
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` — this task's commit (see ledger)
- Files changed: `internal/gateway/protocol.go`, `internal/gateway/build.go`, `internal/gateway/upstream.go`, `internal/gateway/protocol_auto_test.go`, `internal/gateway/provider_capabilities_test.go`, `internal/gateway/model_disabled_test.go`, plan
- Tests/commands and results: focused auto-protocol tests PASS; `GOWORK=off go test ./internal/gateway -count=1` PASS; `GOWORK=off go build ./...` PASS; `GOWORK=off go vet ./internal/gateway` PASS.
- Review findings and fixes: disabled-model reload required unregistering all variant rewriter keys; added `unregisterNodeRewriters`.
- Deferred items/limitations: stale rewriters for removed/disabled nodes remain a pre-existing leak; only disabled models are cleaned up.

---

### Task 4: Handle Anthropic `count_tokens` natively

**Files:**
- Modify: `internal/gateway/upstream.go`
- Modify: `internal/rewriter/rewriter.go`
- Modify: `internal/rewriter/helpers_test.go`
- Create: `internal/rewriter/rewriter_test.go`
- Modify: `internal/gateway/protocol_test.go`
- Modify: `internal/gateway/protocol_auto_test.go`

**Interfaces:**
- Consumes: protocol-aware routing and rewriter registration from Task 3.
- Produces:
  ```go
  // rewriter.Options
  TargetModel string

  func replaceModelField(body []byte, model string) []byte
  func isCountTokensURI(uri string) bool
  ```
  `inboundProtocol("/v1/messages/count_tokens")` returns `"anthropic"`.

- [x] **Step 1: Write failing tests**

Add to `internal/rewriter/helpers_test.go`:

```go
"/v1/messages/count_tokens": "anthropic",
```

Add `internal/rewriter/rewriter_test.go`:

```go
func TestReplaceModelField(t *testing.T)
// {"model":"claude-sonnet","messages":[...]} -> model becomes "target",
// messages value is preserved.
// Invalid JSON is returned byte-for-byte unchanged.
// Empty target returns the input unchanged.

func TestIsCountTokensURI(t *testing.T)
// true for "/v1/messages/count_tokens" and its query form;
// false for "/v1/messages", "/v1/chat/completions".
```

Extend `internal/gateway/protocol_auto_test.go`:

```go
func TestCountTokensAnthropicTarget(t *testing.T)
// deepseek provider with Chat/Anthropic bases.
// POST /v1/messages/count_tokens {"model":"claude-x","messages":[]}
// Upstream path is /anthropic/v1/messages/count_tokens.
// Upstream body decodes with model deepseek-v4-flash and messages preserved.
// Stats: inbound anthropic, upstream anthropic.

func TestCountTokensInvalidBodyPassesThrough(t *testing.T)
// Anthropic target, body "not-json": upstream receives exactly "not-json".
```

Keep the existing `TestProtocolMatrix/count_tokens is not converted` as the non-Anthropic target guard: it must still pass byte-for-byte.

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/rewriter ./internal/gateway -run 'TestReplaceModelField|TestIsCountTokensURI|TestCountTokens|TestInboundProtocol' -count=1`

Expected: FAIL for the new Anthropic path/body assertions and missing helpers.

- [x] **Step 3: Implement path rewriting and model substitution**

In `internal/gateway/upstream.go`:

- Add `^/v1/messages/count_tokens$` to the rewrite sources.
- For an Anthropic target, replace with `<base>/v1/messages/count_tokens`, reusing the existing `/v1` suffix rule.
- For other targets, produce no rule (pass-through).

In `internal/rewriter/rewriter.go`:

- Add `TargetModel string` to `Options`.
- Add `isCountTokensURI` and `replaceModelField` (decode into `map[string]json.RawMessage`, replace `model`, marshal; return the input unchanged on any error or empty model).
- In `Rewrite`, when `meta.Direction == "request"`, `r.opts.Protocol == "anthropic"`, `isCountTokensURI(meta.URI)` and `TargetModel != ""`, patch the body before calling the engine.
- Add the count_tokens case to `inboundProtocol`.
- In `build.go`, pass `TargetModel: target.targetModel` when registering rewriters.

- [x] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./internal/rewriter ./internal/gateway -run 'TestReplaceModelField|TestIsCountTokensURI|TestCountTokens|TestInboundProtocol|TestProtocolMatrix' -count=1`

Expected: PASS.

- [x] **Step 5: Fill the Implementation record and commit**

```bash
git add internal/gateway/upstream.go internal/gateway/protocol_auto_test.go internal/gateway/build.go internal/rewriter/rewriter.go internal/rewriter/helpers_test.go internal/rewriter/rewriter_test.go docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md
git commit -m "feat(gateway): route anthropic count_tokens natively"
```

**Implementation record** (fill after completion):
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` — this task's commit (see ledger)
- Files changed: `internal/gateway/upstream.go`, `internal/gateway/protocol_auto_test.go`, `internal/gateway/build.go`, `internal/rewriter/rewriter.go`, `internal/rewriter/helpers_test.go`, `internal/rewriter/rewriter_test.go`, plan
- Tests/commands and results: focused count_tokens tests PASS; `GOWORK=off go test ./internal/gateway ./internal/rewriter -count=1` PASS; `GOWORK=off go build ./...` PASS; `GOWORK=off go vet ./internal/gateway ./internal/rewriter` PASS.
- Review findings and fixes: count_tokens was initially routed to the Anthropic host without the base path; added `anthropicPath` and a dedicated rewrite source.
- Deferred items/limitations: count_tokens bodies remain non-JSON pass-through when the target is not Anthropic; no protocol conversion is attempted.

---

### Task 5: Expose the node API choice in the admin console

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/presets.ts`
- Modify: `web/src/pages/presets.test.ts`
- Modify: `web/src/pages/Edit.tsx`
- Modify: `web/src/pages/Nodes.tsx`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: JSON `node.api` from Task 1; existing `configuredAPIs`, `derivedTargetAPI`, `ProviderApi`.
- Produces:
  ```ts
  export type NodeAPI = ProviderApi | 'auto';
  // Node.api?: NodeAPI

  export function nodeAPIOptions(provider?: Provider): NodeAPI[]
  // ['auto', ...configuredAPIs(provider)]
  ```

- [x] **Step 1: Write failing web tests**

Add to `web/src/pages/presets.test.ts`:

```ts
describe('nodeAPIOptions', () => {
  it('always offers auto first', () => {
    expect(nodeAPIOptions(opencodeGo)).toEqual(['auto', 'chat', 'responses', 'anthropic']);
  });
  it('returns only auto without a provider', () => {
    expect(nodeAPIOptions(undefined)).toEqual(['auto']);
  });
});
```

Update the `genericCanonicalNote` expectation to the new wording.

- [x] **Step 2: Run tests to verify they fail**

Run: `cd web && npm test -- src/pages/presets.test.ts`

Expected: FAIL because `nodeAPIOptions` does not exist and the note text is old.

- [x] **Step 3: Implement types, helpers and form controls**

- `types.ts`: add `NodeAPI` and `Node.api?: NodeAPI`.
- `presets.ts`: add `nodeAPIOptions`; reword `genericCanonicalNote` to say generic providers have no static model classification but auto matches configured base URLs by inbound protocol and falls back to the canonical order.
- `Edit.tsx`:
  - `NodeForm`: add a `Form.Item name="api"` select with `nodeAPIOptions(provider)`, initial value `node.api ?? 'auto'`; save `api: v.api === 'auto' ? '' : v.api`.
  - `AddNodeModal`: add the same select with `initialValue="auto"` and payload.
  - `DerivedTargetAPIHint`: accept the selected `api`; show `目标 API：<api>（固定）` when pinned, with a warning when the provider lacks that base URL; show `目标 API：auto（按客户端协议匹配；默认 <api>）` otherwise.
- `Nodes.tsx`: show an `auto`/pinned tag; include `api: n.api` in the enable/disable update payload so toggling does not erase it.
- `en-US.ts`: add translations for every new Chinese source string.

- [x] **Step 4: Run the web gates**

Run: `cd web && npm test -- src/pages/presets.test.ts && npx tsc --noEmit && npm run build`

Expected: PASS.

- [x] **Step 5: Rebuild the embedded bundle and run the Go build**

Run: `GOWORK=off go build ./...`

Expected: PASS.

- [x] **Step 6: Fill the Implementation record and commit**

```bash
git add web/src/api/types.ts web/src/pages/presets.ts web/src/pages/presets.test.ts web/src/pages/Edit.tsx web/src/pages/Nodes.tsx web/src/locales/en-US.ts docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md
git commit -m "feat(web): select node target api"
```

Note: `internal/admin/dist` is git-ignored; do not add it. Rebuild it locally with `npm run build` before the Go build.

**Implementation record** (fill after completion):
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` — this task's commit (see ledger)
- Files changed: `web/src/api/types.ts`, `web/src/pages/presets.ts`, `web/src/pages/presets.test.ts`, `web/src/pages/Edit.tsx`, `web/src/pages/Nodes.tsx`, `web/src/locales/en-US.ts`, plan
- Tests/commands and results: `cd web && npm test` PASS (96/96); `npx tsc --noEmit` PASS; `npm run build` PASS; `GOWORK=off go build ./...` PASS.
- Review findings and fixes: restored the NodeForm return/Form block after an editing slip; removed an unused `NodeAPI` import.
- Deferred items/limitations: UI behavior was verified by TypeScript, Vitest and HTTP/build checks; no browser visual regression test exists.

---

### Task 6: Documentation, example config and full gate

**Files:**
- Modify: `README.md`
- Modify: `example.yaml`
- Modify: `AGENTS.md`
- Modify: `/root/code/go-gost/.memory/notes/llm-gw-provider-capabilities.md`
- Modify: `/root/code/go-gost/.memory/MEMORY.md`

**Interfaces:**
- Consumes: all behavior from Tasks 1-5.
- Produces: user-facing docs and a verified full gate.

- [ ] **Step 1: Update the user documentation**

In `README.md`:

- In the routing overview, add that a node's target API is `auto` by default: the gateway matches the inbound wire API to a configured provider API and converts to the default only when no matching base URL exists.
- In the node table, add `api` (`auto`, `chat`, `responses`, `anthropic`, `gemini`; default `auto`; pinned APIs require the matching base URL).
- Replace the provider paragraph that says the gateway always picks the first advertised API with the auto/pin rule, including the generic-provider behavior.
- Add a short Claude Code example: `node.api` omitted, DeepSeek provider with chat/responses/anthropic bases.

In `example.yaml`, add an optional commented `api` line to the node section:

```yaml
  - name: deepseek-fast
    provider: deepseek
    model: deepseek-v4-flash
    # api: auto   # auto matches the client protocol; pin chat/responses/anthropic/gemini to force one
```

- [ ] **Step 2: Update the repository guidance**

In `AGENTS.md`, refine the routing invariant:

- Entrypoint model matching remains the only precedence rule.
- Inbound path never affects precedence, but protocol-specific hop entries use it to pick the upstream wire API.
- Document `node.api` and the four protocol groups.
- Add a gotcha that route variants multiply hops/rewriters and that rewriter registration must stay plan-driven.

- [ ] **Step 3: Update workspace memory**

In `/root/code/go-gost/.memory/notes/llm-gw-provider-capabilities.md`, append the decision:

- `node.api` is auto by default; auto maps inbound paths to the matching provider API and falls back to the previous default; pinned APIs bypass default resolution; count_tokens rewrites only the model; Gemini still needs a `*` entrypoint.

Update the index entry in `/root/code/go-gost/.memory/MEMORY.md` if the summary line needs the new fact.

- [ ] **Step 4: Run the full Go gate**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Run the full web gate**

Run:

```bash
cd web
npm test
npx tsc --noEmit
npm run build
```

Expected: PASS.

- [ ] **Step 6: Verify the example config loads**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`

Expected: PASS.

- [ ] **Step 7: Fill the Implementation record and commit**

Commit the llm-gw docs and example separately from workspace memory:

```bash
git add README.md example.yaml AGENTS.md docs/superpowers/plans/2026-11-06-node-protocol-auto-match.md
git commit -m "docs: document node protocol auto-matching"

git -C /root/code/go-gost add .memory/notes/llm-gw-provider-capabilities.md .memory/MEMORY.md
git -C /root/code/go-gost commit -m "docs(memory): record llm-gw protocol auto-match"
```

**Implementation record** (fill after completion):
- Date/status:
- Repository + commit SHA (llm-gw and workspace memory):
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

## Self-Review Notes

- Spec coverage: config, target selection, path groups, route variants, rewriter registration, count_tokens, admin UI, docs and tests all have tasks.
- Types: `nodeTargets`, `compileNodeTargets`, `routePlan`, `rewriterName(route, variant, node)`, `NodeAPI` and `nodeAPIOptions` are consistent across tasks.
- Review Focus items are pinned to tests in Tasks 2, 3 and 4.
- Deferred items from the spec are not implemented here: stale rewriter GC, default `anthropic-version`, per-entrypoint protocol override, Gemini inbound conversion.
