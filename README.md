# llm-gw

`llm-gw` is a self-hosted LLM gateway with an embedded admin console. It gives
clients one stable endpoint for several providers, routes each client model to a
provider model, converts between wire APIs, and records per-request statistics.

## What it does

- Accepts OpenAI Chat Completions, OpenAI Responses, Anthropic Messages and
  Gemini `generateContent` requests.
- Converts requests and responses between the inbound API and the provider's
  native API.
- Matches client model names with case-insensitive globs and an explicit
  precedence order.
- Balances or fails over across provider keys, nodes and backup nodes.
- Locks provider keys to a write-only admin flow and masks previews in the API.
- Serves an admin console for providers, nodes, routes, entrypoints, requests,
  a provider usage/balance view and a live topology view.
- Stores request history and rollups in SQLite; the topology can be filtered by
  time range down to one minute.
- Reloads configuration on `SIGHUP` or immediately after an admin edit.
- Starts without a config file: a zero-config first run creates the config and
  a one-time admin password under `$LLM_GW_DATA`, then the console creates the
  upstream topology.

## Routing model

Every request follows one chain:

```text
client model
    │  glob match, entrypoint order
    ▼
Entrypoint ──► Route ──► Node ──► Provider model
                 │          │
                 │          └─ one provider + one model
                 └─ ordered nodes + strategy (fifo / round / random)
```

- An **entrypoint** is the client-facing ingress. It has a name, a non-empty
  list of model globs, and exactly one route.
- Entrypoints are evaluated in declaration order. The first entrypoint with a
  matching glob wins; `*` is the explicit catch-all.
- Globs are case-insensitive. `*` is the only wildcard.
- A **route** contains only nodes and a selection strategy. It does not match
  anything by itself.
- A **node** binds one provider and one provider model. `backup` marks fallback
  nodes; `weight` controls the round-robin share.
- A node's target API is `auto` by default. The gateway matches the inbound wire
  API (`/v1/chat/completions`, `/v1/responses`, `/v1/messages`, or
  `/v1beta/models/...`) to a configured provider API and converts only when the
  provider has no matching base URL. Set `api` on the node to pin one API.
- Disabling a provider, a node, or a provider model removes that node from the
  compiled route. A route with no usable nodes fails the request instead of
  passing the client model through.
- If no entrypoint matches, the request is unrouted and returns an error.

> The routing model is intentionally strict. Old configurations using
> `aliases`, route `match`, or node `models` maps are rejected at load time.
> There is no automatic migration.

## Quick start

### Requirements

- Go 1.26.4 or newer (`go.mod`) to build from source.
- Node.js 22 and npm to rebuild the admin console.
- SQLite is embedded; no external database is required.

The module depends on the published versions of `core`, `x` and
`llm-api-converter`; no sibling checkouts are required. If a parent `go.work`
exists, `GOWORK=off` keeps the build on the tagged module versions instead of
the local workspace directories.

### Build

```bash
# from the llm-gw directory
(cd web && npm ci && npm run build)
GOWORK=off go build -o llm-gw .
```

The web build writes to `internal/admin/dist`, which is embedded into the Go
binary. The `GOWORK=off` flag keeps the build scoped to this module when a
parent `go.work` exists.

### Versioning

`llm-gw version` (also `--version` and `-v`) prints the build identity. Release
tags report `vX.Y.Z`, builds from `main` report `main-<short-sha>`, and a plain
local build reports `dev`. The same value appears at the bottom of the admin
navigation panel and in `GET /api/health` as `version`, `commit` and `builtAt`.

CI passes the tag or branch identity to the Docker build. A local build can set
it explicitly:

```bash
GOWORK=off go build \
  -ldflags "-X github.com/go-gost/llm-gw/internal/version.Version=dev \
    -X github.com/go-gost/llm-gw/internal/version.Commit=$(git rev-parse HEAD) \
    -X github.com/go-gost/llm-gw/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o llm-gw .
```

Releases are created by pushing an annotated `vX.Y.Z` tag; CI publishes the
semver image tags alongside `latest`.

### Zero-config first run

No config file is required. Start the binary (or the Docker image) and it
creates `$LLM_GW_DATA/llm-gw.yaml` and `$LLM_GW_DATA/stats.sqlite`:

