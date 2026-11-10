# AGENTS.md

Guidance for coding agents working in the `cuttle` repository.

## Repository

- `cuttle` is a self-hosted LLM gateway written in Go with an embedded React
  admin console (Vite, Ant Design, React Flow).
- `go.mod` uses published module tags (`core`, `x`,
  `github.com/ginuerzh/llm-api-converter`) and has no local `replace`
  directives. Do not reintroduce a replace to make a local sibling change
  compile; publish a tag instead.
- Inside `/root/code/go-gost`, the parent workspace contains a `go.work`. Run Go
  commands in this repository with `GOWORK=off` so the tagged module versions
  are used instead of the local workspace directories.
- `README.md` is the user guide; `DOCKER.md` covers image builds. Keep both in
  sync with behavior changes.
- Do not add a `.memory/` directory here: this repository has an upstream
  remote. Project memory lives in `/root/code/go-gost/.memory/`.

## Commands

```bash
# Build the console, then the binary
(cd web && npm ci && npm run build)
GOWORK=off go build -o cuttle .

# Zero-config first run (no cuttle.yaml)
CUTTLE_DATA=./data ./cuttle serve

# Full Go gate
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1

# Web gate
cd web
npm test
npx tsc --noEmit
npm run build

# Docker (from this directory; modules are fetched by tag)
docker build -t cuttle .
```

`web/` builds into `internal/admin/dist`, which is git-ignored and embedded with
`go:embed`. Rebuild the web bundle before a Go build when the console changed.

## Architecture

| Path | Responsibility |
| --- | --- |
| `main.go` | CLI: `serve`, `hash-password`, `SIGHUP` reload; first-run bootstrap wiring and session reset. The admin server starts only when `admin.passwordHash` is set. |
| `internal/bootstrap` | Zero-config path resolution, first-run config generation, admin password generation, and ignored-env warnings. |
| `internal/config` | Strict YAML schema, environment expansion, defaults, validation, atomic `0600` writes. |
| `internal/gateway/auth.go` | Data-plane gateway-key authentication, request-head replay and fixed 401 responses. |
| `internal/gateway/build.go` | Compiles one hop entry per entrypoint and inbound protocol variant, with route plans cached by route name. |
| `internal/gateway/protocol.go` | Inbound protocol path groups and path/model matcher construction. |
| `internal/gateway/upstream.go` | Resolves default and per-protocol upstream targets per node and rewrites inbound paths/models. |
| `internal/provider` | Provider adapters, model catalogs, API classification, auth. |
| `internal/rewriter` | Body protocol conversion through `llm-api-converter`. |
| `internal/admin` | Management API, sessions, configuration mutation, embedded console. |
| `internal/stats` | SQLite request store, recorder, rollups and graph aggregation. |
| `web/` | Admin console source. |

The data plane uses the x hop group and selector. Do not replace that machinery
with a custom dispatcher unless the task explicitly requires it.

## Routing invariants

The only routing chain is:

```text
Entrypoint -> Route -> Node -> Provider model
```

- Entrypoint declaration order is the only precedence rule. Never make route
  order semantic.
- An entrypoint has a name, a non-empty list of client-model globs and exactly
  one route. `*` is an explicit catch-all.
- Only the request body's `model` field is matched for routing. Paths never
  affect entrypoint precedence or route choice; they only select the upstream
  wire API inside a matched entrypoint. There is no host matching and no
  client-model pass-through.
- A route contains only nodes and a strategy (`fifo`, `round`, `random`,
  `sticky`), plus its retry budget (`retries`/`retryCodes`). It does not match
  anything itself.
- Route retry policy is stamped on every node of the route as the metadata keys
  `retry.max` (int) and `retry.codes` (string); absent or `retry.max <= 0`
  disables retries. Cuttle only carries the policy: x owns the retry loop, the
  replay buffer and the retry status matching.
- A node binds exactly one provider and exactly one provider model.
  `Node.Models`, `EffectiveModel`, logical models and `*` model fallbacks must
  not come back.
