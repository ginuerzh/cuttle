# Route retries (upstream 429/5xx) — design

Date: 2026-11-10
Repositories: `ginuerzh/cuttle`, `go-gost/x`
Status: proposed

## Intent

Cuttle currently has no way to retry an upstream request. A node that returns
`429` or `502` is marked failed by `failCodes`, so *later* requests skip it, but
the request that hit the error is relayed to the client unchanged. Clients see a
transient upstream failure even when another node, provider key or backup node
could have served it.

The goal is a per-route retry budget, configured like the existing selector
settings (`strategy`, `maxFails`, `failTimeout`), where every retry re-runs
normal node selection so the existing selector strategy decides which node
serves the next attempt. Request records must say whether the recorded request
was retried.

## Goals

- Route-level `retries` (extra attempts) and `retryCodes`.
- Retries re-select through the existing hop/selector, so `fifo`, `round`,
  `random`, `sticky`, `backup`, `weight`, `failCodes`, `maxFails` and
  `failTimeout` keep their meaning.
- Only retry before any response byte reaches the client (status-based retry,
  not stream-resume).
- Rate-limit (`429`) and transient upstream (`502`/`503`/`504`, broadly `5xx`)
  responses become the default retry triggers.
- One request record per client request, with a `retries` count so a retried
  request is identifiable in Sessions and in the stats JSON.

## Non-goals

- No retry after the response started streaming (SSE cannot be rewound).
- No resume of a broken stream, no idempotency keys, no request de-duplication.
- No backoff, jitter or `Retry-After` parsing in v1.
- No retry for TLS-passthrough traffic, HTTP/2 (`PRI`) connections or the local
  `count_tokens` estimate path.
- No retry at the route/entrypoint matcher layer; routing precedence is
  untouched.

## Anti-goals

- Retrying a request whose upstream may already have charged for generation
  must not happen silently more than the operator configured. `retries` is
  opt-in per route and defaults to `0`.
- Do not duplicate routing logic in cuttle to find the retry policy.
- Do not record one SQLite row per attempt (it would inflate request counts and
  diverge `requests` from `rollups`).

## Constraints from the repository

- The data plane is the x `forward` handler plus the x hop group and selector.
  Cuttle must not replace that machinery with a custom dispatcher.
- Cuttle pins `github.com/go-gost/x` by tag (`v0.30.0` today) and has no `replace`
  directives. A behavior change in x must be published as a new tag and bumped
  in `cuttle/go.mod`.
- `internal/admin/dist` is embedded; console changes require a rebuild before
  the Go gate.
- Stats columns are additive via `ensureColumns`; old rows must read defaults.
- `requests.request_id` is the primary key and writes use `INSERT OR REPLACE`,
  while `rollups` are incremented per insert. Multiple rows for one client
  request would therefore overwrite the raw row and double-count rollups. This
  is why the design records exactly one row per client request.

## Approaches considered

### A. Retry inside the x HTTP forwarder, policy carried in node metadata (chosen)

- Cuttle stamps `retry.max` and `retry.codes` on every node of a route (same
  mechanism as `weight` and `backup` today).
- x's `HandleHTTP` reads the budget from the selected node, buffers the client
  request once, and on a retryable status marks the node, discards the response
  and loops: re-select through the same hop, re-dial, re-send, produce the final
  response.
- Selector semantics are exactly preserved because the retry is just another
  pass through `hop.Select`. No routing knowledge is duplicated.
- Cost: one x release (`v0.31.0`) and a `go.mod` bump in cuttle. The tag push
  requires explicit approval (the workspace rule is "never push unless asked").

### B. Retry in a cuttle wrapper handler

- A new handler above the x forwarder buffers the request and replays it into
  `next.Handle`; a net.Conn wrapper parses the response head and discards
  retryable responses. The route's retry policy is learned from the first
  attempt via a context value written by the body rewriter.
- Cost: no x release, but response-head interception, 100-continue, write
  boundaries and per-attempt stats are all hand-built in cuttle, and x would
  still record a row per attempt (same primary key, inflated rollups), which
  needs extra suppression logic. Considered too fragile for the same feature.

### C. Global gateway retry setting

Rejected: it cannot express per-route policy and the user explicitly asked for
route-level retry settings.

## Design

### Configuration (`cuttle/internal/config`)

`routes[]` gets two fields:

