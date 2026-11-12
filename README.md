# Cuttle

**The cuttlefish of LLM gateways.** / **一个入口，所有方言。**

`Cuttle` is a self-hosted LLM gateway with an embedded admin console. It gives
clients one stable endpoint for several providers, routes each client model to a
provider model, converts between wire APIs, and records per-request statistics.

The name is short for **cuttlefish**: many arms, rapid mimicry, and near-invisible
camouflage — the same properties a gateway needs to reach many providers, speak
each API dialect, and stay out of the client's way.

![Cuttle admin console overview](docs/assets/demo.gif)

*The overview topology: entrypoints, routes, nodes and providers with live
metrics.*

## What it does

- Accepts OpenAI Chat Completions, OpenAI Responses, Anthropic Messages and
  Gemini `generateContent` requests.
- Converts requests and responses between the inbound API and the provider's
  native API.
- Matches client model names with case-insensitive globs and an explicit
  precedence order.
- Gives every provider, key, node, route, entrypoint and gateway key an
  immutable id plus an editable display name, so renames keep request history,
  per-key usage and the topology layout.
- Balances or fails over across provider keys, nodes and backup nodes, and lets
  a node pin one provider key by id for account-level isolation.
- Keeps provider keys write-only in the admin API and masks previews; every key
  has a name.
- Authenticates data-plane clients with named gateway keys and reports per-key
  usage, or stays open when no keys are configured.
- Serves an admin console for providers, nodes, routes, entrypoints, gateway
  keys, requests, a provider usage/balance view and a live topology view.
- Stores request history and rollups in SQLite; the topology can be filtered by
  time range down to one minute.
- Reloads configuration on `SIGHUP` or immediately after an admin edit.
- Starts without a config file: a zero-config first run creates the config and
  a one-time admin password under `$CUTTLE_DATA`, then the console creates the
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
                 └─ ordered nodes, strategy (fifo / round / random / sticky)
                    and an optional retry budget (`retries` / `retryCodes`)
```

- An **entrypoint** is the client-facing ingress. It has a name, a non-empty
  list of model globs, and one route. The route may be empty while drafting.
- Entrypoints are evaluated in declaration order. The first entrypoint with a
  matching glob wins; `*` is the explicit catch-all.
- Globs are case-insensitive. `*` is the only wildcard.
- A **route** contains only nodes, a selection strategy and an optional retry
  budget. It does not match anything by itself.
- A **node** binds at most one provider and one provider model. `backup` marks
  fallback nodes; `weight` controls the round-robin share. `key` optionally pins
  one provider key by id; without it the node spreads across every enabled key.
- A node's target API is `auto` by default. The gateway matches the inbound wire
  API (`/v1/chat/completions`, `/v1/responses`, `/v1/messages`, or
  `/v1beta/models/...`) to a configured provider API and converts only when the
  provider has no matching base URL. Set `api` on the node to pin one API.
- Disabling a provider, a node, or a provider model removes that node from the
  compiled route. A route with no usable nodes fails the request instead of
  passing the client model through.
- An entrypoint without a route, or a node without a provider, is a **draft**:
  it is saved, shown on the topology and shared with every admin, but it serves
  no traffic. A request that matches a draft entrypoint fails (502) and is
  recorded as unrouted; it never falls through to a later entrypoint. Skip a
  trailing `*` draft entrypoint or it will claim every model.
- If no entrypoint matches, the request is unrouted and returns an error.

> The routing model is intentionally strict. Old configurations using
> `aliases`, route `match`, or node `models` maps are rejected at load time.
> There is no automatic migration. The one automatic config migration is the
> entity-id rewrite described in [Identity: ids and
> names](#identity-ids-and-names).

## Identity: ids and names

Every provider, provider key, node, route, entrypoint and gateway key has two
fields:

- `id` is the stable handle. It looks like `<prefix>_<10 base32 characters>`:
  `p_` provider, `pk_` provider key, `n_` node, `r_` route, `e_` entrypoint,
  `gk_` gateway key. It is globally unique within the file and never changes.
  Every association (`entrypoints[].route`, `nodes[].provider`, `nodes[].key`,
  `routes[].nodes`) stores an id, and so do stats rows, the admin API and the
  saved topology layout.
- `name` is a display label. It must be unique per entity kind (provider key
  names only inside their provider), at most 64 characters, and must not contain
  `$` or control characters. It is otherwise free-form: spaces, `/` and
  non-ASCII are fine. Rename it at any time from the console; request history,
  per-key usage and the topology keep following the same id.

The console generates ids when it creates an entity. A hand-written config may
omit them: `config.Load` derives a stable id from the name, rewrites the file
with explicit ids, and keeps the previous version as `<path>.bak-<unix>`. The
rewrite happens before environment expansion, so `${VAR}` placeholders are never
baked into the file, and every config write normalizes the file to `0600`.
Validation runs after the write-back, so a config that is rejected for an
unrelated reason has already been rewritten on disk.

A hand-maintained config, in particular a read-only Docker mount, cannot be
rewritten and still loads: the derived ids are stable across restarts. In that
case the file is the only identity store, so **keep the `id` line when you
rename an entity**. Dropping it makes the new name the derivation input, so the
entity gets a new identity and its history splits. Console edits always need a
writable config file; with a read-only mount, edit the host file and send
`SIGHUP`.

The write-back needs a writable *directory*, not a writable file: `config.Write`
creates a temp file beside the config and renames it over the original, so a
`chmod 400` file inside a writable directory is still rewritten (and normalized
to `0600`). A config path that is a symlink is skipped with a warning instead,
so a file symlinked out of a dotfiles repository keeps its link and receives
edits through its target. Both outcomes are logged: the startup path (and every
`SIGHUP` reload) logs the backup path at info on success and a warning when the
write-back is skipped or fails.

## Quick start

### Requirements

- Go 1.26.4 or newer (`go.mod`) to build from source.
- Node.js 22 and npm to rebuild the admin console.
- SQLite is embedded; no external database is required.

The module depends on the published versions of `core`, `x` and
`llm-api-converter`; no sibling checkouts are required. If a parent `go.work`
exists, `GOWORK=off` keeps the build on the tagged module versions instead of
the local workspace directories.

### Download

Prebuilt binaries for Linux, macOS and Windows are attached to every
[GitHub release](https://github.com/ginuerzh/cuttle/releases). Archives are
listed in `checksums.txt`; verify before running a downloaded binary:

```bash
sha256sum -c checksums.txt --ignore-missing
```

### Build

```bash
# from the cuttle directory
(cd web && npm ci && npm run build)
GOWORK=off go build -o cuttle .
```

The web build writes to `internal/admin/dist`, which is embedded into the Go
binary. The `GOWORK=off` flag keeps the build scoped to this module when a
parent `go.work` exists.

### Versioning

`cuttle version` (also `--version` and `-v`) prints the build identity. Release
tags report `vX.Y.Z`, builds from `main` report `main-<short-sha>`, and a plain
local build reports `dev`. The same value appears at the bottom of the admin
navigation panel and in `GET /api/health` as `version`, `commit` and `builtAt`.

CI passes the tag or branch identity to the Docker build. A local build can set
it explicitly:

```bash
GOWORK=off go build \
  -ldflags "-X github.com/ginuerzh/cuttle/internal/version.Version=dev \
    -X github.com/ginuerzh/cuttle/internal/version.Commit=$(git rev-parse HEAD) \
    -X github.com/ginuerzh/cuttle/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o cuttle .
