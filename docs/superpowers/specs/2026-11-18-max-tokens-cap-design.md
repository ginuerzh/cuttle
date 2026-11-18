# Design: clamp client `max_tokens` to the target model's output limit

## Intent

A client that asks for model `claude-opus-5-5` may send `max_tokens: 256000`. When
cuttle routes that request to a different model — today `glm-5.3` on `oc-go-glm`
— the target model rejects it before inference because its catalog declares
`limit.output: 131072`. OpenCode CLI reproduced this on both the Anthropic and
OpenAI upstream wire APIs; Claude Code on the same route stayed under the limit.
The gateway should lower an oversized output budget to the target model's
catalog limit so the request can succeed.

## Goals

- Clamp only the output-token field of an outbound request body to the compiled
  target model's catalog `Output`, and never raise a smaller value.
- Cover every outbound wire shape cuttle can emit: Anthropic `max_tokens`,
  OpenAI Chat `max_tokens` / `max_completion_tokens`, Responses
  `max_output_tokens`, and Gemini `generationConfig.maxOutputTokens`.
- Apply the clamp to converted requests **and** same-protocol passthrough, in
  one place, with no change to sibling repositories and no new config fields.
- Best-effort: catalog lookup failures must never block routing or fail a
  build.

## Non-goals / anti-goals

- No config schema, admin API, or console changes. No per-node / per-provider
  manual limit field.
- No change to `llm-api-converter` (no new tag, no `go.mod` bump, no local
  `replace`).
- No change to retry codes, error classification, routing, or client identity.
- No clamping of context/input token fields, no field injection, and no
  rewriting of response bodies or SSE frames.
- Not a fix for the client's own configuration; clients may still send smaller
  values.

## Constraints and current behavior

- `internal/provider/provider.go:40-57`: `provider.Model.Output` carries
  `limit.output`; `provider.Provider.Models(ctx)` is the network catalog call.
- `internal/provider/models.go:405-426`: `limit.output` is parsed into
  `Model.Output` (with fallbacks for `max_output_tokens` and OpenRouter's
  `top_provider.max_completion_tokens`).
- `internal/gateway/upstream.go:43-107`: `compileNodeTargets` builds one
  `upstreamTarget` per node/API using `provider.New(p)`, `Resolve` and
  `ModelDisabled`; it never calls `Models`, and `upstreamTarget` has no limit.
- `internal/gateway/build.go:59` builds the hop group;
  `registerRewriter` (`:760-775`) passes `TargetModel` but no limit to
  `internal/rewriter.Options` (`:31-41`). `newNodeEngine` (`:777-788`) sets a
  default `MaxTokens: 8192`.
- `internal/rewriter/rewriter.go:58-128`: `Rewrite` calls the converter engine,
  then captures `res.Data`. Same-protocol requests take the converter's
  passthrough path, which preserves a client `max_tokens` unchanged.
- The admin catalog cache (`internal/admin/server.go:69-71`,
  `internal/admin/models.go`) is private to `admin.Server` and created after
  the gateway (`main.go:83,103`), so the data plane cannot reuse it.
- `llm-api-converter/engine.Options.MaxTokens` is a fallback, not a cap; client
  values win in every conversion path and in passthrough
  (`convert/openai_to_anthropic.go:191-199`, `convert/convert.go:683-715`).
- `internal/config/config.go` has no node/provider output-token field and uses
  `yaml.KnownFields(true)` (`:348,361`), so an unknown YAML key would fail.

## Approaches considered

1. **Gateway build-time catalog lookup + one clamp in the rewriter (chosen).**
   Fetch catalog limits while building the hop group, thread the target limit
   through `upstreamTarget` into `rewriter.Options`, and clamp the converted
   request body once before capture. One choke point covers conversions and
   passthrough; the change stays inside cuttle. Trade-off: a few extra catalog
   requests on build/reload, and caps can be stale between reloads.
2. **Add `MaxTokensCap` to `llm-api-converter` and publish a tag.** Semantically
   tidy, but requires a sibling-repo change, a release, a `go.mod` bump and a
   second commit; the engine also skips non-chat paths today, so several
   conversion sites would need separate handling.