```yaml
- name: claude
  nodes: [claude-a, claude-b]
  strategy: round
  maxFails: 1
  failTimeout: 10s
  retries: 2                 # extra attempts; 0 disables retries (default)
  retryCodes: "429,5xx"      # same syntax as http failCodes; default 429,5xx
```

- `retries`: `0..10`, default `0`. Total upstream attempts = `retries + 1`.
- `retryCodes`: comma-separated exact codes and hundred-level wildcards
  (`5xx`). Default `429,5xx`. Validated at load time; invalid tokens are an
  error naming the route (unlike x's lenient parser, which only logs).
- `applyDefaults` fills `retryCodes` when empty. `retries == 0` means the codes
  are irrelevant but still stored.
- Unknown-field strictness and the existing validation order are unchanged.
- Admin route create/update decode `config.Route` wholesale, so the fields flow
  through without handler changes; `GET /api/routes` returns them. The console
  edit form and the route list subtitle gain the two fields. `example.yaml` must
  keep loading.

Cuttle compiles the policy into node metadata in `internal/gateway/build.go`:

```
retry.max   int    route.retries
retry.codes string route.retryCodes
```

Metadata is attached only when `retries > 0` (absence means no retry). Every
node of the route variant, including backup nodes and per-key nodes, receives
the same values; the route is the policy owner, the node is just the carrier.

### x forwarder behavior (`x/internal/util/forwarder/sniffer_http.go`)

The retry budget is read from the first selected node of a client request
(`mdutil.GetInt(node.Options().Metadata, "retry.max")`, capped by cuttle at 10;
x itself does not impose a policy maximum). Codes come from
`mdutil.GetString(..., "retry.codes")`, defaulting to `429,5xx` when absent.

Per client request, `HandleHTTP` keeps:

- `replay []byte` — the pristine request (head + buffered body) serialized once;
- `budget int` — remaining extra attempts for this client request;
- `retries int` — attempts already spent (recorded on the final row);
- `reqStart time.Time` — start of the first attempt, so the final row's
  duration covers every attempt.

Buffering (only when the selected node has `retry.max > 0`):

- If the request is not replayable, leave it untouched and use `budget = 0`:
  TLS passthrough, HTTP/2 `PRI`, `Expect: 100-continue`, and bodies above the
  cap (32 MiB, the same order as the local `count_tokens` limit).
- Otherwise read the body to memory, set `ContentLength`, clear
  `TransferEncoding`/`Transfer-Encoding`, restore a reader, and serialize the
  whole request with `req.Write` into `replay`. Each attempt starts from a fresh
  `http.ReadRequest` over those bytes, so per-node header/URL/body rewrites are
  reapplied to the untouched request and a retry can target a different
  provider, model or key.
- If the body is chunked/unknown length and exceeds the cap while buffering,
  restore `bytes.Reader(prefix) + original body` and disable retries for this
  request.

Attempt loop (inside the existing keep-alive loop):

1. Dial the selected node (existing path).
2. On a dial/connect error before the request is written: mark the node (x
   already does), and if `budget > 0`, decrement and `continue` with `cc = nil`
   and a fresh request from `replay`. The failed dial never recorded a row.
3. `httpRoundTrip` reads the response head. Immediately after `http.ReadResponse`
   (and after forwarding interim `100 Continue` responses) but **before** any
   fail-code handling, body rewrite, header rewrite, caching or write to the
   client, test the status against `retryCodes`.
4. If it matches and `budget > 0`:
   - mark the node failed (the normal `failCodes` marker path; a retryable
     status must always remove the node from the next selection, even when
     `retryCodes` is narrower than `failCodes`), without double-marking when
     the status is also a fail code;
   - close the upstream body/connection, discard the response;
   - `retries++`, `budget--`, set the recorded retry count, restore `req` from
     `replay`, `cc = nil`, close any cache tee for this attempt, and continue;
   - do not record an attempt row.
5. Otherwise, the existing response pipeline runs unchanged and records exactly
   one row (the final attempt), with `ro.Retries = retries` and `ro.Time =
   reqStart`.

Selector semantics: each retry is a normal `hop.Select`, so
`round`/`fifo`/`random`/`sticky` apply and a node marked by the failed attempt
is skipped while its `failTimeout` marker lives. With `maxFails > 1` the node is
not yet skipped and the strategy may select it again — that is the existing
selector contract, documented rather than special-cased. When every candidate
is dead, x's last-resort selection can return the same node; retries are still
bounded by the budget.

Each attempt carries a zero-based attempt number in the request context
(`HandleHTTP` sets it before every round trip). `rewriteMeta` exposes it as
`attempt` (`forwarder.MetaKeyAttempt`) so cuttle's capture can keep only the
final attempt; x's own behavior does not depend on it.

Reasoning about which failures are retried: a complete retryable *status* means
the upstream finished answering without producing the requested result, so a
retry cannot duplicate a started generation. A read error after the request was
written is **not** retried (the upstream may have started work). Dial/connect
errors are retried because nothing was sent.

### Recorder and stats (`cuttle/internal/stats`)

- `x/recorder.HandlerRecorderObject` gains `Retries int` (`json:"retries,omitempty"`).
  x sets it on the final attempt only.
- `stats.Request` gains `Retries int` (`json:"retries"`), mapped from the
  recorder object.
- SQLite: additive column
  `retries INTEGER NOT NULL DEFAULT 0` in `ensureColumns("requests", …)`, added
  to the insert and to both select/scan paths. Old rows read `0`, no backfill.
  Rollups, graphs and filters are unchanged; a request is counted once, as
  before.
- Semantics: `retries` is the number of extra upstream attempts performed.
  `0` means the request was served (or failed) on the first attempt.
- Captures keep only the final attempt. x tags every rewriter call with a
  zero-based `attempt` value in the existing rewriter metadata
  (`forwarder.MetaKeyAttempt = "attempt"`, added to `rewriteMeta` from a
  per-attempt context value). Cuttle's rewriter passes it to
  `capture.Manager.Offer`; when the request direction arrives for a different
  attempt than the session's current one, the manager resets that session's
  previews, SSE fields, truncated flags, both hashes, `sseSeen`/hash-used flags
  and byte accounting before buffering the new attempt. `Reason`, `CreatedAt`
  and `OnError` survive. Discarded attempts never offer a response body because
  x stops before response rewriting, so the stored capture always describes the
  row's node/model. Chunked request bodies inside one attempt (same attempt
  value) still append, preserving the existing multi-chunk behavior.