```

Releases are created by pushing an annotated `vX.Y.Z` tag. The release workflow
builds the console and publishes GoReleaser archives plus checksums to GitHub,
while the Docker workflow publishes the semver image tags alongside `latest`.

**`v0.2.0` is a breaking release.** The configuration schema, the admin API paths
and the stats schema all switch from names to immutable entity ids, so an
upgrade migrates both the config file and the statistics database once (each
with a backup; see [Identity: ids and names](#identity-ids-and-names) and
[Stats and topology](#stats-and-topology)). The version string itself is
injected by the release build through ldflags, so the upgrade needs no code or
config change beyond reviewing the backups. To roll back, restore both backups
and the previous image.

### Zero-config first run

No config file is required. Start the binary (or the Docker image) and it
creates `$CUTTLE_DATA/cuttle.yaml` and `$CUTTLE_DATA/stats.sqlite`:

```bash
CUTTLE_DATA=./data ./cuttle serve
```

With `CUTTLE_DATA` unset the directory is `<os.UserConfigDir>/cuttle`:
`~/.config/cuttle` on Linux (`$XDG_CONFIG_HOME` wins),
`~/Library/Application Support/cuttle` on macOS and `%AppData%\cuttle` on
Windows. If the user config directory is unavailable — a service without
`$HOME`, for example — Cuttle uses the working directory and logs a warning.

If `CUTTLE_ADMIN_PASSWORD` is not set, Cuttle generates a 32-character admin
password, prints it once to stderr, and stores only its bcrypt hash. Open the
admin console, log in, and create a provider, node, route and entrypoint. No
provider, key or model environment variables exist.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CUTTLE_DATA` | `<os.UserConfigDir>/cuttle` (`~/.config/cuttle` on Linux) | Directory for the generated config and stats database |
| `CUTTLE_LISTEN` | `:8787` | First-run `gateway.listen` |
| `CUTTLE_ADMIN_LISTEN` | `127.0.0.1:8788` (binary); `:8788` (Docker image) | First-run `admin.listen` |
| `CUTTLE_ADMIN_PASSWORD` | unset | First-run admin password; hashed into the config |

All four are optional. The three config-value variables are **first-run
defaults**: once a config exists, the file and the console are authoritative and
the variables are ignored (a differing value logs a warning). `CUTTLE_DATA` is
also used to locate an existing config.

`-c` is optional. Without it, Cuttle uses the first existing path from:

1. `$CUTTLE_DATA/cuttle.yaml`
2. `/etc/cuttle/cuttle.yaml` (Docker mount)
3. `./cuttle.yaml` (working-directory config)

If none exists, it generates a new config in `$CUTTLE_DATA`. An explicitly
supplied `-c` path must exist.

To choose the password before the first run, set `CUTTLE_ADMIN_PASSWORD` (8–72
bytes; a trailing newline is trimmed, matching `cuttle hash-password`). If the
generated password is lost, replace `admin.passwordHash` with the output of
`cuttle hash-password`, or delete the config (destructive: it also deletes the
topology; persisted sessions are cleared on regeneration).

> Upgrading from a build whose default was `/data`: the binary no longer uses
> `/data` unless `CUTTLE_DATA=/data` is set. Set the variable or move the
> directory before starting the new version; there is no automatic migration.

The rest of this section describes the full manual YAML configuration, which is
still supported and is the right choice for multi-provider topologies.

### Minimal configuration

Create `cuttle.yaml`:

```yaml
gateway:
  listen: ":8787"

admin:
  listen: "127.0.0.1:8788"
  passwordHash: "<bcrypt hash>"   # empty disables the admin server

stats:
  path: "stats.sqlite"

entrypoints:
  - id: e_q7a4m2xk9d
    name: default
    match: ["*"]
    route: r_7d2n5v8b1e

providers:
  - id: p_3k9x2m4q7a
    name: deepseek
    type: deepseek
    chat: https://api.deepseek.com/v1
    responses: https://api.deepseek.com/v1
    anthropic: https://api.deepseek.com/anthropic
    keys:
      - id: pk_8m2xq7a4k9
        name: default
        key: ${DEEPSEEK_API_KEY}

nodes:
  - id: n_4q7a9x2m3k
    name: deepseek-fast
    provider: p_3k9x2m4q7a
    model: deepseek-v4-flash
    # key: pk_8m2xq7a4k9   # pin one provider key id; omit to spread across all enabled keys
    # api: auto

routes:
  - id: r_7d2n5v8b1e
    name: default
    nodes: [n_4q7a9x2m3k]
    strategy: round
```

The ids are shown for concreteness. On a first run you can omit every `id` line
and let the loader assign and write them back; see [Identity: ids and
names](#identity-ids-and-names).

Generate the admin password hash:

```bash
read -rsp "Admin password: " PASSWORD; echo
printf '%s' "$PASSWORD" | ./cuttle hash-password
```

Paste the output into `admin.passwordHash`.

### Run

Zero-config first run (no YAML file):

```bash
CUTTLE_DATA=./data ./cuttle serve
```

The log prints a one-time admin password. Open the admin console, log in, and
create the provider, node, route and entrypoint there. This is the same path the
Docker image uses.

Manual configuration:

```bash
DEEPSEEK_API_KEY=sk-... ./cuttle serve -c cuttle.yaml
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
pkill -HUP -f 'cuttle serve'
```

## Admin console

The console is a React application embedded in the binary. It is served only
when `admin.passwordHash` is set.

| Page | What it manages |
| --- | --- |
| Providers | Provider type, API base URLs, keys, model catalog, disabled models, health probes |
| Nodes | Provider (empty while drafting), one model, backup flag, weight, enabled switch |
| Routes | Ordered node list, selection strategy and the retry budget (`retries` / `retryCodes`) |
| Entrypoints | Ordered model globs and the route they select (empty while drafting) |
| Gateway keys | Named data-plane credentials, per-key 24h/7d/30d usage, enable switches, rotation |
| Sessions | Session history, timing, tokens, status and errors |
| Usage | Per-provider local usage and upstream balance or quota windows |
| Overview | Entrypoint → route → node → provider graph with live metrics, per-node output token speed, one-shot traffic-flow dots and a notification bell for recent gateway-key auth failures |
| Settings | Listen addresses, retention and admin password |

Admin edits validate the new configuration, apply it to the data plane, and
then atomically rewrite the config file with `0600` permissions.

Provider keys are write-only through the admin API: `GET` returns each key's
`id`, `name`, `enabled` flag and masked `keyPreview`, never the value. In the
console you append a new `{name, key}`, rename a key, toggle `enabled`, delete
one, or delete and re-add the same name to rotate a value. `PUT
/api/providers/{id}` carries `keyUpdates` addressed by key id: `{id, name}`
renames, `{id, key}` replaces the value, `{id, enabled}` toggles, `{id,
delete:true}` deletes (rejected when a node of that provider pins the key), and
an entry with no id plus `{name, key}` appends a new key with a generated id.
Existing values are never returned or replaced in place. A node's `key` pin
holds the key id, so renaming a key never touches the nodes that pin it.

A provider's `type` and its non-`generic` API base URLs are immutable after
creation. Delete and recreate the provider to change them, or edit the config
file and reload.

The topology view has monitor and edit modes. In monitor mode nodes are fixed;
in edit mode you can drag an entrypoint onto a route, a route onto a node or a
node onto a provider to change the configuration. A draft card is tagged
`未连接` (not connected); its edge panel offers `断开` to clear the reference
again. Node positions are stored in the stats SQLite
database and shared by every admin; `localStorage` keeps a local backup and is
used if the server cannot be reached.

The monitor streams live data over the `GET /api/events` WebSocket. New traffic
flashes the node icon once and sends light dots along the connection. The
toolbar `动画` (Animation) switch stops both effects without pausing metrics and
is remembered per browser. The time selector picks the initial lookback; the end
is always the current time, so request, error and token totals only increase
until you start a new session.
Rejected gateway-key requests are tracked in memory (the last 50 since process
start; credentials are never kept). The bell next to the edit toggle shows a
red dot while any are retained; click it for the newest first list with the
reason, client IP, method, path and time, and use `清除` to empty the record.
The bell stays live in edit mode, and these failures are not written to SQLite.
The selector keeps its labels but now means "look back this far when the
session starts". **Reset stats** clears the display and reconnects with
`reset=1`, starting at zero without querying history. A session older than
seven days is closed and restarted from the selected lookback. P50/P95 are
percentiles over the accumulated window, not cumulative sums. See
[Admin API](#admin-api) for the reverse-proxy requirements.

## Configuration reference

`config.Load` uses strict YAML decoding. Unknown fields fail the load, which is
why old routing keys are rejected instead of silently ignored. Names, ids, URLs,
keys, models and match globs support `${VAR}` and `$VAR` environment expansion.
The load order is: decode, migrate entity ids and write the file back, expand
the environment, apply defaults, then validate. In practice ids are literals,
because the generator never emits `$`. See [example.yaml](example.yaml) for a
complete configuration.

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
| `userAgent` | `cuttle/<version>` | Upstream User-Agent; provider-level `userAgent` or `headers` can override it |
| `keys` | empty (data plane open) | Named data-plane access keys; see [Gateway keys](#gateway-keys) |

### Gateway keys

Named gateway keys authenticate clients on the data-plane port. They are
separate from the admin password and are never forwarded to a provider.

```yaml
gateway:
  listen: ":8787"
  keys:
    - id: gk_5x2m4q7a9k
      name: claude-code
      key: ${CLAUDE_GATEWAY_KEY}
      enabled: true
    - id: gk_1c7f3b8n2v
      name: laptop-sdk
      key: sk-gw-...
      enabled: false
```

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Immutable gateway key id, the handle usage and the Sessions filter use |
| `name` | yes | Unique display label, at most 64 characters, no `$` or control characters |
| `key` | yes | Unique value, 8–512 characters, no `$` or control characters; `${VAR}`/`$VAR` are expanded |
| `enabled` | no | Default `true`; a disabled entry stays configured but never authenticates |

Names and values must not contain a literal `$` after environment expansion.
`config.Load` expands `${VAR}` and `$VAR` on every reload, so a value stored
verbatim through the admin API would be reinterpreted or fail startup the next
time the config is loaded. Put the secret in the environment and reference it,
or paste a value that contains no `$`. Control characters are rejected in
values as well because they can never authenticate.

An absent or empty `keys` list keeps the data plane open. Any entry turns
authentication on. If every entry is disabled, every data-plane request is
rejected with 401 and the gateway logs a warning when the configuration is
applied.

Accepted credential headers:

- `Authorization: Bearer <value>` (the scheme is case-insensitive)
- `x-api-key: <value>`
- `x-goog-api-key: <value>`
- `api-key: <value>`

**Any candidate may authenticate.** A junk value in one header does not veto a
valid value in another, which is the normal Claude Code and SDK case. When
several candidates match, the request is attributed to the first enabled key in
configuration order. Query-string credentials (for example Gemini's `?key=`) are
not supported and do not authenticate.

`GET` and `HEAD /api/hello` remain public for Claude Code's connection probe.
Every other path, including `POST /v1/messages/count_tokens`, requires a valid
key when any key is configured. In authenticated mode the gateway serves exactly
one request per connection; keep-alive reuse requires a new connection and
re-authentication.

A rejected request gets:

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json
WWW-Authenticate: Bearer realm="cuttle"
Connection: close

{"error":{"message":"invalid gateway key","type":"authentication_error"}}
```

The gateway does not call the upstream. It logs a warning with the client IP,
method, path and reason (`missing`, `disabled` or `invalid`); the credential is
never logged. The same failures are kept in a bounded in-memory record and shown
in the Overview notification bell; `清除` empties that record and they reset
when the gateway restarts. A malformed request head returns 400. The 401 body
and headers are
the same for every inbound protocol.

Manage keys on the Gateway keys page or through the [Admin API](#admin-api).
`POST /api/gateway-keys` and `POST /api/gateway-keys/{id}/rotate` return the new
secret once; copy it then, because later reads only return a masked preview.
`PUT /api/gateway-keys/{id}` changes the enabled flag and may also rename the
label, keeping the id. Rotation and deletion take effect on the next request
without a restart: the data plane reloads before the config file is written.

Per-key requests, errors and input/output/cached tokens come from hourly
`key_rollups`, so they remain accurate for the 7-day and 30-day views even
though raw request rows are retained for 168 hours by default. Usage recorded
before this feature has no key attribution, and deleted keys keep their history
as unconfigured rows until normal rollup retention removes it.

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
| `capture.maxBytes` | `1048576` | Maximum bytes per preview field |
| `capture.retain` | `24h` | Capture row retention |

`errors` buffers every request but persists only failures (`status >= 400` or a
non-empty error). `sample` persists a random subset. On-demand capture rules can
be created from the Sessions page or `POST /api/captures`; they are
time/limit-bounded, in-memory, and lost on restart. `stats.storeBody` is removed.

Captured failures also include an allowlisted header subset: `content-type`,
`user-agent`, `retry-after`, `x-request-id`, `x-cuttle-request-id`,
`x-opencode-session`, `cf-ray`, `anthropic-version`, and
`anthropic-ratelimit-*` / `x-ratelimit-*`. Authorization, cookie, set-cookie
and API-key headers are never stored.

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
- id: e_2v8n5b1c7f
  name: fast
  match:
    - "claude-sonnet*"
    - "gpt-4.5*"
  route: r_7d2n5v8b1e
```

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Immutable entrypoint id; assigned by the loader or the console |
| `name` | yes | Unique display label |
| `match` | yes | Non-empty list of client model globs |
| `route` | no | Existing route id; empty while drafting (the entrypoint then serves no traffic) |

Declaration order is the matching precedence. Globs inside one entrypoint are a
union.

### `providers[]`

```yaml
- id: p_3k9x2m4q7a
  name: deepseek
  type: deepseek
  chat: https://api.deepseek.com/v1
  responses: https://api.deepseek.com/v1
  anthropic: https://api.deepseek.com/anthropic
  keys:
    - id: pk_8m2xq7a4k9
      name: default
      key: "${DEEPSEEK_API_KEY}"
  models:
    deepseek-v4-pro:
      disabled: true
```

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Immutable provider id |
| `name` | yes | Unique provider display label |
| `type` | yes | `deepseek`, `opencode-go`, `opencode-zen`, `commandcode`, `openrouter`, `generic` |
| `chat` | no | OpenAI Chat Completions base URL |
| `responses` | no | OpenAI Responses base URL |
| `anthropic` | no | Anthropic Messages base URL |
| `gemini` | no | Gemini API base URL |
| `keys` | no | Named keys `{id, name, key, enabled}`; values expand from the environment; names are unique per provider |
| `models` | no | Local overrides, currently `disabled: true` |
| `auth` | no | Header/prefix/key-header overrides |
| `userAgent` | no | Overrides `gateway.userAgent` for this provider |
| `headers` | no | Extra upstream request headers; applied after auth and before `auth.extra` |
| `health` | no | Probe API, path, interval (default `30s`), timeout (default `5s`) |
| `tls` | no | `serverName`, `secure` (default `true`) |
| `enabled` | no | Set `false` to remove the provider from routing |

A provider can hold several named keys, for example one per upstream account.
Each entry is `{id, name, key, enabled}`; the `name` is a display label and the
`id` is what a node pin references. A disabled key stays configured but is
skipped when routes compile. Without a node pin, every enabled key becomes a
separate x node and the route selector spreads traffic and fails over across
them. Set `node.key` to the key id to use only one of them.

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

The gateway also owns the upstream `User-Agent`: it sends
`gateway.userAgent` (default `cuttle/<version>`) instead of the client's UA.
`provider.userAgent` overrides it, and `provider.headers` can add or replace
any header except `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`,
`Proxy-Connection`, `Upgrade`, `Accept-Encoding` and `Proxy-Authorization`.
This prevents clients such as `Python-urllib` from being rejected by
Cloudflare bot rules (CommandCode `error code: 1010`). The client's own UA is
still recorded in `stats.requests.user_agent` for attribution.

For `opencode-go` providers the gateway injects `x-opencode-session` from the
request ID when the client sent no session-like header at all. If the client
already sends a session header, including Claude Code's native session header,
it is preserved. Without a recognized session header, OpenCode returns 400
`MissingSessionID`.

With the quick-start example above, Claude Code talking Anthropic Messages to
`/v1/messages` is forwarded to DeepSeek's native
`https://api.deepseek.com/anthropic` endpoint instead of being converted to the
chat API.

`openrouter` is a built-in provider type with native Chat, Responses and
Anthropic Messages endpoints. All three use `Authorization: Bearer`:

```yaml
- id: p_9q3k7a1x5m
  name: openrouter
  type: openrouter
  chat: https://openrouter.ai/api/v1
  responses: https://openrouter.ai/api/v1
  anthropic: https://openrouter.ai/api
  keys:
    - id: pk_2x5m7a9q3k
      name: default
      key: "${OPENROUTER_API_KEY}"
```

OpenRouter does not implement `/v1/messages/count_tokens`, so its routes answer
those requests with the gateway's local token estimate while still forwarding
Messages to the native endpoint.

### `nodes[]`

```yaml
- id: n_4q7a9x2m3k
  name: deepseek-fast
  provider: p_3k9x2m4q7a
  model: deepseek-v4-flash
  api: auto
  backup: false
  weight: 1
  # key: pk_8m2xq7a4k9   # optional: pin one provider key id
  enabled: true
```

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Immutable node id |
| `name` | yes | Unique node display label |
| `provider` | no | Existing provider id; empty while drafting (the node is skipped when its route compiles) |
| `model` | with a provider | Exactly one provider model ID; optional on a providerless draft |
| `api` | no | `auto` (default), `chat`, `responses`, `anthropic` or `gemini`; a pinned API requires the matching base URL and a provider |
| `key` | no | Pin one provider key id; empty spreads across every enabled key; requires a provider |
| `backup` | no | Mark as a fallback node |
| `weight` | no | Selection weight; default `1`, must be `>= 0` |
| `enabled` | no | Set `false` to remove the node from routing |

### `routes[]`

```yaml
- id: r_7d2n5v8b1e
  name: fast
  nodes: [n_4q7a9x2m3k, n_6b1w8z3p5c]
  strategy: round
  maxFails: 2
  failTimeout: 30s
```

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Immutable route id |
| `name` | yes | Unique route display label |
| `nodes` | yes | Ordered list of existing node ids; duplicates rejected. Order is the selection priority. |
| `strategy` | no | `fifo`, `round` (default), `random` or `sticky` |
| `maxFails` | no | Consecutive failures before a node is marked dead; default `1` |
| `failTimeout` | no | How long a dead node is skipped; default `10s` |
| `retries` | no | Extra attempts; `0` disables retries, default `0` (max `10`) |
| `retryCodes` | no | Statuses that trigger a retry, default `429,5xx`; comma-separated exact codes and `Nxx` wildcards |

`sticky` keeps one conversation on one node: it reads the client's session
header (`x-claude-code-session-id`, `x-opencode-session`, `x-session-id` and the
other known session headers) and uses weighted rendezvous hashing, so new
conversations split by node weight while a conversation keeps its upstream
prompt cache. A request without a session header falls back to weighted random.

`retries` is an opt-in budget of extra attempts, so a route with `retries: 2`
makes at most three upstream attempts. A retry re-runs node selection through
the route strategy, so `fifo`, `round`, `random`, `sticky`, `backup`, `weight`
and the failure marker (`maxFails`/`failTimeout`) decide the next node exactly
as they would for a new request. A retry only happens before any response byte
reaches the client: a dial/connect failure and an upstream response status that
matches `retryCodes` are retried, while an error after the request was written
(including a broken stream) is not, because the upstream may already have
charged for it. Retries buffer the request body for replay up to 32 MiB; a
larger body, or a request carrying `Expect: 100-continue`, is forwarded exactly
once with no retry. There is no `Retry-After` backoff, delay or jitter in v1:
each attempt starts immediately, and each attempt is still bounded by
`gateway.readTimeout` for the upstream response head, so `retries: 10` can hold
a client connection for roughly eleven read timeouts (SSE responses already
have no total read bound). A discarded response is logged at `warn` level with
the node and status, and is never stored. Retries do not apply to TLS
passthrough, HTTP/2 connections or the locally answered `count_tokens`
estimate.

## Stats and topology

Requests are recorded with route, node, provider, client model, target model,
gateway key, status, duration and token counts. Those columns hold immutable
entity ids; the gateway key column is the id of the key that authenticated the
request and is empty in open mode. Raw rows are retained for
`stats.retainRequests`; rollups are retained for `stats.retainRollups`. Rollups
also store `cached_tokens`; rows written before that column existed read as 0
cached tokens.

The database is at schema version 2. On the first start after upgrading from the
name-based schema, the store copies `stats.sqlite` to
`stats.sqlite.v1.bak-<unix>` and rewrites the existing name values in
`requests`, `rollups`, `key_rollups`, `probe_results` and the saved topology
positions to entity ids, in one transaction; a failed copy aborts the migration
rather than upgrading without a backup. The copy is a `VACUUM INTO`, so the
filesystem needs free space equal to the database size (roughly 2×
`stats.sqlite`). A successful migration logs the backup path at info. If the
copy fails, the database stays at version 1, the data plane keeps recording the
old names, the binary logs the failure at error level once, retries only on the
next process start (not on every reload), and reports `statsMigrationError` in
`GET /api/health` until an attempt succeeds. A fresh database is created at
version 2 and never runs the rewrite. Historical names are kept in
`entity_labels`, which is never pruned, so a renamed or deleted entity still
shows its last known label. When a v1 series and an id-keyed series land in the
same rollup bucket, they are summed rather than one overwriting the other.

The guard that decides whether a stored value still needs an id checks the id
*shape* (`p_` or another prefix plus 10 base32 characters), so a v1 **name** that
happens to look like an id was already treated as an id and is not rewritten.
That is the one way the migration can leave a stale value behind; give such an
entity an explicit id if that matters to you.

Each row carries `retries`, the number of extra upstream attempts made for that
client request (`0` when the first attempt was the only one). It counts extra
attempts only: one client request is always one row, and rollups, graphs and
filters are unchanged. The discarded attempts themselves are stored beside the
row and exposed through `GET /api/attempts` and the request detail's `attempts`
array. Sessions tags a retried row with `重试 N 次`, and the detail panel repeats
the count and lists the discarded attempts under `上游尝试`. The node panel merges
the discarded attempts of the window into the session list newest first, tagging
them `重试尝试` (clicking one opens its parent request), and summarises them beside
the request count; the dead badge's tooltip explains the node with
`失败 N（…）· 重试丢弃 M · 最后 HH:MM:SS <status>`, or `窗口内没有失败记录` when
the window has neither.
Body capture keeps only the final attempt, so a captured pair describes the
response the client actually received.

Attempts are emitted by the `github.com/go-gost/x` release that writes the
`http.attempts` recorder field: with the pinned `v0.31.1` these lists stay empty
and the attempt counters read zero, so cuttle behaves exactly as before.

Hourly `key_rollups` store requests, errors and input/output/cached tokens per
gateway key id for the per-key usage view. They use the same
`stats.retainRollups` cutoff as provider rollups, so deleting a key keeps its
history only until normal cleanup removes it. Rows and rollups written before
gateway keys existed have no key attribution and are not backfilled. Rejected
401 requests and locally answered `count_tokens` requests are not recorded in
SQLite, so they produce no key usage; rejected 401s are still surfaced in the
Overview notification bell.

`GET /api/routes/health` returns each route's node markers: `failCount`,
`lastFailure`, `dead` and `deadUntil`. The state is x's in-memory marker,
resets when the hop is rebuilt, and only reflects the failures x marks: TCP
dial failures, responses matching a node's HTTP `failCodes` (currently 429 and
5xx), and every status a route retried, including a `retryCodes` match outside
`failCodes` — the final attempt of an exhausted budget marks the node too, so a
retried node stays dead for `failTimeout` instead of looking healthy right
after refusing the request. A streaming HTTP 200 truncation is not a marker
failure. Because a node can be bound by several routes, the marker is per
route: the topology marks a node dead only when every route that reports it is
dead, a node dead in some routes shows an amber partial badge, its dead
`route -> node` edges are drawn dashed red, and the node panel lists the dead
routes.

The graph API attributes each row to an entrypoint at query time by resolving
the row's `client_model` through the current entrypoint list:

```text
entrypoint:<id> -> route:<recorded route id>
```

Graph nodes and edges are keyed by entity id (`entrypoint:<id>`, `route:<id>`,
`node:<id>`, `provider:<id>`), so renaming an entity keeps its graph position and
its traffic. The edge uses the route recorded on the request, not the
entrypoint's current route. Editing an entrypoint's globs or reordering
entrypoints therefore changes the historical attribution, while old rows keep
the route id that actually served them. Rows with no recorded route are shown as
`unrouted`.

The topology only counts rows that still belong to the current topology: the
route must exist, the node must be referenced by that route, and the node's
provider must match. Stale historical nodes do not show old traffic.

## Admin API

All `/api/*` routes except `/api/health`, `/api/login` and `/api/logout`
require a session cookie. `GET /api/health` returns `ok` and the build identity;
after a failed stats upgrade it also carries `statsMigrationError`, and that
field is absent when the migration is clean.

```text
GET  /api/health
POST /api/login
POST /api/logout

GET  /api/config
PUT  /api/config
GET  /api/presets

GET|POST      /api/providers
GET|PUT|DELETE /api/providers/{id}
POST          /api/providers/{id}/test
GET           /api/providers/{id}/models

GET|POST      /api/nodes
GET|PUT|DELETE /api/nodes/{id}
GET|POST      /api/routes
GET           /api/routes/health
PUT|DELETE    /api/routes/{id}
GET|PUT       /api/entrypoints

GET|POST      /api/gateway-keys
PUT|DELETE    /api/gateway-keys/{id}
POST          /api/gateway-keys/{id}/rotate
GET           /api/gateway-key-usage

GET  /api/stats/summary
PUT  /api/stats/capture
GET  /api/stats/errors
GET  /api/stats/graph
GET  /api/stats/series
GET  /api/events                      (WebSocket)
GET  /api/alerts
GET|PUT /api/topology/positions
GET  /api/requests
GET  /api/requests/{id}
GET  /api/attempts
GET  /api/provider-usage
GET|POST      /api/captures
DELETE        /api/captures/{id}
PUT  /api/admin/password
```

`GET /api/requests` accepts `errors=1`, `captured=1`, `errorClass=<class>`,
`provider=<id>`, `route=<id>`, `node=<id>`, `entrypoint=<id>` and `key=<id>`
(exact gateway key id). Request rows store ids and add resolved
`providerName`/`nodeName`/`routeName`/`gatewayKeyName` labels (the current config
wins over the last stored label; an unknown entity falls back to its id); the
Sessions list and detail show those names. Request detail includes an optional
`capture` object and an optional `attempts` array of the discarded upstream
attempts of that request (absent when there were none); the list only reports a
cheap `hasCapture` flag and never carries `attempts`.
`GET /api/stats/series` returns the session trend for a window as
`{"bucket":"<duration>","points":[{"bucket":"<RFC3339>","requests":n,"errors":m}]}`.
It accepts `from`/`to`, `bucket=<duration>` (default `1h`, clamped to `1s..24h`,
`400` when unparsable) and exactly the same filters as `GET /api/requests`, so
the Sessions chart and its list can never disagree. Only non-empty buckets are
returned; the console pads the gaps so a quiet bucket renders at zero height.

`GET /api/attempts` lists the discarded attempts of the window newest first with
`from`/`to` plus the `route=<id>`, `node=<id>`, `provider=<id>`,
`errorClass=<class>`, `status=<code>`, `limit` and `offset` filters. An attempt
adds `providerName`/`nodeName`/`routeName` labels, keeps its parent's `ts`, and
is never a request row of its own: one client request stays one row in
`GET /api/requests`. `status` filters only on a positive value, because an
attempt that failed before a response stores `0`.
`GET /api/stats/errors` aggregates the window by `error_class`, provider, route
and client model, and returns top message fingerprints plus hourly buckets.
`GET /api/alerts` evaluates the last 15 minutes for auth failures, an error-rate
spike, gateway timeouts and excessive stream truncation; alerts are UI-only in
P1.

`GET /api/gateway-keys` lists `{id,name,keyPreview,enabled}` in configuration
order and returns `[]` when none are configured. `POST /api/gateway-keys` takes
`{name,enabled}`, generates a 256-bit `sk-gw-...` secret and returns `201` with
`{id,name,key}`. `PUT /api/gateway-keys/{id}` changes `enabled` and may rename
the label, keeping the id, and returns `204`; `DELETE` returns `204` and keeps
historical usage. `POST /api/gateway-keys/{id}/rotate` returns `200` with a new
`{id,name,key}`, and the old value stops working on the next request. Create and
rotate are the only responses that contain a raw key; create is the deliberate
exception to the empty `201` body. `GET /api/gateway-key-usage` accepts
`from`/`to` and returns range totals plus per-key requests, errors and tokens,
joined by key id. Configured keys appear in order including zero usage, deleted
keys are appended alphabetically with `configured:false`, and the endpoint
returns `503` when stats are disabled.

`GET /api/config` reports `gateway.readTimeout`, `gateway.userAgent`,
`gateway.keysSet` and `gateway.authRequired` but never key values.
`PUT /api/config` is a full replacement for the fields the view exposes:
write-only provider keys, `gateway.keys` and the admin password are preserved
when absent (provider keys per provider id), and `gateway.keys` is cleared only
when the request sends `[]`. Ids follow one adopt-or-mint rule for every entity:
an entry that carries an id keeps it, an id-less entry adopts the stored id of
the same trimmed name, and only a genuinely new entry gets a freshly minted id.
That means a provider removed and re-created with the same name in one
`PUT /api/config` (for example deleted and re-added in the console before
saving) inherits the old id, and therefore the old history. Deleting it with
`DELETE /api/providers/{id}` and creating it again mints a new id; use a new
name if you want a new identity on purpose. A `PUT /api/config` that carries a
provider-key id which is not configured on that provider is rejected with
`key id ... is not configured`, so a stale console snapshot cannot drop or
reassign keys. A rename is not inferred from a name change: send the id when you
rename through the endpoint, or the new name becomes a new entity. Fields the
view does not expose, such as provider `userAgent` and `headers`, are not
round-trippable through GET then PUT; send them explicitly with the update.

`GET /api/events` is a WebSocket subscription. It accepts `range=...`,
`since=<RFC3339>` to resume a session, or `reset=1` to start at zero. The server
pushes absolute `{type:"snapshot",since,to,metrics}` messages every second.
Reverse proxies must forward the WebSocket Upgrade for `/api/events` and
preserve the `Host` header; the server checks `Origin` against it. Vite's dev
proxy uses `ws: true`. The HTTP `GET /api/stats/graph` endpoint remains
available to API clients.

`DELETE /api/auth-failures` clears the in-memory gateway-key auth failure record
that feeds the Overview notification bell. It returns `204` and changes no
request statistics or configuration.

## Request correlation and capture

Every response that passes through a node hop carries
`X-Cuttle-Request-ID`. The value is the same `sid` stored as the request's
`requestId` in stats and logs, so a client report can be matched to one row.
Gateway-generated 502/503 responses (no entrypoint match, upstream unreachable
before headers) do not pass through the node header rewrite and are a known v1
gap, as are request-side failures that produce no response.

The Sessions page shows the structured rewriter fields (`inboundProtocol`,
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
`rewrite_error`, `client_error` or `client_disconnect`. `truncated` marks a
streaming request that ended without a terminal
`finish_reason`/`stop_reason` or `[DONE]`; it is how a clean-looking HTTP 200
cut is surfaced.

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

## Connection warming

Claude Code sends `HEAD /api/hello` and `GET /api/hello` to
`ANTHROPIC_BASE_URL` as a best-effort connection-warming probe. The gateway
answers both locally with HTTP 200 (`GET` returns `{"message":"hello"}`); the
probe is not forwarded upstream and is not recorded in stats or captures.

## Provider usage and balances

The Usage page shows one card per provider. The **Statistics** switch, off by
default, reveals local totals (requests, errors, input/output/cached tokens)
from hourly rollups; when it is on, a 24-hour, 7-day or 30-day range selector
appears. Token counts use k/M/B units. Upstream values are queried per API key:

- **DeepSeek**: account balance from `/user/balance`.
- **CommandCode**: credits and the 5-hour/weekly windows from
  `/alpha/billing/credits`.
- **OpenCode Go**: rolling, weekly and monthly windows from
  `/zen/go/v1/usage`, or `/inference/go/v1/usage` for providers created with
  the new inference base URLs.
- **OpenRouter**: per-key `limit`, `limit_remaining`, `limit_reset` and
  `usage` from `/api/v1/key`. A key without a limit shows its all-time spend;
  account-wide `/credits` requires a management key and is not queried.

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
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest

docker logs cuttle
```

The first start generates `/data/cuttle.yaml` and `/data/stats.sqlite`. The log
contains a one-time admin password; open `http://localhost:8788/`, log in, and
create the upstream topology. The image sets `CUTTLE_DATA=/data` (which keeps
the container on `/data`; the binary default is `<os.UserConfigDir>/cuttle`) and
`CUTTLE_ADMIN_LISTEN=:8788` for the first run. The binary default remains
`127.0.0.1:8788`.

To choose the password, set it before the first start:

```bash
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -e CUTTLE_ADMIN_PASSWORD='choose-a-long-password' \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest
```

The three config-value variables are first-run defaults; `CUTTLE_DATA` is the
data directory used on every start. An existing config is not overridden; edit
`admin.listen` in the file or use the console. To keep the admin API inside the
container network on a fresh container, set `CUTTLE_ADMIN_LISTEN=127.0.0.1:8788`
before the first start.

Existing configs can be mounted at `/etc/cuttle/cuttle.yaml`:

```bash
docker run -d --name cuttle \
  -p 8787:8787 -p 8788:8788 \
  -v "$PWD/cuttle.yaml:/etc/cuttle/cuttle.yaml:ro" \
  -v cuttle-data:/data \
  ghcr.io/ginuerzh/cuttle:latest
```

`/etc/cuttle/cuttle.yaml` is found when `/data/cuttle.yaml` does not exist; use
`serve -c /etc/cuttle/cuttle.yaml` as the container command to be explicit.
Admin edits write the config file, so mount it writable if you want to manage
the gateway from the console; with a read-only mount, edit the host file and
send `SIGHUP` instead. A read-only or hand-maintained file still gets stable
entity ids: the first start derives them, cannot write them back, and logs a
warning, so the ids are recomputed identically on every start. Keep the `id`
lines in such a file when renaming an entity, or the new name becomes a new
identity and its statistics split.

`/data` is the working directory, so generated files persist in the volume. The
container runs as UID/GID `10001`; a bind-mounted config must be readable by
that user and a bind-mounted `/data` must be writable by it. If the GHCR package
is private, run `docker login ghcr.io` with a token that can read packages
first.

Build the image from this directory; Go modules are downloaded from the module
proxy:

```bash
docker build -t cuttle .
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
internal/gateway        data plane: gateway-key auth, entrypoint matchers, routes, nodes
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
- Gateway keys are also stored in plaintext in the YAML config, like provider
  keys. Protect the file (`0600`) and inject keys through environment variables
  where possible. Raw gateway keys appear only in the create and rotate
  responses; list, config and console views expose masked previews only.
  Query-string credentials are not accepted, so keys do not leak into recorded
  URIs or reverse-proxy logs. Gateway keys never authenticate the admin API,
  and an admin session never authenticates the data plane.
- The generated config contains a bcrypt admin hash, not the plaintext
  password. The one-time password is printed to stderr/logs on the first run.
  In production, set `CUTTLE_ADMIN_PASSWORD` through your orchestrator's secret
  mechanism, or capture the first-run logs securely.
- A newly created data directory is `0700`; a pre-existing or bind-mounted
  directory is not changed, and startup warns if it is accessible by group or
  other. The config file is `0600`: every write, including the loader's
  migration write-back and an admin save, replaces it atomically and normalizes
  the permissions to `0600`.
- The admin API is HTTP only. The binary binds to loopback by default; the
  Docker image binds `:8788` for first-run onboarding. Put a TLS reverse proxy
  in front of it before exposing it on a network. The proxy must forward the
  WebSocket Upgrade for `/api/events` and preserve the `Host` header.
- `CUTTLE_ADMIN_PASSWORD` is visible in the container environment; use an env
  file or an orchestrator secret to inject it.
- `admin.passwordHash` empty disables the admin server entirely, including the
  management API.