- `node.api` is `auto` by default. Auto matches the inbound protocol group
  (`chat`, `responses`, `anthropic`, `gemini`) to a configured provider API and
  falls back to the model's default API. A pinned `api` bypasses default
  resolution and requires the matching base URL.
- `node.key` is optional. When it names a provider key, the node builds only
  that key; when it is empty the node builds every enabled provider key and the
  route selector spreads across them. The name must exist on the node's
  provider.
- Disabled providers, disabled nodes and provider-disabled models are skipped at
  compile time. If every node is skipped, the request fails; never fall through
  to another route or send the client model upstream.
- Do not edit the sibling repositories (`../core`, `../x`,
  `../llm-api-converter`) as part of cuttle work. Publish a tag in the sibling
  repository, then bump the tagged dependency here; record that repository's
  commit separately.

## Gateway invariants

- The data-plane chain is public hello handler -> gateway auth -> count_tokens
  -> x forward. `GET|HEAD /api/hello` is exempt; every other path, including
  Anthropic `count_tokens`, requires a key when any key is configured.
- Open mode (no `gateway.keys`) must be a pass-through that does not parse the
  request.
- Accepted credentials are `Authorization: Bearer <value>`, `x-api-key`,
  `x-goog-api-key` and `api-key`. Any candidate may authenticate; a junk value
  in one header must not veto a valid value in another. Query-string keys are
  not supported.
- Attribution goes to the first enabled match in configuration order. Compare
  every candidate against every entry digest in constant time, including
  disabled entries; a disabled match only changes the failure reason
  (`missing`, `disabled` or `invalid`).
- A rejected request gets the fixed JSON 401 with
  `WWW-Authenticate: Bearer realm="cuttle"` and `Connection: close`. Never call
  the upstream and never log the credential; log only client IP, method, path
  and the failure reason. A malformed request head returns 400.
- The auth handler reads only the request head (1 MiB / 15s bound) and replays
  the exact head plus the framed body. It must yield exactly one request and
  then EOF: x's keep-alive loop would otherwise serve a second request on the
  same connection without re-entering the auth handler.
- `Gateway` keeps the compiled policy in an `atomic.Pointer`. SIGHUP and admin
  reloads swap it after the new hop group builds; a failed build keeps the old
  group and policy. Warn at apply time when every configured key is disabled.
- The authenticated key name travels only in the request context for stats. It
  is never added as an upstream header, and inbound credentials are stripped
  as before.
- Route retries are x's loop around `hop.Select`: every retry re-runs selection
  (so the strategy, backup flag and node marker pick the next node), marks the
  failed node, and is decided before any response byte reaches the client.
  Cuttle records one request row per client request and the final attempt's
  response. Never add a cuttle-level retry dispatcher or one row per attempt.

## Configuration invariants

- `config.Load` uses `yaml.KnownFields(true)`. Unknown fields must keep failing
  loudly; old `aliases`, route `match` and node `models` configs are rejected by
  design with no migration.
- Keep the `Load` order: decode, `expandEnv`, `applyDefaults`, `Validate`.
- Provider keys are named: `keys` is a list of `{name, key, enabled}`. Names
  are required, trimmed, at most 64 runes, unique per provider, and must not
  contain `/`, `$` or control characters; values must be non-empty and contain
  no `$` or control characters. `enabled` defaults to true.
- A node's `key` must name a configured key of its own provider; a dangling
  name is a validation error. A pinned-but-disabled key is valid and the node is
  skipped at build time.
- Validation errors must name the offending provider, node, route or entrypoint.
- Defaults: gateway `:8787`, admin `127.0.0.1:8788`, session `12h`, remember
  `720h`, stats `stats.sqlite`, log `info`/`json`, capture `off`/`1048576`/`24h`,
  provider health `30s`/`5s`, node weight `1`, node API `auto` (stored as an
  empty string), route strategy `round`.
