# AGENTS.md

Guidance for coding agents working in the `llm-gw` repository.

## Repository

- `llm-gw` is a self-hosted LLM gateway written in Go with an embedded React
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
GOWORK=off go build -o llm-gw .

# Zero-config first run (no llm-gw.yaml)
LLM_GW_DATA=./data ./llm-gw serve

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
docker build -t llm-gw .
```

`web/` builds into `internal/admin/dist`, which is git-ignored and embedded with
`go:embed`. Rebuild the web bundle before a Go build when the console changed.

## Architecture

| Path | Responsibility |
| --- | --- |
| `main.go` | CLI: `serve`, `hash-password`, `SIGHUP` reload; first-run bootstrap wiring and session reset. The admin server starts only when `admin.passwordHash` is set. |
| `internal/bootstrap` | Zero-config path resolution, first-run config generation, admin password generation, and ignored-env warnings. |
| `internal/config` | Strict YAML schema, environment expansion, defaults, validation, atomic `0600` writes. |
| `internal/gateway/build.go` | Compiles one hop entry per entrypoint, with route hops cached by route name. |
| `internal/gateway/upstream.go` | Resolves one upstream target per node and rewrites inbound paths/models. |
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
- Only the request body's `model` field is matched. There is no path or host
  matching and no client-model pass-through.
- A route contains only nodes and a strategy (`fifo`, `round`, `random`). It
  does not match anything itself.
- A node binds exactly one provider and exactly one provider model.
  `Node.Models`, `EffectiveModel`, logical models and `*` model fallbacks must
  not come back.
- Disabled providers, disabled nodes and provider-disabled models are skipped at
  compile time. If every node is skipped, the request fails; never fall through
  to another route or send the client model upstream.
- Do not edit the sibling repositories (`../core`, `../x`,
  `../llm-api-converter`) as part of llm-gw work. Publish a tag in the sibling
  repository, then bump the tagged dependency here; record that repository's
  commit separately.

## Configuration invariants

- `config.Load` uses `yaml.KnownFields(true)`. Unknown fields must keep failing
  loudly; old `aliases`, route `match` and node `models` configs are rejected by
  design with no migration.
- Keep the `Load` order: decode, `expandEnv`, `applyDefaults`, `Validate`.
- Validation errors must name the offending provider, node, route or entrypoint.
- Defaults: gateway `:8787`, admin `127.0.0.1:8788`, session `12h`, remember
  `720h`, stats `stats.sqlite`, log `info`/`json`, capture `off`/`32768`/`24h`,
  provider health `30s`/`5s`, node weight `1`, route strategy `round`.
- `LLM_GW_LISTEN` (`:8787`), `LLM_GW_ADMIN_LISTEN` (`127.0.0.1:8788`; the
  Docker image sets `:8788`) and `LLM_GW_ADMIN_PASSWORD` (unset) are first-run
  defaults: they are used only when a new config is generated. `LLM_GW_DATA`
  (default `/data`) is the data directory used on every start and determines
  where an existing config is looked up or a missing one is generated.
- Config resolution: explicit `-c` must exist; otherwise
  `$LLM_GW_DATA/llm-gw.yaml`, `/etc/llm-gw/llm-gw.yaml`, `./llm-gw.yaml`, then
  generate at `$LLM_GW_DATA/llm-gw.yaml`.
- A generated config writes an absolute `stats.path` under `LLM_GW_DATA`, the
  documented `168h`/`2160h` retention defaults, a bcrypt hash of the env
  password or a 32-character generated password, and empty topology lists. The
  generated password is printed once to stderr after the config write and before
  stats is opened. A trailing `\r\n` is trimmed from the env password.
- First-run generation uses `config.WriteNew` (`os.Link`, no overwrite). Losing
  a concurrent first-run race loads the winner's config instead of replacing it.
- A generated config writes `$LLM_GW_DATA/.llm-gw-sessions-reset` before the
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

## Admin API invariants

- Public routes: `GET /api/health`, `POST /api/login`, `POST /api/logout`.
  Everything else under `/api/` requires a session cookie.
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
- Provider keys are write-only: `GET` exposes only `keysSet` and masked
  `keyPreviews`. `keyUpdates` supports only `{index, delete: true}` and
  `{key}` append; replacing an existing index is rejected, and `keys` cannot be
  combined with `keyUpdates`.
- Provider model overrides live in `provider.models.<id>.disabled`. They stay
  local, are merged into catalog responses, and must not invalidate the catalog
  cache.

## Stats invariants

- x node names stay `<route>/<provider>/<node>[/<keyIndex>]`;
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
- Node-forwarded responses carry `X-LLM-GW-Request-ID` equal to the stats
  `requestId` (the x `sid`). Gateway-generated 502/503 responses and request-side
  failures without a response are a documented v1 gap.
- Captures live in `request_captures` beside `requests`. `off` creates nothing;
  `errors` buffers all and persists failures; `sample` persists a subset; rules
  override the policy. `maxBytes` bounds each preview field and `capture.retain`
  bounds rows. Never capture headers.
- Rollups store `cached_tokens`; `Store.ProviderUsage` aggregates them by
  provider for the Usage page. The column is additive; old rollup rows read 0
  cached tokens and are not backfilled.

## Web invariants

- Types in `web/src/api/types.ts` mirror the Go JSON shapes. Use `entrypoints`,
  not `aliases`; a node has `model: string`, not `models`.
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
- Requests show client model and target model; there is no logical model.

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
- Admin config writes require a writable config file. With a Docker `:ro` mount,
  console edits fail; edit the host file and send `SIGHUP`.
- First-run config and stats live in `$LLM_GW_DATA` (default `/data`). The
  Docker image uses `CMD ["serve"]`; its `LLM_GW_ADMIN_LISTEN=:8788` is a
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
- Docker builds use the `llm-gw` directory as context. Dependencies are
  downloaded from the module proxy at the tags in `go.mod`; the workflow no
  longer checks out sibling repositories.
- The demo under `/tmp/llm-gw-demo.yaml` is local-only and may contain real
  keys. Never copy it into the repository.
- OpenCode Zen has no per-key balance API: its Usage card must link to
  `https://opencode.ai/console` instead of querying. OpenCode Go's windows are
  at `/zen/go/v1/usage`; CommandCode uses `resetAt` (Unix time) while OpenCode
  Go uses `resetsAt` (RFC 3339).