```bash
LLM_GW_DATA=./data ./llm-gw serve
```

If `LLM_GW_ADMIN_PASSWORD` is not set, llm-gw generates a 32-character admin
password, prints it once to stderr, and stores only its bcrypt hash. Open the
admin console, log in, and create a provider, node, route and entrypoint. No
provider, key or model environment variables exist.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LLM_GW_DATA` | `/data` | Directory for the generated config and stats database |
| `LLM_GW_LISTEN` | `:8787` | First-run `gateway.listen` |
| `LLM_GW_ADMIN_LISTEN` | `127.0.0.1:8788` (binary); `:8788` (Docker image) | First-run `admin.listen` |
| `LLM_GW_ADMIN_PASSWORD` | unset | First-run admin password; hashed into the config |

All four are optional. The three config-value variables are **first-run
defaults**: once a config exists, the file and the console are authoritative and
the variables are ignored (a differing value logs a warning). `LLM_GW_DATA` is
also used to locate an existing config.

`-c` is optional. Without it, llm-gw uses the first existing path from:

1. `$LLM_GW_DATA/llm-gw.yaml`
2. `/etc/llm-gw/llm-gw.yaml` (legacy Docker mount)
3. `./llm-gw.yaml` (legacy working-directory config)

If none exists, it generates a new config in `$LLM_GW_DATA`. An explicitly
supplied `-c` path must exist.

To choose the password before the first run, set `LLM_GW_ADMIN_PASSWORD` (8–72
bytes; a trailing newline is trimmed, matching `llm-gw hash-password`). If the
generated password is lost, replace `admin.passwordHash` with the output of
`llm-gw hash-password`, or delete the config (destructive: it also deletes the
topology; persisted sessions are cleared on regeneration).

The rest of this section describes the full manual YAML configuration, which is
still supported and is the right choice for multi-provider topologies.

### Minimal configuration

Create `llm-gw.yaml`:

```yaml
gateway:
  listen: ":8787"

admin:
  listen: "127.0.0.1:8788"
  passwordHash: "<bcrypt hash>"   # empty disables the admin server

stats:
  path: "stats.sqlite"

entrypoints:
  - name: default
    match: ["*"]
    route: default

providers:
  - name: deepseek
    type: deepseek
    chat: https://api.deepseek.com/v1
    responses: https://api.deepseek.com/v1
    anthropic: https://api.deepseek.com/anthropic
    keys:
      - ${DEEPSEEK_API_KEY}

nodes:
  - name: deepseek-fast
    provider: deepseek
    model: deepseek-v4-flash
    # api: auto

routes:
  - name: default
    nodes: [deepseek-fast]
    strategy: round
```

Generate the admin password hash:

```bash
read -rsp "Admin password: " PASSWORD; echo
printf '%s' "$PASSWORD" | ./llm-gw hash-password
```

Paste the output into `admin.passwordHash`.

### Run

Zero-config first run (no YAML file):

```bash
LLM_GW_DATA=./data ./llm-gw serve
```

The log prints a one-time admin password. Open the admin console, log in, and
create the provider, node, route and entrypoint there. This is the same path the
Docker image uses.

Manual configuration:

```bash
DEEPSEEK_API_KEY=sk-... ./llm-gw serve -c llm-gw.yaml
```

- Gateway: `http://127.0.0.1:8787`
- Admin console: `http://127.0.0.1:8788/`

Send a request (after the manual DeepSeek topology or an equivalent zero-config
topology exists in the console):

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [{"role": "user", "content": "Say OK"}]
  }'