- `CUTTLE_LISTEN` (`:8787`), `CUTTLE_ADMIN_LISTEN` (`127.0.0.1:8788`; the
  Docker image sets `:8788`) and `CUTTLE_ADMIN_PASSWORD` (unset) are first-run
  defaults: they are used only when a new config is generated. `CUTTLE_DATA`
  (default `/data`) is the data directory used on every start and determines
  where an existing config is looked up or a missing one is generated.
- Config resolution: explicit `-c` must exist; otherwise
  `$CUTTLE_DATA/cuttle.yaml`, `/etc/cuttle/cuttle.yaml`, `./cuttle.yaml`, then
  generate at `$CUTTLE_DATA/cuttle.yaml`.
- A generated config writes an absolute `stats.path` under `CUTTLE_DATA`, the
  documented `168h`/`2160h` retention defaults, a bcrypt hash of the env
  password or a 32-character generated password, and empty topology lists. The
  generated password is printed once to stderr after the config write and before
  stats is opened. A trailing `\r\n` is trimmed from the env password.
- First-run generation uses `config.WriteNew` (`os.Link`, no overwrite). Losing
  a concurrent first-run race loads the winner's config instead of replacing it.
- A generated config writes `$CUTTLE_DATA/.cuttle-sessions-reset` before the
  config. `main` clears persisted sessions and removes the marker only after
  success; a failed or interrupted startup retries on the next start.
- An existing config with an empty `admin.passwordHash` stays admin-disabled.
  Env values that differ from a loaded config are ignored with a warning;
  matching values are silent.
- `admin.passwordHash` must not be added to `expandEnv`; bcrypt hashes contain
  `$`.
- `log.level` is live through SIGHUP and admin saves; `log.format` is fixed at
  startup and changing it at runtime only logs a restart warning.
- `log.level: trace` enables x header dumps that can contain client
  `Authorization`/`x-api-key` values; do not leave trace enabled in production.
- `config.Write` must stay atomic (temp file, `fsync`, rename, `0600`).
- `example.yaml` must always load with the current schema.
- `gateway.keys` empty (absent or `[]`) leaves the data plane open. Any entry
  turns authentication on, including a list where every entry is disabled.
- Gateway key names are required, trimmed, at most 64 characters, unique, and
  must not contain `/`, `$` or control characters. Values are required,
  trimmed, 8–512 characters, unique, must not contain `$` or control
  characters, and are rejected without echoing the value.
- `enabled` defaults to true. A disabled entry stays configured but never
  authenticates. An all-disabled list logs a warning at apply time and rejects
  every data-plane request.
- `config.Load` environment-expands gateway key values. Admin writes apply
  defaults and validation only and must not expand a literal `$`; a literal
  `$` is rejected so the next reload cannot reinterpret or fail on it.

## Admin API invariants

- Public routes: `GET /api/health`, `POST /api/login`, `POST /api/logout`.
  Everything else under `/api/` requires a session cookie.
- `GET /api/events` is a WebSocket subscription under `requireAuth`. Validate
  `Origin` against `Host` (empty Origin is allowed for non-browser clients),
  accept `range`, `since` or `reset=1`, and push absolute snapshots. Close
  sockets with 4401 on logout or session expiry, 4408 when an open session
  exceeds `wsMaxAge`, and close them on shutdown.
- List endpoints return `[]` for empty results, never `null`.
- Create endpoints return `201 Created` with an empty body. The web client must
  tolerate empty success responses.
- Errors are JSON objects with an `error` string.
- Mutations go through `mutate`/`replaceLocked`: clone, apply, default, validate,
  reload the data plane, then write the config. Do not apply invalid config.
- `GET /api/entrypoints` returns the ordered list; `PUT /api/entrypoints`
  replaces the whole list. Order is precedence.
- `GET /api/topology/positions` returns the shared layout map; `PUT` replaces it
  in one transaction. Reject empty IDs and maps above 10,000 entries; return
  `503` when the stats store is unavailable.
- `GET/POST /api/captures` and `DELETE /api/captures/{id}` manage bounded
  on-demand rules. Rules are in-memory, lost on restart, and capped at 1000
  uses / 1 hour.