3. **Static `node.maxOutputTokens` in config.** No network dependency, but
   operators must maintain every model's limit by hand and schema, admin API
   and UI all change; values drift from the catalog.

Approach 1 was selected by the human partner.

## Design

### 1. Build-time catalog limits (gateway)

- In `internal/gateway`, add a small resolver that, given the config, returns
  `map[providerID]map[modelID]int` for **providers actually referenced by
  enabled nodes**. Create the provider with `provider.New(p)` and call
  `Models(ctx)` once per provider (deduplicated).
- Call it at the start of `buildHopGroup` with a bounded context created
  inside the build (a single overall timeout, e.g. 5s). No caller signature
  change: `Gateway.New`/`Reload` and existing `buildHopGroup` callers stay as
  they are.
- On a provider error, timeout, missing model, or `Output <= 0`: record no cap
  for that model, log one warning at build time, and continue. Do not fail the
  build. No last-good cache and no retry: reloads are infrequent, and a stale
  network failure should not silently keep an old limit.
- Extend `upstreamTarget` with `maxOutputTokens int` and pass the resolved cap
  into `compileNodeTargets`. Keep the existing no-limit call path used by unit
  tests working (e.g. a thin wrapper or an explicit empty limits map).

### 2. Thread the cap to the rewriter

- Add `MaxOutputTokens int` to `internal/rewriter.Options` and set it from the
  compiled `upstreamTarget` in `registerRewriter`.
- The cap belongs to the compiled node target, not to the route: different
  protocol variants of one node share the same model limit.

### 3. Clamp in `Rewriter.Rewrite`

- Add a helper (new file, e.g. `internal/rewriter/max_tokens.go`) that receives
  the request body, outbound `Protocol` and cap, and returns the body with the
  output field lowered when needed.
- Run it only when `meta.Direction` is `request`, `cap > 0`, and the URI is not
  an Anthropic `count_tokens` path (reuse `isCountTokensURI`); run it on
  `res.Data` after `eng.Rewrite` and **before** `captureRewrite`, so captures
  show the body actually sent upstream.
- Wire fields, checked in this order and only when already present:
  - `anthropic` → `max_tokens`
  - `chat` → `max_tokens`, else `max_completion_tokens`
  - `responses` → `max_output_tokens`
  - `gemini` → `generationConfig.maxOutputTokens`
- A present positive integer greater than the cap becomes the cap. A smaller
  value, zero, a non-integer, an absent field, or `cap <= 0` leaves the body
  byte-for-byte unchanged. Never add a field that was not in the body, and
  never touch other fields.
- If JSON parsing fails, log at debug/warn and return the original body; a
  malformed body is not a reason to fail the request here (the converter or
  upstream already handles it).
- Re-marshalling may reorder JSON keys; that is acceptable for request bodies.

### 4. Observability

- Build-time: one warning per provider whose catalog could not be read, and
  one when a configured target model has no usable `Output`.
- Request time: no new stats columns and no info-level log per request; the
  capture records the clamped body, which is the durable evidence.

## Testing

- `internal/rewriter`: table-driven tests for the clamp helper covering each
  protocol's field name, oversize vs under-limit values, no field injection,
  Gemini nesting, malformed JSON, and the four "do nothing" cases above.
- `internal/rewriter`: a test through `Rewrite` proving a converted request is
  clamped before capture, and that response-direction and `count_tokens`
  bodies are untouched.
- `internal/gateway`: catalog resolver tests with an `httptest` provider
  catalog (cap populated, provider failure → no cap, missing model → no cap,
  one fetch per provider), plus a build test proving the cap reaches the
  rewriter options from `compileNodeTargets`.
- Full Go gate per `cuttle/AGENTS.md` with `GOWORK=off` (`go build ./...`,
  `go vet ./...`, `go test ./... -count=1`). No web bundle change, so no web
  gate needed.
- Manual production verification after deploy: send the previously failing
  `/v1/chat/completions` request with `max_tokens: 256000` through the
  `claude-opus` entrypoint and expect 200 with a capture showing
  `glm-5.3` + `max_tokens: 131072`.

## Left to the builder

- Concrete helper/type names, exact catalog timeout constant, and log wording.
- Exact test fixtures and whether the empty-limit test path uses a wrapper or
  an explicit `nil` map.