```

Because the entrypoint matches `*`, any client model is accepted. The response
`model` field is the provider model that actually served the request
(`deepseek-v4-flash` in this example).

Reload after editing the file:

```bash
pkill -HUP -f 'llm-gw serve'
```

## Admin console

The console is a React application embedded in the binary. It is served only
when `admin.passwordHash` is set.

| Page | What it manages |
| --- | --- |
| Providers | Provider type, API base URLs, keys, model catalog, disabled models, health probes |
| Nodes | Provider, one model, backup flag, weight, enabled switch |
| Routes | Ordered node list and strategy |
| Entrypoints | Ordered model globs and the route they select |
| Requests | Request history, timing, tokens, status and errors |
| Usage | Per-provider local usage and upstream balance or quota windows |
| Overview | Entrypoint → route → node → provider graph with live metrics |
| Settings | Listen addresses, retention and admin password |

Admin edits validate the new configuration, apply it to the data plane, and
then atomically rewrite the config file with `0600` permissions.

Provider keys are write-only through the admin API: `GET` returns only
`keysSet` and masked `keyPreviews`. New keys can be appended and existing keys
can be deleted; an existing key cannot be replaced in place.

A provider's `type` and its non-`generic` API base URLs are immutable after
creation. Delete and recreate the provider to change them, or edit the config
file and reload.

The topology view has monitor and edit modes. In monitor mode nodes are fixed;
in edit mode you can drag an entrypoint onto a route or a route onto a node to
change the configuration. Node positions are stored in the stats SQLite
database and shared by every admin; `localStorage` keeps a local backup and is
used if the server cannot be reached.

## Configuration reference

`config.Load` uses strict YAML decoding. Unknown fields fail the load, which is
why old routing keys are rejected instead of silently ignored. Names, URLs,
keys, models and match globs support `${VAR}` and `$VAR` environment expansion.
See [example.yaml](example.yaml) for a complete configuration.

A config file is optional; see [Zero-config first run](#zero-config-first-run)
for the generated path and the four first-run environment variables. Once a
config exists, it is the source of truth and those variables are not reapplied.

### Top level

```yaml
gateway: { ... }
admin: { ... }
stats: { ... }
log: { ... }
entrypoints: [ ... ]
providers: [ ... ]
nodes: [ ... ]
routes: [ ... ]
```

### `gateway`

| Field | Default | Description |
| --- | --- | --- |
| `listen` | `:8787` | Data-plane listen address |
| `readTimeout` | `60s` | Deadline for reading upstream HTTP response headers; 1s–300s |

### `admin`

| Field | Default | Description |
| --- | --- | --- |
| `listen` | `127.0.0.1:8788` | Admin listen address |
| `passwordHash` | empty | bcrypt hash; empty disables the admin server |
| `sessionTTL` | `12h` | Normal session lifetime |
| `rememberTTL` | `720h` | Lifetime of a remembered session |

### `stats`

| Field | Default | Description |
| --- | --- | --- |
| `path` | `stats.sqlite` | SQLite database path |
| `retainRequests` | `168h` | Raw request retention |
| `retainRollups` | `2160h` | Aggregated rollup retention |
| `capture.mode` | `off` | `off`, `errors` or `sample` |
| `capture.sampleRate` | `0.01` | Sample rate when mode is `sample` |
| `capture.maxBytes` | `32768` | Maximum bytes per preview field |
| `capture.retain` | `24h` | Capture row retention |

`errors` buffers every request but persists only failures (`status >= 400` or a
non-empty error). `sample` persists a random subset. On-demand capture rules can
be created from the Requests page or `POST /api/captures`; they are
time/limit-bounded, in-memory, and lost on restart. `stats.storeBody` is removed.

Captured bodies may contain prompts and tool arguments; keep capture off outside
an investigation, use a short retention, and protect the admin session and
database. Capture stores the bodies visible to the rewriter, not necessarily
byte-identical wire bytes: x skips compressed SSE and chunked non-streaming
bodies over its `MaxChunkSize` (1 MiB default), and adds SSE framing after the
rewriter.

### `log`

| Field | Default | Description |
| --- | --- | --- |
| `level` | `info` | `trace`, `debug`, `info`, `warn`, `error` or `fatal`; can change at runtime |
| `format` | `json` | `json` or `text`; fixed at startup |

`SIGHUP` and an admin config save apply a new `level` immediately, including the
`llm-api-converter` debug output routed through the same handler. A changed
`format` takes effect after a restart and logs a warning when changed at runtime.
`trace` also enables the underlying x request/response header dumps, which can
contain client credentials such as `Authorization` or `x-api-key`; use it only
for short debugging sessions and protect the log stream.

### `entrypoints[]`

```yaml
- name: fast
  match:
    - "claude-sonnet*"
    - "gpt-4.5*"
  route: fast
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Unique entrypoint name; no `/` |
| `match` | yes | Non-empty list of client model globs |
| `route` | yes | Existing route name |

