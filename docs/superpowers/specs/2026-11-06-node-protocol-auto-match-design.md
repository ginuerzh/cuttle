# Node protocol auto-match design

Date: 2026-11-06
Status: proposed
Repository: `llm-gw`

## Intent

A node currently binds one provider model and one upstream wire API selected at
compile time. When a provider exposes several APIs for the same model (for
example DeepSeek exposes chat, responses and anthropic), the selection always
picks the first one with a configured base URL. As a result, Claude Code talking
Anthropic Messages to `/v1/messages` is converted to OpenAI chat and sent to
DeepSeek's chat endpoint even though DeepSeek serves a native Anthropic
endpoint.

The gateway must prefer the upstream API that matches the client's inbound
protocol, and let the operator pin a protocol when automatic matching is not
what they want. Automatic matching is the default for every node; existing
configs do not need a new field.

## Current behavior

- `internal/gateway/upstream.go:compileRouteNode` resolves exactly one target
  API per node.
- `selectAPI` takes the first entry of the model's static `APIs` list that has a
  configured base URL, otherwise the canonical order `chat, responses,
  anthropic, gemini`.
- `internal/gateway/build.go:upstreamRewrites` maps every known inbound path to
  that one target's canonical path.
- `internal/rewriter/rewriter.go:inboundProtocol` already knows the inbound
  protocol from the request URI, but only for statistics.
- Entrypoint matching uses only the request body's `model` field.

DeepSeek's adapter classifies its models as `chat, responses, anthropic`, so
with all three base URLs configured the first selection is always `chat`.

## Goals

- Auto-select the upstream API from the inbound wire protocol when the provider
  can serve it, without changing the client-facing entrypoint model.
- Allow a node to pin one API: `auto`, `chat`, `responses`, `anthropic` or
  `gemini`.
- Keep the routing chain and its invariants: entrypoint declaration order is the
  only precedence rule, route strategy/weight/backup semantics are unchanged,
  and x node names remain `<route>/<provider>/<node>[/<keyIndex>]`.
- Keep statistics truthful: inbound protocol and upstream protocol continue to
  be recorded separately.
- Expose the choice in the admin console and keep `example.yaml` loadable.

## Non-goals

- No per-entrypoint protocol override.
- No header-, host- or model-name-based protocol detection. The inbound path is
  the contract.
- No request-time retry with another API when the selected endpoint fails; x
  node failover keeps handling node failure.
- No catalog-driven routing. The data plane stays offline and uses the static
  adapter classification plus configured base URLs.
- No Gemini-to-chat body conversion fixes; the existing engine limitation is out
  of scope.
- No protocol conversion of Anthropic `count_tokens` bodies. Only the
  top-level model is substituted when the target is Anthropic.
- No path-based model extraction for Gemini entrypoints. Gemini requests still
  need a `*` entrypoint because entrypoint matching reads the body `model`
  field, which Gemini bodies do not have. That is a pre-existing limitation and
  the Gemini protocol group only affects catch-all entrypoints.

## Anti-goals

- Path must not become part of entrypoint precedence or user-visible routing.
- Do not introduce duplicate x node names or change `stats.parseNodeName`.
- Do not replace the x hop group/selector with a custom dispatcher.
- Do not silently pin a protocol when automatic matching is possible.

## Approaches considered

### A. Entrypoint protocol variants (chosen)

Every entrypoint expands into protocol-specific hop entries plus one fallback
entry. The matcher combines the existing model glob with a path regexp, and each
protocol entry points to a route variant compiled for that inbound protocol. A
route variant chooses, per node, the matching upstream API when available and
the default API otherwise.

Tradeoffs: hop and rewriter count grows with entrypoints × protocols; in
exchange the implementation stays on x matchers and the FIFO group selector,
entrypoint order remains the only precedence rule, and node names, stats and
topology are untouched.

### B. Node-level protocol variants

Each config node would compile into several x nodes carrying `Path` matchers.
The route hop stays singular, but one logical node appears several times in the
selector. A conversion fallback needs mutually exclusive path matchers, and
backup/weight/fail-marker semantics become hard to reason about. Rejected.

### C. Custom route-hop dispatcher

One entrypoint would keep one hop entry and a wrapper would inspect
`hop.SelectOptions.Path` before delegating to a precompiled variant. It is
smaller, but it introduces routing dispatch outside the x hop machinery, which
the repository explicitly forbids. Rejected.

## Design

### Configuration

Add an optional field to `config.Node`:

```yaml
nodes:
  - name: deepseek-fast
    provider: deepseek
    model: deepseek-v4-flash
    api: auto        # optional; auto is the default
```

- `api` accepts `auto`, `chat`, `responses`, `anthropic` and `gemini`.
- Values are trimmed and lowercased. `auto` and the empty string are equivalent
  and are normalized to the empty value so written configs stay minimal.
