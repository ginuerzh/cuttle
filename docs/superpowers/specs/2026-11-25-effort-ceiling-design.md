# Effort ceiling by target model design

Date: 2026-11-25
Status: proposed

## Intent

OpenCode's four `cuttle` models should offer a top reasoning-effort option
(`max`). Each Cuttle model name is routed to an upstream target whose highest
accepted effort level differs: DeepSeek V4.1 Flash tops out at `max`, some
models stop at `xhigh`, others at `high`. The client should not need to know
which target sits behind a route, and changing a node's target must not require
editing client configuration. cuttle already owns the target and already reads
provider catalogs while compiling a build, so it should clamp a requested
effort level down to the target's ceiling.

## Goals

- A client may send any explicit effort level. When it exceeds the target
  model's highest supported level, cuttle rewrites it to that level before the
  protocol conversion, and again afterwards if the converted body still carries
  a level.
- The ceiling comes from the provider model catalog — the same best-effort fetch
  that already feeds the `max_tokens` cap.
- `max` always resolves to the target's top tier: `max` when the target declares
  it, otherwise `xhigh` when the target declares it, otherwise `high`. The same
  fixed rule applies when a catalog declares no levels at all: a requested `max`
  becomes `high`, regardless of which model sits behind the route.
- The resolver covers the explicit level field of every inbound protocol.
- OpenCode changes once: all four Cuttle models declare the same effort list.

## Non-goals

- No clamping of thinking budgets (`thinking.budget_tokens`, Gemini
  `thinkingBudget`); only explicit level fields.
- No remapping of values below the ceiling (`medium` sent to a target without
  `medium` passes through unchanged).
- No cuttle config schema or admin-console change (that was approach B/C).
- No change to stats effort semantics: `effort` keeps recording the level the
  client selected.
- No effort injection when the client sends none.
- No routing, retry, sticky-selection or entrypoint behavior change.

## Constraints

- Catalog capability data varies by upstream. Verified live on 2026-11-25:
  - DeepSeek `/models` returns
    `"effort":{"supported_levels":["low","high","max"],"default_level":"high"}`.
  - opencode-go `/models` returns
    `"capabilities":{"reasoning":{"supported":true,"effort":["low","high","max"]}}`.
  - The native Gemini catalog reports no levels.
  - `hungerg-free` reports `reasoning.supported` without an effort list.
- Catalog reads are already best-effort, cached with a ten-minute TTL, and must
  never block admin saves or a reload.
- Rewriters are registered per node, not per route: sticky selection and retry
  may send the same client request to nodes with different ceilings. The clamp
  therefore belongs in the per-node rewriter, not in route-level planning.
- A request body must stay byte-identical when no rewrite is needed. The
  existing `clampMaxTokens`/`replaceModelField` re-marshal only on change.

## Approaches considered

### A. Catalog-driven ceiling (chosen)

Reuse the catalog fetch/cache, carry each target model's effort list through
`upstreamTarget` into the rewriter, and clamp there. One code path, no new
operator configuration, and a target change in the admin console takes effect
on the next catalog refresh plus reload.