Declaration order is the matching precedence. Globs inside one entrypoint are a
union.

### `providers[]`

```yaml
- name: deepseek
  type: deepseek
  chat: https://api.deepseek.com/v1
  responses: https://api.deepseek.com/v1
  anthropic: https://api.deepseek.com/anthropic
  keys: ["${DEEPSEEK_API_KEY}"]
  models:
    deepseek-v4-pro:
      disabled: true
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Unique provider name |
| `type` | yes | `deepseek`, `opencode-go`, `opencode-zen`, `commandcode`, `generic` |
| `chat` | no | OpenAI Chat Completions base URL |
| `responses` | no | OpenAI Responses base URL |
| `anthropic` | no | Anthropic Messages base URL |
| `gemini` | no | Gemini API base URL |
| `keys` | no | API keys; expanded from the environment |
| `models` | no | Local overrides, currently `disabled: true` |
| `auth` | no | Header/prefix/key-header overrides |
| `health` | no | Probe API, path, interval (default `30s`), timeout (default `5s`) |
| `tls` | no | `serverName`, `secure` (default `true`) |
| `enabled` | no | Set `false` to remove the provider from routing |

An empty API base URL means the provider does not serve that API. A node's
target API is chosen as follows:

- `auto` (the default) matches the inbound wire API to a configured provider
  API: `/v1/messages` uses `anthropic`, `/v1/responses` uses `responses`,
  `/v1/chat/completions` uses `chat`, and Gemini paths use `gemini`.
- When the matching base URL is missing, or the model's static classification
  excludes it, the gateway keeps the model's default API and converts the body.
- A pinned `node.api` bypasses default resolution and always uses that API.
  Generic providers have no static model classification, so auto matching is
  how their configured base URLs are selected.

The gateway authenticates every upstream request with the provider's configured
key and never forwards inbound client credentials. `Authorization`, `x-api-key`,
`x-goog-api-key` and `api-key` are removed before the provider headers are
applied; `auth.extra` is applied last and still wins. This matters when a client
such as Claude Code sends its own `Authorization` header, because upstreams may
prefer it over the injected `x-api-key` and reject the provider key.

With the quick-start example above, Claude Code talking Anthropic Messages to
`/v1/messages` is forwarded to DeepSeek's native
`https://api.deepseek.com/anthropic` endpoint instead of being converted to the
chat API.

### `nodes[]`

```yaml
- name: deepseek-fast
  provider: deepseek
  model: deepseek-v4-flash
  api: auto
  backup: false
  weight: 1
  enabled: true
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Unique node name; no `/` |
| `provider` | yes | Existing provider name |
| `model` | yes | Exactly one provider model ID |
| `api` | no | `auto` (default), `chat`, `responses`, `anthropic` or `gemini`; a pinned API requires the matching base URL |
| `backup` | no | Mark as a fallback node |
| `weight` | no | Selection weight; default `1`, must be `>= 0` |
| `enabled` | no | Set `false` to remove the node from routing |

### `routes[]`

```yaml
- name: fast
  nodes: [deepseek-fast, deepseek-backup]
  strategy: round
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Unique route name; no `/` |
| `nodes` | yes | Non-empty list of existing node names; duplicates rejected |
| `strategy` | no | `fifo`, `round` (default) or `random` |

## Stats and topology

Requests are recorded with route, node, provider, client model, target model,
status, duration and token counts. Raw rows are retained for
`stats.retainRequests`; rollups are retained for `stats.retainRollups`.
Rollups also store `cached_tokens`; rows written before that column existed
read as 0 cached tokens.

The graph API attributes each row to an entrypoint at query time by resolving
the row's `client_model` through the current entrypoint list:

```text
entrypoint:<name> -> route:<recorded route>
```

The edge uses the route recorded on the request, not the entrypoint's current
route. Renaming or reordering entrypoints therefore changes the historical
attribution, while old rows keep the route that actually served them. Rows with
no recorded route are shown as `unrouted`.

The topology only counts rows that still belong to the current topology: the
route must exist, the node must be referenced by that route, and the node's
provider must match. Stale historical nodes do not show old traffic.

