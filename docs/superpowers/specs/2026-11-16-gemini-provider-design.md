# Google Gemini provider

Date: 2026-11-16
Status: draft

## Intent

Add Google Gemini as a first-class provider type (`gemini`) so operators can
point the gateway at a Google AI account with one preset and route Claude Code,
OpenAI Chat and native Gemini clients through it. The goal is concrete: the
Apple-of-the-eye client is Claude Code (Anthropic inbound) reaching the Gemini
API; native Gemini inbound keeps working through the existing `*` entrypoint.

Audience: the go-gost/cuttle maintainer and any Google AI Studio / Google AI
plan subscribers who pay for Gemini API tokens through a Gemini API key.

The verifier: after this lands, `cuttle` should be able to create a `gemini`
provider from the console with zero hand-tuning and serve `/v1/messages`,
`/v1/chat/completions` and native Gemini paths end to end, with a clean model
catalog scoped to models that can actually generate content.

## Goals

- Register `gemini` as a provider type accepted by config validation, the
  provider factory, the console preset list, the web provider-type list and
  `README`/`example.yaml`.
- Preset carries two base URLs:
  - `gemini`: `https://generativelanguage.googleapis.com/v1beta` (native)
  - `chat`:   `https://generativelanguage.googleapis.com/v1beta/openai` (OpenAI-compatible)
  - `responses`:  (Google does not implement the Responses API; left empty so
    a Responses client falls back to the engine's conversion instead of 404ing)
  - `anthropic`:  (Google does not implement `/v1/messages`; left empty)
- Auth defaults:
  - `chat` → `Authorization: Bearer <key>` (Google's compatible layer rejects
    a key sent only as `x-goog-api-key`)
  - `gemini` → `x-goog-api-key` (Google's native API rejects Bearer)
- **Catalog from the native `/v1beta/models` endpoint only.** The compatible
  `/v1beta/openai/models` list is slim (no limits) and is not fetched for the
  catalog. Native list is paginated:
  - `GET {geminiBase}/models?pageSize=1000&pageToken=…` until `nextPageToken`
    is empty (usually one page today; do not rely on that).
  - Drop models whose `supportedGenerationMethods` does not include
    `generateContent` (embedding- and TTS-only models are not chat models).
  - Keep the existing `models/` prefix strip and the displayName fallback to id.
  - No price metadata exists; `Model.Price` stays nil. The model cards will
    show context/output limits only.
- Classify any non-empty model ID as `[Chat, Gemini]`. Effect on auto: chat-
  targeted nodes default to the compatible endpoint (streaming works), Gemini
  inbound gets the native target, Anthropic inbound falls back to the chat base
  and converts through the engine (which already handles the conversion).
- `UsageSupport("gemini")` returns a note explaining there is no per-key
  balance API ("Gemini API usage is billed per Google Cloud; AI Studio plan
  credits usable in AI Studio do not expose a remaining-quota endpoint").

## Non-goals

- Vertex AI (project/location-region endpoints, OAuth/ADC). Out of scope; the
  `gemini` type is the AI Studio / Gemini API key surface.
- Routing improvements for Gemini-native inbound (path-based model match,
  `count_tokens` for Gemini paths). The `*`-entrypoint limitation stays; see
  the node-protocol-auto-match spec. A Gemini-native client that asks for
  models from outside the catalog still gets a 502 with no entrypoint match.
- Forcing a streaming `streamGenerateContent?alt=sse` rewrite for Anthropic
  → Gemini targets. That requires a body-aware URL rewrite, which does not
  exist in x; the compatible chat target already serves streaming clients and
  the native base is for Gemini clients that can handle the JSON-array
  framing.
- A price table. Google does not publish per-model USD pricing in the catalog
  and a hand-maintained table will go stale; the card shows nothing instead.
- `count_tokens` for the Anthropic inbound with a Gemini target: stays the
  local estimate (unchanged behavior).

## Constraints

- Provider type and non-`generic` base URLs are immutable after creation; the
  preset therefore ships exactly two base URLs and no way to "add more".
- The model catalog cache is 10-minute, last-good, singleflight; nothing in
  this plan changes the caching layer.
- `config.providerTypes` and the provider factory must stay in sync with the
  web `PROVIDER_TYPES` list and the server-side `presets.go` — the same
  registry-count discipline as the OpenRouter provider.
- `listAll` stays `false` for this adapter. The catalog is fetched from the
  native Gemini base **only** — the chat (compatible) base is never attempted
  for listing, **with no fallback to chat on failure**: the existing
  last-good catalog cache keeps the provider serving until the native list
  succeeds, and the catalog error surfaces as before. The compatible base
  is configured purely as an upstream target; nothing reads its slim
  catalog.
- `config.Load` use `yaml.KnownFields(true)`: unknown fields keep failing
  loudly. The example.yaml provider block must use the current schema with
  explicit valid ids and id references.
- The web bundle is rebuilt into `internal/admin/dist`; changes to the web UI
  require `npm run build` before the Go build.
- The admin API is not changed; no new endpoints, no new fields.

## Approaches considered

### A. Preset-only (`type: generic` with two bases, docs only) (rejected)

Ships in an hour and works today for simple setups, confirmed by a live
scratch run: Claude Code straight to Gemini streams cleanly through the
compatible `/chat/completions` endpoint. But:
- The loader truncates the catalog at 50 models and includes embedding-only
  models that will 400 at request time.
- No provider-type row, no tailored catalog, no usage note; documents drift.
- The user explicitly asked for a first-class type.

### B. Dedicated `gemini` type, thin (chosen)

Scope captured in Goals above. ~1 working day. The adapter is a new file
(`internal/provider/adapter_gemini.go`), reuses `defaultAuth` for the chat
base (Bearer) and `geminiAuth` for the native base, and extends `models.go`'s
decode path with a pagination loop + capability filter. Discovery work is
minimal.

### C. Full stream-aware gateway integration (rejected)

Add a rewrite that selects `:streamGenerateContent?alt=sse` when the inbound
request has `stream: true`. This is the one thing standing between a native
Gemini target and a Claude Code stream, and it is not a provider-type change —
it belongs in the gateway build with its own design, and it would not be
needed if operators route Claude Code via the compatible chat base (the
supported path for this provider). Deferred as a separate change, possibly
paired with a path-based model matcher for Gemini inbound entrypoints.

## The build

Files changed, all in the cuttle repo unless marked.

1. `internal/provider/adapter_gemini.go` (new, ~120 lines): adapter with
   `classify`, per-API auth (Chat→Bearer via the default path; Gemini→
   `x-goog-api-key`), `listAll=false`, `catalogAPIs() []API{Gemini}` (see
   3a), and `keylessCatalog=false` — both endpoints require the caller's
   key.
2. `internal/provider/models.go` (extend):
   - `ModelsURL(base, api)` stays `base + "/models"`; the Gemini base is
     `…/v1beta` so the URL is already `…/v1beta/models` — correct.
   - `fetchModels` for `api == Gemini` gains pagination: send
     `?pageSize=1000` with `pageToken`, decode `nextPageToken`, loop until
     empty. One-shot to any size is fine (the doc-observed catalog is ~50
     models; the loop is for correctness, not volume). A failed page fails
     the whole listing (no partial catalog), as today.
   - `geminiModelJSON` gains `SupportedGenerationMethods []string \`json:"supportedGenerationMethods,omitempty"\``.
   - Filter: drop items where `supportedGenerationMethods` is non-empty and
     does not contain `generateContent`.
3. `internal/provider/provider.go` (extend): `case "gemini"` in the factory;
   `IsProviderType` is derived from `config.providerTypes` which gains the key.
3a. **Catalog-selection hook.** `listAPIs` (`internal/provider/models.go:93`)
   returns `[Chat]` for any non-`listAll` adapter when a Chat base is
   configured, so the gemini preset would otherwise catalog from the slim
   `/v1beta/openai/models` (embedding/TTS models included, no limits, no
   `models/` stripping) and the pagination work in item 2 would be dead code.
   Fix: add an **optional hint interface** (e.g. `catalogAPIs() []API`,
   returning nil when absent) and have `baseProvider.listAPIs()` honor it when
   present, falling back to today's logic. The gemini adapter returns
   `[]API{Gemini}`. This is deliberately an adapter hint and **not**
   `listAll=true` — both would fetch the compatible catalog (duplicate
   `models/`-prefixed IDs, slim metadata). Call sites outside the listing path
   are unchanged: `probeTarget()` still health-checks the Chat base (conscious
   decision — the verify button proves the key reaches the API; it is not
   intended to exercise the native path), `usageBody()` still forces
   `p.Auth(Chat)` per configuration, and auto-target resolution stays
   chat-first.
4. `internal/config/config.go`: `providerTypes` gains `"gemini"`.
5. `internal/provider/usage.go`: `UsageSupport("gemini")` returns a new note,
   e.g. `"Gemini API usage is billed per Google Cloud; AI Studio plan credits are for AI Studio. There is no per-key balance API."` — operator-facing, safe to show.
6. `internal/provider/capabilities.go`: unchanged — Anthropic count_tokens
   still applies only when the target API is Anthropic; Gemini targets always
   estimate locally.
7. Admin preset: add a `gemini` preset with
   `Gemini: "https://generativelanguage.googleapis.com/v1beta"` and
   `Chat:   "https://generativelanguage.googleapis.com/v1beta/openai"`.
8. Web (`web/src/pages/presets.ts`, `web/src/pages/*`):
   - `PROVIDER_TYPES` gains `'gemini'` (after `openrouter`).
   - No new UI surface besides two preset base URLs and the usual
     API dropdown (Chat and Gemini listed, both enabled).
9. `presets.ts`/`presets.test.ts`/`PROVIDER_TYPES` are unchanged in shape.
10. `README.md` provider table gains the row; a new subsection under
    "Provider usage and balances" explains the AI Studio credits caveat
    and the one caveat that matters operationally: **`node.api` must not pin
    `gemini` for a Claude Code route** — pinning it changes the client to the
    native path and breaks streaming; keep `auto`.
11. `example.yaml` gains a commented-out `gemini` provider block plus a
    node/route/route sketch? Keep it minimal: provider block only.
12. Tests:
    - `internal/provider`: classify; per-API auth (Chat→Bearer,
      Gemini→x-goog-api-key); catalog pagination across two pages; filter
      drops embedding-only models; native list still selected when a Chat
      base exists (regression guard for the 3a hint); price nil.
    - `internal/admin/server_test.go`: preset count 5→6 and
      `TestPresetsMatchSpec` updated (existing mechanical failure once the
      preset is added).
    - `internal/provider/provider_test.go:19`: hard-coded type list gains
      `"gemini"`.
    - `internal/admin`: gemini preset with the two base URLs and both APIs
      enabled.
    - Web: `presets.test.ts` gains a gemini entry consistent with the rest.
    - Gateway (recommended, cheap): Anthropic inbound with auto →
      Gemini-backed node targets `/v1beta/openai/chat/completions` (the same
      assertion style as
      `internal/gateway/provider_capabilities_test.go`).
13. Full Go gate: `GOWORK=off go build ./...`, `go vet ./...`,
    `go test ./... -count=1`; web gate: `npm test`, `npx tsc --noEmit`,
    `npm run build`.

## Verification bar

CI and the Go/web gates cover everything achievable offline. The headline
claim — an agentic Claude Code session through a `gemini` provider — is
verified manually, by the maintainer, **after** the change lands and the
binary is rebuilt:

1. Maintainer creates the `gemini` provider in the web console with a Gemini
   API key (Web → Providers → New → Gemini preset).
2. Maintainer configures one Claude Code-facing entrypoint/route bound to a
   Gemini model on that provider (`claude` entry pointing at it).
3. Maintainer runs a real Claude Code session with at least one agentic
   tool-use turn (a coding task with tool calls + tool results), confirming
   streaming works mid-session and token usage is recorded.

No live Google API calls happen in tests, CI, or this plan. If the smoke
check fails, the fix lands on the same branch before merge; the manual check
is a hard gate, not a formality.

## Documented limitations (to put in README)

- Responses/Anthropic clients go through the converter; `node.api: gemini` must
  not be pinned for Claude Code (streaming breaks).
- Gemini native clients are reachable only through `*` entrypoints (no body
  `model` to match); non-`*` entrypoint globs never match them.
- Embedding models are not listed in the catalog; they cannot be routed.
- The catalog does not carry prices or context cache pricing.
- AI Studio plan credits (Pro/Ultra) only apply inside AI Studio; API keys
  draw Cloud Billing against the $5 prepay plus any Google Cloud credits you
  have redeemed.

## Decisions left to the builder

- Exact operator-facing wording of the usage note (both languages).
- Whether the preset sets `enabled` default true — match the other vendor
  presets (it does).
- Whether to expose the Gemini catalog pagination as a query knob. Not by
  default; paging is internal.

## Approaches for approval

Short: **B** above (thin, native-catalog, chat-base-compat-for-clients) as
outlined. C rejected as a gateway change outside this design. A remains a
documentation recipe for users who do not want a type.