- An unknown value is a validation error naming the node.
- `auto` means per-request matching (below). A non-empty value pins the node to
  that upstream API for every inbound protocol.
- A pinned API requires the provider to have the corresponding base URL.
  Compilation fails with a message naming the node, provider and API otherwise.
- A pinned API is not checked against the static `Model.APIs` list. Typed
  adapters may have incomplete or outdated classification, generic providers
  have none, and the override is the escape hatch.

The field is JSON-visible on the admin API (`api`, omitted for auto) so the
console can create and update it without a new endpoint.

### Inbound protocol groups

| group | inbound paths | preferred upstream API |
| --- | --- | --- |
| chat | `^/v1/chat/completions$` | `chat` |
| responses | `^/v1/responses$` | `responses` |
| anthropic | `^(/v1/messages|/v1/messages/count_tokens)$` | `anthropic` |
| gemini | `^/v1beta/models/[^/]+:(generateContent|streamGenerateContent)$` | `gemini` |
| fallback | anything else | node default |

The path is matched with `PathRegexp` against `req.URL.Path`, so query strings
do not participate. Entrypoint model matching still reads the body `model`
field, so the Gemini group is only reachable from a `*` entrypoint; this is the
same limitation Gemini already has today.

`internal/rewriter.inboundProtocol` also classifies
`/v1/messages/count_tokens` as `anthropic` for statistics.

### Target API selection

For each enabled node and each inbound group:

1. If `node.api` is pinned, use it for every group, including the fallback. The
   pin is the effective default; the legacy `selectAPI` default resolution is
   not consulted at all. This lets `api: anthropic` rescue a model whose static
   classification or configured base URLs would otherwise produce no default.
2. Otherwise compute the default API with the existing rule: the first entry of
   the model's static `APIs` list with a configured base URL, then the canonical
   order `chat, responses, anthropic, gemini`.
3. For the fallback group, use the default API.
4. For a protocol group, use the matching API when the provider has its base
   URL and the model does not statically exclude it. A model with a non-empty
   static `APIs` list that omits the protocol keeps the default API. A model
   with no static classification (generic providers, unknown models) uses the
   matching API when its base URL is configured.

The last rule is deliberate: generic providers have no static model metadata,
and automatic matching must work for them too. Operators can pin `api` when a
configured base URL does not actually serve a model.

A pinned node only requires the pinned API to have a base URL. An auto node
whose default API cannot be resolved fails exactly as today.

### Compilation and hop entries

`buildHopGroup` compiles each route once per needed inbound group.

- A route variant is needed only when at least one enabled node's target API
  differs from that node's default API. Routes whose nodes are all pinned, or
  whose nodes cannot serve any other protocol, produce no extra variants.
- Route variants are cached by `(route name, group)` and shared by entrypoints.
- The default variant is always compiled.
- For every entrypoint, in declaration order, entries are appended in this
  order: chat, responses, anthropic, gemini (only when the variant exists), then
  the fallback entry.
- Protocol entries use `PathRegexp(...) && BodyJSON("model", pattern)`; the
  fallback entry keeps the current body-only matcher, and a `*` entrypoint uses
  the path regexp alone.
- Because protocol entries precede the fallback within one entrypoint and
  protocol path sets are disjoint, a request is served by at most one entry per
  entrypoint and FIFO keeps entrypoint order as the only precedence rule.
- Route node order, strategy, weight and backup metadata are unchanged. A mixed
  route (some nodes native, some converting) keeps the same node pool for every
  protocol; nodes that cannot serve the inbound protocol natively fall back to
  their default API. This preserves route semantics; operators who need strict
  native routing should not mix incompatible nodes in one route.

x node names remain `<route>/<provider>/<node>[/<keyIndex>]`. Route variants
live in separate hops, so names never collide in a selected hop and
`stats.parseNodeName` is unaffected.

### Rewriter registration

Body rewriters are currently registered per `(route, node)` and must stay
aligned with the path and target API that produced the x node. x's rewriter
registry resolves names lazily, so reusing a name would mutate the live hop
before the new hop is swapped in.

- The logical key is `r/<route>/<variant>/<node>`, where `variant` is `default`
  or the protocol group name.
- The registry key is namespaced by build generation:
  `b/<generation>/r/<route>/<variant>/<node>`.
- A build registers only new-generation names; it never unregisters or replaces
  a name the old hop may still resolve. Builds are serialized by a mutex.
- After a successful build the hop is swapped, and the old generation's names
  are unregistered after the 30-second in-flight drain.
- A failed build unregisters only the names it registered; the old hop and its
  rewriters remain untouched.
- Registration and hop construction consume the same compiled route variant
  plan, so the target API used for URL rewrites, body conversion and stats can
  never diverge.

### Path rewrites