## Admin API

All `/api/*` routes except `/api/health`, `/api/login` and `/api/logout`
require a session cookie.

```text
GET  /api/health
POST /api/login
POST /api/logout

GET  /api/config
PUT  /api/config
GET  /api/presets

GET|POST      /api/providers
GET|PUT|DELETE /api/providers/{name}
POST          /api/providers/{name}/test
GET           /api/providers/{name}/models

GET|POST      /api/nodes
GET|PUT|DELETE /api/nodes/{name}
GET|POST      /api/routes
PUT|DELETE    /api/routes/{name}
GET|PUT       /api/entrypoints

GET  /api/stats/summary
GET  /api/stats/graph
GET|PUT /api/topology/positions
GET  /api/requests
GET  /api/requests/{id}
GET  /api/provider-usage
GET|POST      /api/captures
DELETE        /api/captures/{id}
PUT  /api/admin/password
```

`GET /api/requests` accepts `errors=1` and `captured=1`. Request detail includes
an optional `capture` object; the list only reports a cheap `hasCapture` flag.

## Request correlation and capture

Every response that passes through a node hop carries
`X-LLM-GW-Request-ID`. The value is the same `sid` stored as the request's
`requestId` in stats and logs, so a client report can be matched to one row.
Gateway-generated 502/503 responses (no entrypoint match, upstream unreachable
before headers) do not pass through the node header rewrite and are a known v1
gap, as are request-side failures that produce no response.

The Requests page shows the structured rewriter fields (`inboundProtocol`,
`errorStage`, `errorDirection`, `ssePhase`, `eventIndex`) and any captured
original/rewritten previews. The Capture control creates a rule for a route
and/or client-model glob; use the "captured only" filter to find rows if the
client cannot report an ID.

Request rows also carry diagnostics that do not require body capture: `uri`,
`method`, `clientIp`, `userAgent`, `upstream`, `errorClass`, `clientStatus`,
`finishReason`, `sawDone` and `truncated`. `status` remains the upstream HTTP
status (0 when no response arrived); `clientStatus` is what the client actually
received (502 for a failed request with no upstream response). `errorClass` is
one of `upstream_auth`, `upstream_forbidden`, `upstream_not_found`,
`upstream_quota`, `upstream_bad_request`, `upstream_unavailable`,
`upstream_truncated`, `upstream_error`, `gateway_timeout`, `network`,
`rewrite_error` or `client_error`. `truncated` marks a streaming request that
ended without a terminal `finish_reason`/`stop_reason` or `[DONE]`; it is how a
clean-looking HTTP 200 cut is surfaced.

`PUT /api/entrypoints` replaces the whole ordered list. Entrypoint order is
precedence, so the list is edited and saved as one unit.

`GET /api/topology/positions` returns the shared layout map and `PUT` replaces
it. The layout lives in the `topology_positions` table of `stats.sqlite`.

## Token counting

Claude Code calls `POST /v1/messages/count_tokens` to size tool definitions,
skills and conversation fragments.

- Routes whose target API is Anthropic pass the request through; the existing
  top-level model rewrite still applies.
- Routes whose target API is Chat Completions, Responses or Gemini are
  answered locally with a conservative estimate. No upstream request is made,
  and the value is an approximation calibrated on DeepSeek V4; it is
  intentionally biased high so context compaction is not delayed.
- A route that mixes Anthropic and non-Anthropic nodes estimates for all
  `count_tokens` requests.
- Locally answered requests are not recorded in stats or captures.

## Provider usage and balances

The Usage page shows one card per provider. Local totals (requests, errors,
input/output/cached tokens) come from hourly rollups for the selected 24-hour,
7-day or 30-day window. Upstream values are queried per API key:

- **DeepSeek**: account balance from `/user/balance`.
- **CommandCode**: credits and the 5-hour/weekly windows from
  `/alpha/billing/credits`.
- **OpenCode Go**: rolling, weekly and monthly windows from
  `/zen/go/v1/usage`, or `/inference/go/v1/usage` for providers created with
  the new inference base URLs.

OpenCode Zen does not expose a key's credits through an API; its card says to
use the OpenCode console. `generic` providers have no known balance contract and
show a note. Disabled providers are not queried. A supported provider with no
keys shows "no API key configured".

