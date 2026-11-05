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
- Serves an admin console for providers, nodes, routes, entrypoints, requests
  and a live topology view.
- Stores request history and rollups in SQLite; the topology can be filtered by
  time range down to one minute.
- Reloads configuration on `SIGHUP` or immediately after an admin edit.

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

The module uses local `replace` directives for three sibling repositories:

```text
../core
../x
../llm-api-converter
```

Check them out next to `llm-gw`, or use the Docker image:

```text
workspace/
├── core/
├── x/
├── llm-api-converter/
└── llm-gw/
```

### Build

```bash
# from the llm-gw directory
(cd web && npm ci && npm run build)
GOWORK=off go build -o llm-gw .
```

The web build writes to `internal/admin/dist`, which is embedded into the Go
binary. The `GOWORK=off` flag keeps the build scoped to this module when a
parent `go.work` exists.

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

```bash
DEEPSEEK_API_KEY=sk-... ./llm-gw serve -c llm-gw.yaml
```

- Gateway: `http://127.0.0.1:8787`
- Admin console: `http://127.0.0.1:8788/`

Send a request:

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
| Topology | Entrypoint → route → node → provider graph with live metrics |
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
change the configuration. Node positions are persisted.

## Configuration reference

`config.Load` uses strict YAML decoding. Unknown fields fail the load, which is
why old routing keys are rejected instead of silently ignored. Names, URLs,
keys, models and match globs support `${VAR}` and `$VAR` environment expansion.
See [example.yaml](example.yaml) for a complete configuration.

### Top level

```yaml
gateway: { ... }
admin: { ... }
stats: { ... }
entrypoints: [ ... ]
providers: [ ... ]
nodes: [ ... ]
routes: [ ... ]
```

### `gateway`

| Field | Default | Description |
| --- | --- | --- |
| `listen` | `:8787` | Data-plane listen address |

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
| `storeBody` | `false` | Reserved; request/response bodies are not stored yet |

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

An empty API base URL means the provider does not serve that API. The gateway
picks the first API the node's model advertises and the provider has a base URL
for, in `chat`, `responses`, `anthropic`, `gemini` order.

### `nodes[]`

```yaml
- name: deepseek-fast
  provider: deepseek
  model: deepseek-v4-flash
  backup: false
  weight: 1
  enabled: true
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Unique node name; no `/` |
| `provider` | yes | Existing provider name |
| `model` | yes | Exactly one provider model ID |
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
GET  /api/requests
GET  /api/requests/{id}
PUT  /api/admin/password
```

`PUT /api/entrypoints` replaces the whole ordered list. Entrypoint order is
precedence, so the list is edited and saved as one unit.

## Docker

The image bundles the gateway and the built admin console:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro" \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

`/data` is the working directory, so a relative `stats.path` persists in the
volume. The container runs as UID/GID `10001`. If the GHCR package is private,
run `docker login ghcr.io` with a token that can read packages first.

The example mounts the config read-only. Admin edits write the config file, so
mount it writable if you want to manage the gateway from the console; with a
read-only mount, edit the file on the host and send `SIGHUP` instead.

Building locally requires the parent directory that contains the sibling
repositories:

```bash
docker build -f llm-gw/Dockerfile -t llm-gw .
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
main.go                 CLI: serve, hash-password, SIGHUP reload
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
- The admin API is HTTP only and binds to loopback by default. Put a TLS
  reverse proxy in front of it before exposing it on a network.
- `admin.passwordHash` empty disables the admin server entirely, including the
  management API.
