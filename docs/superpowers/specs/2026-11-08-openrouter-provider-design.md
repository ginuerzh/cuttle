# OpenRouter provider

Date: 2026-11-08
Status: draft

## Intent

Add OpenRouter as a first-class provider type so operators can route models
such as `anthropic/claude-sonnet-4` and `openai/gpt-4o` through the gateway
using an OpenRouter API key, with the same catalog, protocol matching and
usage visibility as the existing vendor providers.

## Goals

- Register `openrouter` as a provider type accepted by config validation and
  the provider factory.
- Add a console preset with the three native OpenRouter endpoints:
  - Chat: `https://openrouter.ai/api/v1`
  - Responses: `https://openrouter.ai/api/v1`
  - Anthropic Messages: `https://openrouter.ai/api`
- Authenticate all three APIs with `Authorization: Bearer <key>`, matching
  OpenRouter's documented contract (including its Anthropic-compatible path).
- List the public OpenRouter model catalog and expose context length, maximum
  output, input/output modalities, USD-per-million prices and per-model
  endpoint capabilities.
- Query `GET /api/v1/key` per configured key and show the key's remaining
  limit, used percentage, or all-time spend when the key has no limit.
- Keep the gateway's existing protocol conversion as the fallback when a
  model does not advertise a native endpoint.
- Answer Anthropic `count_tokens` requests locally when the selected route
  uses OpenRouter, because OpenRouter does not implement
  `/v1/messages/count_tokens`.

## Non-goals

- Account-wide credits from `GET /api/v1/credits`. That endpoint requires a
  management key; normal API keys are the supported deployment unit. The
  Usage page therefore reports per-key data.
- OpenRouter-specific routing preferences (`provider.order`, fallbacks,
  `route`, `transforms`, prompt caching controls). Operators can still send
  these as body fields; the gateway does not rewrite them.
- OpenRouter app-attribution headers by default. `provider.headers` and
  `auth.extra` remain available for `HTTP-Referer` / `X-OpenRouter-Title`.
- A dedicated OpenRouter UI page or model browser. The existing provider,
  model catalog and node pickers are reused.
- Gemini. OpenRouter does not expose a Gemini-compatible endpoint.

## Constraints

- Provider `type` and non-`generic` base URLs are immutable after creation;
  the preset must therefore be correct at create time.
- `config.providerTypes`, the provider factory, the web `PROVIDER_TYPES` list
  and the server presets are four separate registries. All must stay in sync.
- Node models can contain `/` (for example `anthropic/claude-sonnet-4`); the
  existing gateway uses node names, not model IDs, in rewrite keys.
- The model catalog is public, but sending an invalid bearer token can make
  some providers reject it. Follow the OpenCode pattern: omit the key for the
  catalog while keeping configured extra headers.
- The gateway's static `Resolve` has no network access. Per-model endpoint
  refinement can only come from the catalog; the static fallback must keep
  auto protocol variants compilable.

## Approaches considered

### A. Full native integration (chosen)

Register an adapter with all three endpoints, parse OpenRouter's richer model
metadata, and query `/api/v1/key` for usage. Auto mode can use Chat, Responses
or Anthropic natively; the existing converter remains the fallback.

Trade-off: a new usage parser and several catalog fields, but no new gateway
mechanism.

### B. Chat-only provider

Register only the OpenAI-compatible Chat endpoint. Anthropic and Responses
clients still work through body conversion. Smallest change, but loses native
Anthropic/Responses behavior and OpenRouter's advertised endpoint metadata.
Rejected because the user selected full integration.

### C. Generic provider with an OpenRouter preset

Ship only a preset and tell operators to use `type: generic`. No new backend
code. Rejected because there is no native usage query, model prices and
modalities are incomplete, and the type would not appear as a first-class
provider.

## Provider registration

- Add `openrouter` to `internal/config.providerTypes` and
  `internal/provider.New`.
- Add `openRouterAdapter`:
  - `classify`: any non-empty model ID → `[Chat, Responses, Anthropic]`;
    this is the static fallback used by the gateway before a catalog is
    loaded.
  - `auth`: `bearerAuth()` for every API, including Anthropic.
  - `listAll`: false (list from the Chat base).
  - `keylessCatalog`: true.
- Add the server preset and web provider type:
  ```text
  chat:      https://openrouter.ai/api/v1
  responses: https://openrouter.ai/api/v1
  anthropic: https://openrouter.ai/api
  gemini:    (empty)
  ```
- The Anthropic base intentionally stops at `/api`: the gateway appends
  `/v1/messages`, while `ModelsURL` appends `/v1/models` when needed.

## Model catalog

`GET https://openrouter.ai/api/v1/models` returns entries with `context_length`,
`architecture`, `pricing`, `top_provider` and `endpoints`.