Upstream results are cached for 60 seconds. The page's refresh button forces a
query; a failed refresh keeps the last successful amounts, marks the account
stale and caches the failure for the same 60 seconds. Balances are not persisted
and survive only in memory. The API returns masked key previews only; raw keys
are never returned, logged or stored in stats.

`GET /api/provider-usage` accepts `from`/`to` RFC 3339 bounds and `refresh=1`.
Local totals are omitted with a `localError` when stats are disabled, but
upstream balances are still returned.

## Docker

The image bundles the gateway and the built admin console and starts with no
config file:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest

docker logs llm-gw
```

The first start generates `/data/llm-gw.yaml` and `/data/stats.sqlite`. The log
contains a one-time admin password; open `http://localhost:8788/`, log in, and
create the upstream topology. The image sets `LLM_GW_DATA=/data` and
`LLM_GW_ADMIN_LISTEN=:8788` for the first run. The binary default remains
`127.0.0.1:8788`.

To choose the password, set it before the first start:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -e LLM_GW_ADMIN_PASSWORD='choose-a-long-password' \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

The three config-value variables are first-run defaults; `LLM_GW_DATA` is the
data directory used on every start. An existing config is not overridden; edit
`admin.listen` in the file or use the console. To keep the admin API inside the
container network on a fresh container, set `LLM_GW_ADMIN_LISTEN=127.0.0.1:8788`
before the first start.

Existing configs still work through the legacy mount:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro" \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

`/etc/llm-gw/llm-gw.yaml` is found when `/data/llm-gw.yaml` does not exist; use
`serve -c /etc/llm-gw/llm-gw.yaml` as the container command to be explicit.
Admin edits write the config file, so mount it writable if you want to manage
the gateway from the console; with a read-only mount, edit the host file and
send `SIGHUP` instead.

`/data` is the working directory, so generated files persist in the volume. The
container runs as UID/GID `10001`; a bind-mounted config must be readable by
that user and a bind-mounted `/data` must be writable by it. If the GHCR package
is private, run `docker login ghcr.io` with a token that can read packages
first.

Build the image from this directory; Go modules are downloaded from the module
proxy:

```bash
docker build -t llm-gw .
```

See [DOCKER.md](DOCKER.md) for build details and the GitHub Actions workflow.

## Development

```bash
# Go tests
GOWORK=off go test ./... -count=1

# Web tests and type check
cd web
npm test
npx tsc --noEmit
npm run build
```

Rebuild the web bundle before a Go build if the console changed:
`internal/admin/dist` is git-ignored and embedded at compile time.

## Project layout

```text
main.go                 CLI: serve, hash-password, SIGHUP reload, first-run bootstrap
internal/bootstrap      zero-config path resolution and first-run generation
internal/config         strict YAML schema, defaults, validation
internal/gateway        data plane: entrypoint matchers, routes, nodes
internal/provider       provider adapters and model catalogs
internal/rewriter       protocol/path conversion for upstream requests
internal/admin          management API and embedded console
internal/stats          SQLite storage, recorder, graph aggregation
web/                    React + Ant Design + React Flow console
example.yaml            full configuration example
DOCKER.md               image build and CI notes
```

## Security notes

- Provider API keys are stored in the YAML config. Protect the file and mount
  secrets through environment variables where possible.
- The generated config contains a bcrypt admin hash, not the plaintext
  password. The one-time password is printed to stderr/logs on the first run.
  In production, set `LLM_GW_ADMIN_PASSWORD` through your orchestrator's secret
  mechanism, or capture the first-run logs securely.
- A newly created data directory is `0700`; a pre-existing or bind-mounted
  directory is not changed, and startup warns if it is accessible by group or
  other. The config file is `0600`.
- The admin API is HTTP only. The binary binds to loopback by default; the
  Docker image binds `:8788` for first-run onboarding. Put a TLS reverse proxy
  in front of it before exposing it on a network.
- `LLM_GW_ADMIN_PASSWORD` is visible in the container environment; use an env
  file or an orchestrator secret to inject it.
- `admin.passwordHash` empty disables the admin server entirely, including the
  management API.