- Sessions (`web/src/pages/Requests.tsx`) shows a `重试 N 次` tag when
  `retries > 0`, and the detail drawer (`web/src/components/RequestDetail.tsx`)
  gets a `retries` row. `web/src/api/types.ts` mirrors the field; add the
  Chinese source string and the `en-US` translation.
- Interim attempts are visible in logs (`Warnf` with node, status, attempt
  number); they are not stored, by design.

### Cross-repository release

The tag is cut from x `HEAD`, not from `v0.30.0`. HEAD is 37 commits ahead of
`v0.30.0` (dashboard phases 0–3 plus `b9d895a fix(forward): accumulate HTTP
failCodes failures across requests`). That fix changed the marker semantics
this design relies on: a successful connect no longer resets the marker, only a
non-fail-code response does, so `maxFails` counts across requests and a failed
attempt stays skipped for `failTimeout`. The retry code is written against HEAD,
so the tag must contain it.

1. Implement and test in `../x`; run x's Go gate (`go build ./...`, `go vet
   ./...`, `go test ./... -count=1`).
2. Commit, tag `v0.31.0` on HEAD, and push the tag. The user approved this push
   on 2026-11-10.
3. In cuttle, bump `github.com/go-gost/x` to `v0.31.0`, run `go mod tidy`.
4. Run cuttle's full Go gate with `GOWORK=off` and the web gate; rebuild the
   embedded console before the final Go build.
5. Regression-check the failCodes marker behavior in cuttle after the bump
   (route health, a dead node stays dead for `failTimeout`, a good response
   brings it back), because the tag also delivers `b9d895a` and the dashboard
   series to a module that was pinned at `v0.30.0`.
6. Record the x commit and tag in the cuttle plan's per-task implementation
   record.

Local iteration may use the parent `go.work` to compile cuttle against the
unreleased x, but the final verification must be `GOWORK=off` against the
published tag, because that is what the Docker build and release use.

## Edge cases and failure behavior

- All attempts exhausted: the last response (status and body) is relayed to the
  client; the row shows the last node and `retries = budget spent`.
- Client disconnects during a retry wait: the next attempt fails fast on the
  closed client connection; no row corruption.
- The retried request is byte-identical to the original at the wire level
  except for per-node rewrites (which are the point of re-selection).
- `429` with `Retry-After`: ignored in v1; the retry immediately targets the
  next node. If every node is rate limited, each attempt returns 429 and the
  final one is relayed.
- Caching: cuttle does not configure the HTTP cache, but a stale cached
  response is never preferred over a retry attempt; the cache lookup is skipped
  for retried attempts.
- Body capture records the final attempt only (see the stats section); an
  operator investigating a retried failure sees the final outcome plus log
  lines for the discarded attempts. Attempt-level forensics would be a
  deliberate schema change, not a side effect.
- No total per-request deadline is introduced. Each attempt keeps the existing
  `gateway.readTimeout` bound, so `retries: 10` can hold a client connection for
  roughly eleven read timeouts; SSE generation already has no total bound. The
  README and AGENTS.md state this worst case instead of adding a knob that would
  also cut long, healthy streams.
- Memory: at most one buffered request body (≤ 32 MiB) and one discarded
  response body per in-flight retried request.

## Testing

x (`../x`):

- Forwarder test with two `httptest` upstreams: first returns 429 then 200,
  assert two upstream hits, client sees 200, the node has one failed marker, the
  recorded object carries `Retries: 1`, and the request body was replayed
  identically.
- Status outside `retryCodes` (e.g. 400) does not retry.
- `retries: 0` / absent metadata does not buffer or retry.
- Dial failure with budget retries the next node; a written request whose
  response read fails does not retry.
- Oversized body (over the cap) is forwarded once, with the body intact.
- 200 + streaming body is not retried and streams through unchanged.
- `Expect: 100-continue` request is forwarded once.

cuttle:

- Config: defaults, validation errors naming the route, strict unknown fields,
  `retryCodes` parsing, `example.yaml` loads.
- Build: every node of a route (including per-key nodes and backup nodes)
  carries `retry.max`/`retry.codes`; routes with `retries: 0` carry neither.
- Stats: recorder maps `Retries`, store round-trip of the new column, old DB
  migration, rollups stay one row per request.
- Capture: attempt 1 for the same SID replaces attempt 0's previews/hashes/
  truncation/byte accounting; two offers with the same attempt still append;
  the stored hash matches the final rewritten body.
- Admin: create/update/list routes keep both fields; invalid values rejected.
- Web: types, route form, Sessions tag and detail row; Vitest + `tsc --noEmit`.
- End-to-end gateway test with two local upstreams: route with `retries: 1`,
  first upstream 502, second 200, one stats row with `retries: 1`.
- Post-bump regression for `b9d895a`: a failed node stays marked for
  `failTimeout`, a good response clears it, and `maxFails > 1` accumulates
  across requests.
- README route table and behavior section; AGENTS.md invariants updated with the
  retry contract (route fields, x metadata keys, one-row rule, retries column).

## Decisions taken here (check these while reading)

- One row per client request with a `retries` count, not one row per attempt.
- Default `retryCodes` is `429,5xx`, reusing the existing fail-code syntax.
- `retries` defaults to `0`, so existing configs behave exactly as today.
- The retry budget and codes travel through node metadata, avoiding a
  `core` release; only x needs a new tag.
- The x tag is `v0.31.0` cut from HEAD, including dashboard phases 0–3 and
  `b9d895a`'s marker semantics; the tag push is approved.
- Captures are reset to the final attempt when a retry happens; they never
  concatenate two attempts' bodies and hashes.
- No total request deadline; the worst case is documented instead.
- No backoff, no `Retry-After`; retries are immediate re-selections.
- Dial failures are retried; post-write read failures are not.
- 32 MiB request-body cap; oversize or `Expect: 100-continue` requests are
  forwarded without retry.
- Node failure marking on retry is always applied, even when `retryCodes` is
  narrower than `failCodes`.

## Left to the builder

- The exact refactor of `httpRoundTrip` (return signal vs. sentinel error) and
  how the keep-alive loop tracks per-request retry state across the cache
  lookup, dial and round-trip calls.
- Where to put the request buffering helper and the status-code parser in x
  (forwarder package, reusing `chain.FailCodes.Match` semantics).
- The x test harness shape for a two-node hop and the exact assertions on the
  marker and recorder.
- Console wording/placement of the retries tag and route fields.
- Plan task ordering across the two repositories, including the tag/bump step.