Trade-off: a target whose catalog does not report levels is treated as a
`high` ceiling, which may under-use a level the upstream actually supports (for
example Gemini's `xhigh`), and catalog refreshes are best-effort.

### B. Per-node configured efforts

Add `node.efforts` and clamp against it. Deterministic and network-free, but it
moves the staleness from the client into cuttle configuration: changing the
node's model means editing its effort list too.

Rejected for now: does not deliver the requested "follow the target".

### C. A plus B override

Catalog first, optional node override for providers whose catalog is silent
(Gemini today). Strongest, but adds a config field, admin form, validation and
tests; can be added later without changing A's contract.

## Design

### Provider layer

`provider.Model.Efforts` already exists and is serialized; it is currently never
populated. Populate it from the model list:

- Extend `openAIModelJSON` with an optional raw `effort` field and an optional
  `capabilities.reasoning.effort` array.
- Accept these `effort` shapes:
  - object: `{"supported_levels":["low","high","max"],"default_level":"high"}`
    (DeepSeek);
  - bare array: `["low","high","max"]`;
  - string: comma- or space-separated levels.
- Accept `capabilities.reasoning.effort` (opencode-go/OpenRouter-shaped).
- Normalize: lowercase and trim, keep only the known levels
  `none|minimal|low|medium|high|xhigh|max`, deduplicate, sort ascending by rank.
  Malformed optional data is ignored and never fails the catalog.
- `modelItem` carries efforts; `Model.Efforts` is filled in `Models()`.
- Merge rules for disagreeing sources: within one catalog entry,
  `capabilities.reasoning.effort` wins over a top-level `effort` (it is the
  richer shape and the two agree in practice); across duplicate entries from
  different listing APIs, intersect the non-empty effort sets rather than
  unioning them, because a level only one endpoint accepts can produce a 400.
- Native Gemini decoding leaves efforts empty (unknown), even though the
  catalog reports `"thinking": true`.

### Gateway catalog

- Replace the output-limit-only catalog with per-model metadata:
  `catalogInfo map[providerID]map[modelID]catalogModel`, where
  `catalogModel{Output int; Efforts []string}`. Rename the cache to
  `catalogCache`; its TTL, retry, coalescing, prime and last-good semantics stay
  unchanged, and efforts are retained/refreshed with the limits.
- `fetchCatalogInfo` fills output plus efforts in one catalog read; merge and
  clone helpers preserve both.
- `compileNodeTargets` attaches `efforts` to every `upstreamTarget` variant from
  `catalog[providerID][targetModel]`. Effort metadata is deliberately model-wide
  (provider + model), not keyed by upstream wire API: vendor catalogs do not
  report per-API effort lists, so an API-keyed cache would fabricate precision.
  This is an accepted approximation for providers serving several APIs.
- `registerRewriter` passes `UpstreamEfforts` into `gwrewriter.Options`.

### Rewriter clamp

- New `internal/rewriter/effort.go` with
  `clampEffort(body []byte, efforts []string) ([]byte, error)`.
- Runs on request-direction bodies in `Rewriter.Rewrite`, including when the
  target declares no levels (the ceiling is then `high`), skipping Anthropic
  `count_tokens` requests (same guard as the model rewrite). A cheap substring
  pre-check for `effort`/`thinkingLevel` avoids parsing bodies that carry no
  level at all. It runs twice:
  - before `eng.Rewrite`, on the client body, so level-to-level conversions
    start from a value the target can accept;
  - after `eng.Rewrite`, on the converted body, because the converter can map
    effort upward (Anthropic `xhigh` becomes chat `max`). The second pass only
    acts when the converted body still carries an explicit level field; bodies
    converted to a token budget are left alone.
- Parses the body once as `map[string]json.RawMessage` and inspects explicit
  level fields wherever present:
  - `reasoning_effort` (chat),
  - `reasoning.effort` (responses-shaped),
  - `output_config.effort` (anthropic),
  - `generationConfig.thinkingConfig.thinkingLevel` (gemini).
- Ceiling = highest-ranked level in `efforts`, or `high` when the list is empty
  or the target declares no levels. A present level ranked above the ceiling is
  replaced with the ceiling's literal value; other fields are untouched. Marshal
  only when at least one field changed; a parse or encode error returns the
  original body. This gives the fixed resolution without a special case:
  `max` → `max`, else `xhigh`, else `high`, and a declared top below `high`
  becomes that top.
- Stats: read the client-selected level from the original body via the existing
  `requestEffort` before clamping, so `effort` keeps meaning "what the client
  asked for". Log a debug line with `sid`, route, node, requested and effective
  values when a clamp happens.
- Protocol conversion runs after the clamp, so the converter keeps owning
  cross-protocol effort mapping: Anthropic targets map `xhigh` to `max`, Gemini
  targets map `max`/`xhigh` to a 32000-token thinking budget.

### Client configuration (outside this repository)

`~/.config/opencode/opencode.json`: every cuttle model gains

```jsonc
"capabilities": {
  "tools": true, "input": ["text", "image"], "output": ["text"],
  "reasoning": { "supported": true, "effort": ["low", "high", "max"] }
}
```

OpenCode replaces its default `low/medium/high` variant list with this one and
sends the selected level as `reasoning_effort`. No variant is active by default,
so requests without a selection still send no effort. Config hot-reloads.

## Calls made by the builder

- Ceiling-only clamping rather than nearest-level remapping: predictable, and
  the client list used with Cuttle (`low`, `high`, `max`) never contains a gap
  value. Gaps are documented as a limitation.
- A silent catalog means a `high` ceiling. Missing metadata is treated as "no
  tier above high", so `max` still becomes `high` instead of reaching an
  upstream that may reject it.
- Stats keep the client level; the effective level is only logged. Dashboards
  keep answering "what did clients ask for".
- Renaming the catalog types to `catalogInfo`/`catalogCache` is accepted for
  honesty; the change is mechanical and covered by existing tests.
- DeepSeek's `default_level` is ignored — no effort is injected for clients that
  send none.

## Verification

- Provider decode tests: DeepSeek object shape, capabilities array, bare array,
  string form, malformed values, merge across duplicate model entries, native
  Gemini empty.
- Catalog cache tests: efforts survive prime/refresh and last-good retention;
  existing limit tests stay green.
- Build propagation test (extend `build_cap_test`): a fake catalog reporting
  efforts installs them on the registered rewriter, and an over-ceiling request
  is clamped end to end.
- Rewriter clamp table tests: each of the four fields; declared `max` → `max`,
  declared `xhigh` → `xhigh`, declared `high` → `high`, and a silent catalog
  (`max` → `high`); at/below ceiling unchanged; invalid JSON; byte-identical
  no-op when no clamp is needed; unrelated fields preserved. A dedicated case
  proves the post-conversion pass catches an upward mapping (Anthropic `xhigh`
  → chat `max` against an `xhigh` ceiling).
- Full Go gate with `GOWORK=off`: build, vet, test. No web change, so the web
  gate is unchanged.
- Post-deploy manual checks: `GET /api/providers/{id}/models` shows `efforts`
  for DeepSeek and opencode-go; a real `/v1/chat/completions` request with
  `reasoning_effort:max` to `cuttle/claude-sonnet-5-5` records `effort=max` and
  reaches DeepSeek unchanged; `cuttle/claude-fable-5` (Gemini, silent catalog)
  records `effort=max` and the debug clamp log shows the effective `high`.

## Limitations

- Targets without catalog level data (native Gemini catalog, `hungerg-free`)
  get a `high` ceiling. `claude-fable-5` therefore sends `high` for a client
  `max`, even though Google's published catalog for `gemini-4-hydrogen` includes
  `xhigh`; this under-uses the backend by design so that no undeclared level is
  ever sent. Raising it later is the deferred node-level override (approach C).
- Values below the ceiling that the target does not list (for example `medium`
  on DeepSeek) pass through.
- Chat inbound converted to an Anthropic target currently loses explicit effort
  entirely (converter v0.7.4 does not map `reasoning_effort`). No active route
  targets an Anthropic endpoint, so this change neither fixes nor exercises it.
  Anthropic→Responses collapses `xhigh`/`max` to `high`, which is a
  pre-existing over-clamp owned by the converter.
- The admin API now exposes `efforts`, but the console does not render it yet.

## Implementation record

Filled in per plan task.