- `GET /api/provider-usage` joins local rollups with per-key upstream
  balances/windows. It caches upstream results for 60 seconds (failures too);
  `refresh=1` bypasses the cache. Keys stay ordered, only masked previews are
  returned, disabled/no-key providers make no upstream call, and `generic` /
  `opencode-zen` return a note instead of querying.
- Deleting a route still referenced by an entrypoint must fail and list the
  referencing entrypoints. Deleting a node still referenced by a route must
  fail.
- Provider `type` and non-`generic` API base URLs are immutable through the
  admin API. Delete and recreate, or edit the file offline.
- Provider keys are write-only: `GET` returns `[{name, keyPreview, enabled}]` and never a value. `keyUpdates` is name-based: `{name,key}` appends, `{name,delete:true}` deletes (a missing name is rejected, and deleting a key pinned by a node of the same provider is rejected), `{name,enabled}` sets the flag; a name may be deleted and re-added in one request. `keys` cannot be combined with `keyUpdates`. A full `keys` replacement preserves stored values when the field is absent, clears on an explicit `[]`, and merges by name (an entry with an empty `key` whose name exists keeps the stored value).
- Usage accounts carry the key `name` and `enabled` beside the masked preview.
- Provider model overrides live in `provider.models.<id>.disabled`. They stay
  local, are merged into catalog responses, and must not invalidate the catalog
  cache.
- Gateway keys are write-only through `GET /api/gateway-keys`: only
  `keyPreview` is returned. The raw secret appears only in the `POST
  /api/gateway-keys` 201 body and the `POST /api/gateway-keys/{name}/rotate`
  200 body; `PUT` changes only `enabled`, and `DELETE` returns 204 and keeps
  usage history.
- `GET /api/config` exposes `gateway.readTimeout`, `gateway.userAgent`,
  `gateway.keysSet` and `gateway.authRequired`, never key values.
  `PUT /api/config` is a full replacement for the exposed fields. Write-only
  provider keys (per provider name), `gateway.keys` and the admin password are
  preserved when absent; `gateway.keys` clears only on an explicit `[]`; a
  non-empty list replaces the whole set and is validated without echoing
  values. Fields the view does not expose (for example provider `userAgent` and
  `headers`) are not round-trippable through GET then PUT.
- `GET /api/gateway-key-usage` merges `key_rollups` with the current list:
  configured keys stay in order including zero usage, deleted keys are appended
  alphabetically with `configured:false`, and `keys` is never `null`. Return
  503 when stats are disabled.
- Gateway-key create is the deliberate exception to the empty-201 rule: the
  one-time secret must be in the response body.

## Stats invariants

- x node names stay `<route>/<provider>/<node>[/<key>]`;
  `stats.parseNodeName` depends on that shape.
- Record the route, node, provider, client model, target model, status, duration
  and token counts for every request.
- Entrypoint attribution is query-time: resolve each row's `client_model`
  through the current entrypoint list. Never store an entrypoint column.
- Graph keys use `entrypoint:<name>`; edges use the row's **recorded route**
  (`entrypoint:<name>->route:<route>` or `->unrouted`), never the entrypoint's
  current route.
- `TopologyIncludes` keeps unrouted rows but drops rows whose route is gone, is
  not referencing the node, or whose provider does not match. Orphan nodes must
  show zero traffic.
- Old SQLite databases keep an inert `logical_model` column. Do not read or
  write it, and do not require a destructive migration.
- The graph emits `entrypoint:<name>`, `route:<name>`, `node:<name>` and
  `provider:<name>` keys; rows without a route use the literal `unrouted` key.
  Keep the web graph and stats tests aligned.
- Node-forwarded responses carry `X-Cuttle-Request-ID` equal to the stats
  `requestId` (the x `sid`). Gateway-generated 502/503 responses and request-side
  failures without a response are a documented v1 gap.
- Captures live in `request_captures` beside `requests`. `off` creates nothing;
  `errors` buffers all and persists failures; `sample` persists a subset; rules
  override the policy. `maxBytes` bounds each preview field and `capture.retain`
  bounds rows. Never capture headers.