`upstreamRewrites` keeps mapping all known inbound paths to the selected API's
canonical path. One addition is required:

- `/v1/messages/count_tokens` maps to `<anthropic base>/v1/messages/count_tokens`
  (respecting a base already ending in `/v1`).
- For non-Anthropic targets it remains a pass-through, as today.

Anthropic `count_tokens` bodies are not protocol-converted, but the client model
must still be mapped or the upstream rejects the request. When the selected
target API is Anthropic and the request URI is `/v1/messages/count_tokens`, the
in-process rewriter replaces only the top-level `model` field with the node's
target model and leaves every other field's value unchanged. This is implemented
in `internal/rewriter` (the engine deliberately skips non-chat paths): add the
target model to `rewriter.Options` and apply the substitution before delegating
to the engine. Other target APIs keep today's byte-for-byte pass-through.

### Statistics and observability

- No schema change.
- Upstream `protocol` continues to come from the rewriter target API.
- `inboundProtocol` continues to come from the request URI and gains
  `count_tokens`.
- Requests already display both protocols; the topology and rollups are
  unaffected.

### Admin console

- `Node` in `web/src/api/types.ts` gains `api?: 'auto' | 'chat' | 'responses' |
  'anthropic' | 'gemini'`.
- Node create/edit forms gain a "Target API" select: `auto` plus the provider's
  configured APIs. `auto` is the initial value when the field is absent.
- The existing derived-target hint becomes:
  - pinned: `Target API: <api> (fixed)`;
  - auto: `Target API: auto (match client protocol; default <api>)`.
- The node list shows an `auto` or pinned-API tag.
- `genericCanonicalNote` is reworded: generic providers still have no static
  model classification, but auto now matches configured base URLs by inbound
  protocol and falls back to the canonical order.
- UI source strings stay Chinese and are translated in
  `web/src/locales/en-US.ts`.

### Documentation

- `README.md`: explain auto matching in the routing overview, provider/node
  reference and example config.
- `example.yaml`: document `api` as optional with `auto` behavior.
- `AGENTS.md`: refine the entrypoint-routing invariant to say path never affects
  precedence but selects the upstream wire API, and document the new node field.

## Error handling

- Invalid `node.api`: config validation error naming the node.
- Pinned API without a provider base URL: gateway build error naming the node,
  provider and API. Reload keeps the previous data plane, as today.
- An auto provider/model with no usable default API: unchanged build error.
- Disabled provider, node or model: skipped in every variant, as today.
- A route with no usable nodes: the entrypoint remains compiled and requests
  fail rather than falling through to another entrypoint, as today.

## Compatibility

- Existing configs load unchanged; auto is the zero value.
- Behavior changes only when a provider configures more than one API base URL
  and the model can be served by the matching protocol — the intended fix.
- `api: auto` writes are normalized away, so generated configs stay clean.
- Stats databases, node names, topology IDs and admin endpoints are unchanged.

## Testing

- Config: enum validation, normalization of `auto`, example config load,
  admin create/update round-trip.
- Gateway (httptest upstreams, no real keys):
  - DeepSeek-like provider with chat/responses/anthropic bases and an auto node:
    `/v1/messages` hits the anthropic path with an Anthropic body,
    `/v1/responses` hits responses, `/v1/chat/completions` hits chat, and stats
    record the matching inbound/upstream protocols.
  - Provider with only chat configured: Anthropic inbound still converts to
    chat (regression guard).
  - Static model classification that excludes a protocol: falls back to the
    default API.
  - Generic/unknown model with a configured matching base: uses it.
  - Pinned `api: chat`: Anthropic inbound converts to chat even when an
    anthropic base exists.
  - Pinned `api: anthropic` with only an anthropic base and a model whose static
    list has no usable default: compiles and serves.
  - Unknown path: fallback entry and pass-through path.
  - `/v1/messages/count_tokens` with an Anthropic target: path is rewritten,
    the body keeps its Anthropic shape, and only `model` changes.
  - `/v1/messages/count_tokens` with a non-Anthropic target: body stays
    byte-for-byte unchanged (regression guard).
  - Gemini inbound through a `*` entrypoint selects the Gemini group; a
    non-`*` entrypoint keeps the existing body-model limitation.
  - Route with disabled nodes and backup nodes keeps existing behavior.
- Rewriter: every protocol variant points at the rewriter whose target protocol
  matches the rewritten path.
- Web: helper tests for the auto/pinned hint and target option list; TypeScript,
  Vitest and build gates.
- Full gates: `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1`;
  `npm test`, `npx tsc --noEmit`, `npm run build`.

## Deferred

- Garbage-collecting stale rewriter registry keys.
- Adding a default `anthropic-version` header for converted or native Anthropic
  requests when the client did not send one.
- Per-entrypoint protocol preferences.
- Gemini inbound body conversion.