- Extend the OpenAI-compatible decoder with optional fields:
  - `architecture.input_modalities` / `output_modalities`
  - `top_provider.max_completion_tokens` (and context fallback)
  - `pricing.prompt` / `pricing.completion` (USD per token)
  - `endpoints`
- Convert per-token prices to the catalog's USD-per-million representation by
  multiplying by 1,000,000. Keep the existing `cost` metadata path as a
  fallback.
- Map endpoint paths:
  - `/api/v1/chat/completions` → `chat`
  - `/api/v1/responses` → `responses`
  - `/api/v1/messages` → `anthropic`
- If an entry declares endpoints but none map to a supported text API, drop it
  from the catalog (for example image/batch-only models). Entries without an
  `endpoints` field keep the existing source-API and static-classification
  behavior.
- In `Models`, prefer per-entry endpoints when present, then the adapter's
  static classification, then the source API.

## Usage

Add `openrouter` to `UsageSupport` and `usageAccount`.

- Endpoint: the Chat base plus `/key`, i.e.
  `https://openrouter.ai/api/v1/key`, authenticated with the same bearer auth.
- Parse `data.limit`, `data.limit_remaining`, `data.limit_reset`,
  `data.usage`, `data.is_free_tier`.
- Key with a positive limit and a remaining value:
  - `BalancePart{Currency: "USD", Total: max(0, limit_remaining)}`.
  - One `QuotaWindow` named from `limit_reset` (`daily`, `weekly`, `monthly`)
    or `key limit` when unknown, with
    `UsedPercent = clamp(100 * (limit - remaining) / limit)`.
  - No reset timestamp: OpenRouter reports the reset policy, not the next
    reset instant, on this endpoint.
- Key without a limit:
  - No balance or window; set `AccountUsage.Note` to
    `unlimited key · all-time usage $X` (or `unlimited key` when usage is not
    reported).
  - Expose `note` in `accountUsageView`, `AccountUsage` (web type) and render
    it under the key preview.
- Error behavior, redaction, caching, stale fallback and timeouts stay
  unchanged.
- Add `daily` and `key limit` window labels and translations.

## Gateway behavior

- No gateway routing change is expected except count_tokens capability:
  `routeNeedsCountTokensEstimate` must treat OpenRouter as having no native
  Anthropic counter even when the Anthropic target is compiled.
- `selectAPI` defaults to Chat because it is first in the canonical order.
- Auto mode compiles Responses and Anthropic variants when the corresponding
  base URL exists and the static classification includes the inbound API.
- Anthropic `count_tokens` is estimated locally for OpenRouter routes. Add a
  provider-level capability helper (false for `openrouter`, true otherwise)
  and make the route policy consult it. This keeps native Messages routing
  while avoiding OpenRouter's 400/404 on the counter endpoint.
- OpenRouter's Responses API is stateless; the gateway does not persist state
  and does not add `store` or `previous_response_id`.

## Admin console

- The provider type select and preset list gain `openrouter`.
- The existing provider edit page shows the three locked base URLs.
- Model cards show context, max output, modalities and USD/M prices from the
  catalog.
- The Usage page shows the per-key remaining limit/window, or the unlimited-key
  all-time spend note.
- All new user-facing strings go through `t()` with zh-CN and en-US entries.

## Testing

- Provider adapter: classification, bearer auth for all three APIs, catalog
  keyless behavior, OpenRouter metadata parsing, endpoint filtering, price
  conversion.
- Factory/config: `openrouter` accepted; preset count and exact URLs.
- Gateway: Anthropic inbound compiles the `/api/v1/messages` target with bearer
  auth; Responses inbound compiles the `/api/v1/responses` target;
  `count_tokens` is estimated locally for an OpenRouter route.
- Usage: limited key (balance + window), unlimited key (note), `/key` path and
  auth header, malformed reply, redaction.
- Web: provider type/preset lock; usage note rendering; window labels.
- Full gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`,
  `GOWORK=off go test ./... -count=1`, `npm test`, `npm run build`.

## Documentation

- README provider table: add `openrouter`, the three base URLs and the
  per-key usage contract.
- README Usage section: clarify that OpenRouter uses `/api/v1/key` and
  management-key account credits are out of scope.
- `example.yaml`: optional commented OpenRouter provider block.
- Memory note `llm-gw-provider-capabilities` gains the adapter and preset
  facts; `llm-gw-provider-usage` gains the `/key` contract.

## Decisions left to the builder

- Exact Chinese wording for `key limit` (proposed `密钥限额`) and the unlimited
  usage note.
- Whether the Usage card should show all-time usage as a note or as a separate
  row; a note is sufficient here.
- Whether the model catalog should also expose `pricing.input_cache_read`;
  outside the current `Price{input, output}` shape.