- Rollups store `cached_tokens`; `Store.ProviderUsage` aggregates them by
  provider for the Usage page. The column is additive; old rollup rows read 0
  cached tokens and are not backfilled.
- `requests.gateway_key` and the `idx_requests_gateway_key_ts` index are added
  by `ensureColumns`; old rows read as empty. `Filter.GatewayKey` maps to
  `GET /api/requests?key=<name>` (exact name).
- `requests.retries` is additive (`INTEGER NOT NULL DEFAULT 0`) and counts extra
  upstream attempts only: `0` means the first attempt served the request. One
  client request is one row and one set of rollups; never insert a row per
  attempt. Body capture keeps the final attempt only, selected through the
  rewriter's `attempt` metadata.
- Hourly `key_rollups` (bucket + gateway_key) store per-key requests, errors and
  token counters. Insert them only for authenticated requests, in the same
  transaction as `rollups`. `Cleanup` deletes them with the same
  `retainRollups` cutoff.
- No backfill: pre-upgrade rows and rollups have no key. Rejected 401 requests
  are not recorded; locally answered `count_tokens` requests produce no usage
  row either.
- `Store.KeyUsage` aggregates `key_rollups`; the admin layer appends deleted key
  names with `configured:false` so history survives deletion.
- The live monitor tails `requests` by `seq`, a monotonic counter stored in
  `request_seq`, never by `ts` or `rowid`; a long request can complete after
  its start time, and retention can reuse rowids. `GraphAggregate` seeds the
  window and `ForEachRequestAfter` reads new rows. Deduplicate by request ID
  so a replaced row cannot double count.
- A live snapshot is cumulative from a fixed `since`. Counts and tokens are
  monotonic between resets; P50/P95 are percentiles over the same window and
  may move either way. Error samples are bounded to 200, and cached
  percentile keys are not re-materialized on every tick.

## Web invariants

- Types in `web/src/api/types.ts` mirror the Go JSON shapes. Use `entrypoints`,
  not `aliases`; a node has `model: string`, not `models`.
- `Provider.keys` is a masked `[{name, keyPreview, enabled}]` view; create and
  update payloads use the separate `ProviderKeyInput`. The provider form edits
  named key rows and the node form has a pinned-key select; the create modal
  takes one name and value per key.
- Admin UI source strings are Chinese and translated through
  `web/src/locales/en-US.ts`. Add both sides for new text.
- List pages use the shared `ListPage`/`ListCard` single-column layout. The
  navigation is a Drawer at every width.
- Entrypoints reorder with up/down controls and save the whole list; routes no
  longer have order controls.
- The topology has monitor and edit modes. Monitor mode must not allow node
  dragging. Selection must not change node size, and the detail panel stays
  hidden until a node or edge is selected. Positions are persisted server-side
  in the `topology_positions` table (shared by all admins) and mirrored to
  `localStorage` as a backup/fallback. Time presets are
  `1m, 5m, 15m, 30m, 1h, 6h, 12h, 24h`.
- The topology monitor uses the `/api/events` WebSocket, not polling. The time
  preset sets the initial lookback (`since = now - range`); Reset clears the
  display and reconnects with `reset=1`; the status shows the last update and
  the session start.
- Requests show client model and target model; there is no logical model.
- The navigation labels the `gateway-keys` page `密钥` between Entrypoints and
  Sessions. The page heading, dialogs and Sessions filter keep `网关密钥`. It
  shows the open-data-plane and all-keys-disabled alerts, 24h/7d/30d usage,
  enable switches, rotate and delete.
- Overview animations share one toolbar switch persisted in `localStorage`
  (`cuttle.animation`). Off stops node icon flashes and edge light dots without
  pausing metrics. The node flash is one-shot; do not reintroduce the card ring.
- Create and rotate open the one-time secret dialog; the raw key is never kept
  in list state or rendered anywhere else. The dialog must say the value is
  shown only once.
- Sessions (the Requests page) has a `网关密钥` exact-name filter, labels rows
  with `key:<name>` and shows `gatewayKey` in the detail.

## Testing

- Add or update tests with every behavior change. Do not weaken assertions to
  make a change pass.
- Config fixtures must use the current schema. Gateway tests use local
  `httptest` upstreams and need no real API keys.
- Useful focused commands:

```bash
GOWORK=off go test ./internal/config -count=1
GOWORK=off go test ./internal/gateway -run TestEntrypoint -count=1
GOWORK=off go test ./internal/admin -run TestEntrypoint -count=1
GOWORK=off go test ./internal/stats -count=1
```

- Run the full Go gate and the web gate before claiming completion. If the web
  bundle changed, rebuild it before the final Go build.
- There are no browser visual regression tests; state clearly when UI behavior
  was verified only by TypeScript, Vitest and HTTP checks.

## Commits and plans

- Commit after each completed plan task, including the task's Implementation
  record in the same commit. Keep records in the plan file; do not rely on chat.
- Never push unless the user explicitly asks.
- Use conventional commit messages, for example `feat(web): ...`,
  `fix(stats): ...`, `refactor(backend)!: ...`, `docs: ...`.
- Do not mix unrelated workspace changes into this repository's commits.
  Workspace memory changes belong in `/root/code/go-gost` as a separate commit.
- When work spans sibling repositories, record and commit each repository's
  contribution separately.
- Code, comments, docs and commit messages are English. UI strings are the
  exception described above.

## Subagents

- Never pass a `model` when dispatching a subagent: it must inherit the current
  model.
- Never pass `sessionID` to resume an old subagent session: start a new session
  and restate the context and report paths.

## Gotchas

- `internal/admin/dist` is ignored but required by `go:embed all:dist`. A plain
  `go build` fails on a fresh checkout until the web bundle is built.
- WebSocket `/api/events` needs `ws: true` in the Vite proxy and Upgrade
  forwarding plus `Host` preservation in a reverse proxy, or the Origin check
  rejects the handshake.
- Route variants multiply hop entries and rewriters by inbound protocol. Build
  one route plan and register rewriters from that plan; never recompute the
  target API separately, or path, body protocol and stats can diverge.
- Route retries require `github.com/go-gost/x` `v0.31.0` or newer: the retry
  loop, the 32 MiB replay buffer and the `retry.max`/`retry.codes` node
  metadata all live in x. An older tag ignores that metadata silently. The Go
  gate still runs with `GOWORK=off` inside the parent workspace, so the tagged
  module is what gets built and tested.
- Admin config writes require a writable config file. With a Docker `:ro` mount,
  console edits fail; edit the host file and send `SIGHUP`.
- First-run config and stats live in `$CUTTLE_DATA` (default `/data`). The
  Docker image uses `CMD ["serve"]`; its `CUTTLE_ADMIN_LISTEN=:8788` is a
  first-run default, not an override for an existing config.
- The generated admin password is printed only on the first run. If it is lost,
  replace `admin.passwordHash` with `hash-password` output or delete the config
  (destructive; persisted sessions are cleared on regeneration).
- `replaceLocked` reloads the data plane before writing the file. A write
  failure can leave memory ahead of disk; do not treat the two steps as one
  transaction without reviewing that path.
- Provider catalogs are cached in the admin layer (10 minutes, last-good,
  singleflight). Model-only changes must not invalidate the cache.
- OpenCode catalogs are keyless. Sending an invalid `Authorization` header makes
  them return 401; `x-api-key` is ignored.
- Docker builds use the `cuttle` directory as context. Dependencies are
  downloaded from the module proxy at the tags in `go.mod`; the workflow no
  longer checks out sibling repositories.
- The demo under `/tmp/cuttle-demo.yaml` is local-only and may contain real
  keys. Never copy it into the repository.
- OpenCode Zen has no per-key balance API: its Usage card must link to
  `https://opencode.ai/console` instead of querying. OpenCode Go's windows are
  at `/zen/go/v1/usage` (legacy bases) or `/inference/go/v1/usage` (inference
  bases); CommandCode uses `resetAt` (Unix time) while OpenCode Go uses
  `resetsAt` (RFC 3339).
