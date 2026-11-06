# Zero-config first run for llm-gw

Status: draft for approval
Date: 2026-11-06

## Problem

`llm-gw` currently refuses to start without a YAML configuration file:

- `main.go` defaults `-c` to `llm-gw.yaml` and `config.Load` calls `os.Open`
  directly, so a missing file is a fatal error.
- The admin server starts only when `admin.passwordHash` is non-empty. A new
  Docker user therefore has a chicken-and-egg problem: no config file means no
  admin console, and no admin console means no way to create providers, nodes,
  routes or entrypoints.
- The Docker image hard-codes
  `serve -c /etc/llm-gw/llm-gw.yaml`, so a config file must be mounted even
  though `/data` already exists as the persistent volume.
- `admin.passwordHash` is not expanded from the environment. The only current
  way to set a password is `llm-gw hash-password` followed by pasting the hash
  into YAML.

The goal is a Docker-first first run: start the container, read one generated
admin password from the logs, open the console, and create the upstream
configuration in the web UI.

## Intent

- A fresh `llm-gw serve` with no configuration file starts successfully.
- The first run creates `$LLM_GW_DATA/llm-gw.yaml` and uses
  `$LLM_GW_DATA/stats.sqlite` for statistics.
- If `LLM_GW_ADMIN_PASSWORD` is not set and no password hash exists yet, a
  random admin password is generated and printed once to the terminal.
- Providers, API keys, models, nodes, routes and entrypoints are created later
  in the existing web console. No provider bootstrap variables are added.
- Only four optional environment variables are introduced:
  `LLM_GW_DATA`, `LLM_GW_LISTEN`, `LLM_GW_ADMIN_LISTEN` and
  `LLM_GW_ADMIN_PASSWORD`.
- The three config-value variables are **first-run defaults**. Once a config
  file exists, the file and the admin console are the source of truth. They are
  not runtime overrides. `LLM_GW_DATA` is the data-directory path used to locate
  or generate the config.

## Non-goals

- Bootstrapping providers, keys or models from environment variables.
- Representing a full routing topology in environment variables.
- Runtime environment overrides of an existing config. That would require a
  separate "effective config vs. file config" layer, which is deliberately out
  of scope.
- A new onboarding wizard in the web UI. The existing CRUD pages are the
  onboarding flow; only empty-state and end-to-end behavior are verified.
- TLS termination, multi-user admin accounts, or login rate limiting.
- Changing the data-plane routing model or the stats schema.

## Anti-goals

- Never overwrite an existing configuration file silently.
- Never generate a new password on every restart when a config already exists.
- Never change the meaning of an existing `admin.passwordHash`, including the
  documented "empty disables the admin server" behavior.
- Never let an admin console save bake first-run environment values into the
  file. First-run values are written once at generation; after that, the
  operator's file is authoritative.
- Never make an explicitly supplied `-c` path silently optional; a typo must
  still fail.
- Never log `LLM_GW_ADMIN_PASSWORD` or its hash. The generated password is
  printed once, only when it is actually generated.
- Do not add provider/model environment variables in this change.

## Constraints

- `config.Load` uses strict YAML decoding (`KnownFields(true)`), expands
  environment placeholders in selected fields, applies defaults, then validates.
- `config.Write` must remain atomic (`0600`, temp file, `fsync`, rename).
- The admin server starts only when `admin.passwordHash` is non-empty.
- Bcrypt hashes contain `$`, so `admin.passwordHash` must not be passed through
  the existing `os.Expand`-based field expansion.
- `stats.Open` receives `cfg.Stats.Path`; relative paths are resolved against
  the process working directory.
- The Docker image runs as UID/GID `10001`, uses `/data` as the working
  directory, and declares `/data` as a volume.

## Approaches considered

**A. Provider bootstrap environment variables plus generated config.**
Rejected for this change: it adds a second configuration surface (provider
type, key, model, base URL) even though the existing web console can create
those objects.

**B. Full topology from environment variables or inline YAML.**
Rejected: multi-line YAML and arrays are poor environment-variable citizens,
strict-schema errors are hard to diagnose in a shell, and the admin write-back
path still needs a file or an explicit read-only mode.

**C. Empty configuration plus admin-console onboarding.**
Chosen. The gateway starts with no upstreams, the admin console is enabled by a
generated password, and the operator creates the topology in the existing UI.
This matches the requested Docker workflow and keeps YAML as the source of
truth after first run.

## Design

### 1. Configuration resolution

`serve` keeps the `-c`/`--config` flag. Its default becomes empty rather than
`llm-gw.yaml`.

When `-c` is explicitly supplied:

1. The file must exist and load. A missing explicit path is an error.

When `-c` is not supplied, resolve in this order:

1. `$LLM_GW_DATA/llm-gw.yaml`
2. `/etc/llm-gw/llm-gw.yaml` (legacy Docker mount)
3. `./llm-gw.yaml` (legacy working-directory config)
4. Generate a new config at `$LLM_GW_DATA/llm-gw.yaml`

The legacy candidates preserve existing Docker and bare-metal deployments.
`$LLM_GW_DATA/llm-gw.yaml` wins when both the new and old locations exist.
The resolved path is passed to `admin.New`, so console edits continue to write
to the file that was actually loaded.

`LLM_GW_DATA` defaults to `/data`. The directory is created with mode `0700`
when it does not exist. If it cannot be created or is not writable and a new
config must be generated, startup fails with an error that names
`LLM_GW_DATA`. If an existing config was loaded, startup does not fail merely
because generation was skipped, but the stats database still needs a writable
path; a read-only stats path fails as it does today.

### 2. Environment variables

All four variables are optional.

| Variable | Role | Default |
| --- | --- | --- |
| `LLM_GW_DATA` | Data directory used to locate or generate the config and stats database | `/data` |
| `LLM_GW_LISTEN` | Default `gateway.listen` when a config is generated | `:8787` |
| `LLM_GW_ADMIN_LISTEN` | Default `admin.listen` when a config is generated | `127.0.0.1:8788` in the binary; the Docker image sets `:8788` for first run |
| `LLM_GW_ADMIN_PASSWORD` | Admin password hashed into a generated config | unset; a random password is generated |

Rules:

- `LLM_GW_DATA` is always used for path resolution. It determines where an
  existing config is looked up and where a missing config is generated. It does
  not rewrite `stats.path` in an existing config. Generated configs write an
  absolute `stats.path`; hand-written configs should do the same or run with
  the data directory as the working directory.
- `LLM_GW_LISTEN` and `LLM_GW_ADMIN_LISTEN` are used only when generating a new
  config. If a config file is loaded, they are ignored; the file and the admin
  Settings page are authoritative. When they are set but ignored, startup logs
  a warning so the behavior is visible.
- `LLM_GW_ADMIN_PASSWORD` is used only when generating a new config. If a config
  file is loaded, it is ignored; startup logs a warning. An empty value is
  treated as unset. Existing configs keep their `admin.passwordHash` semantics,
  including "empty disables the admin server". To change an existing password,
  use the admin console or `llm-gw hash-password` plus a file edit.
- When an existing config is loaded, a warning is emitted only when an ignored
  environment value differs from the loaded value. A matching value is silent.
  This keeps the Docker image's first-run default from producing a warning on
  every subsequent start: the generated config already contains `:8788`, so the
  image's `LLM_GW_ADMIN_LISTEN` matches it. The password is compared with
  `config.VerifyPassword` against the stored hash.
- A user-supplied password must be at least 8 characters, matching the admin
  password-change rule. Longer than bcrypt's 72-byte limit is rejected with a
  clear error.
- Because these values are written into the generated config once, an admin
  console save does not need to distinguish environment values from file
  values. There is no effective-config/file-config split in this change.

### 3. First-run generation

When no config file is found, build a default config:

- `gateway.listen`: `LLM_GW_LISTEN` or `:8787`
- `admin.listen`: `LLM_GW_ADMIN_LISTEN` or `127.0.0.1:8788`
- `admin.sessionTTL` and `admin.rememberTTL`: existing defaults
- `admin.passwordHash`: hash of `LLM_GW_ADMIN_PASSWORD` if set; otherwise hash
  of a generated password
- `stats.path`: absolute `$LLM_GW_DATA/stats.sqlite`
- `stats` retention and capture defaults
- `log.level: info`, `log.format: json`
- empty `entrypoints`, `providers`, `nodes` and `routes`

Call `Validate`, write the file with `config.Write` (`0600`), then load it (or
use the validated in-memory value). If the write fails, fail startup: starting
with an ephemeral password would change the password on every restart and the
admin console could not persist edits.

Password generation:

- 24 random bytes from `crypto/rand`, encoded as unpadded base64url (32
  characters). It satisfies the 8-character minimum and avoids characters that
  need shell quoting.
- The plaintext is printed once to stderr, never through the structured
  logger:

  ```text
  llm-gw: generated admin password: <password>
  llm-gw: admin console listening on <admin.listen>
  llm-gw: set LLM_GW_ADMIN_PASSWORD to choose your own password
  ```

- The password is printed only when a new config is generated and
  `LLM_GW_ADMIN_PASSWORD` is unset. A restart with the existing config prints
  nothing.
- If `LLM_GW_ADMIN_PASSWORD` is set, it is hashed into the generated config
  and nothing is printed.
- Print the generated password immediately after `config.Write` succeeds,
  before opening the stats store or starting the data plane. If a later startup
  step fails, the config still exists; printing early ensures the operator has
  the password for the next start instead of being locked out.

Session safety on a fresh config:

- Generating a new admin password must invalidate remembered sessions from a
  previous data directory. After the stats store is opened, call the existing
  `Store.DeleteAllSessions` before `admin.New` loads sessions. If the stats
  store is enabled and the clear fails, fail startup with a clear error rather
  than serving old sessions.
- This matters when an operator deletes `llm-gw.yaml` but keeps `stats.sqlite`.

Existing-config behavior:

- A config with a non-empty `admin.passwordHash` keeps it. No password is
  generated or printed.
- A config with an empty `admin.passwordHash` keeps the admin server disabled.
  This preserves the documented meaning of an empty hash. To enable the
  console, write a hash with `llm-gw hash-password`.
- Deleting the generated config is the supported way to start over; it also
  deletes the provider topology, so it is documented as destructive. The
  persisted sessions are cleared when the new config is generated.

### 4. Empty topology and onboarding

The gateway starts and binds its listener with zero entrypoints. Requests fail
with the existing `errNodeNotAvailable` path until the operator creates an
entrypoint. Startup logs a warning:

```text
llm-gw: no entrypoints configured; use the admin console to add a provider, node, route and entrypoint
```

The admin console is the onboarding path:

1. Log in with the printed or env-supplied password.
2. Create a provider, including its API base URLs and key.
3. Create a node bound to one provider model.
4. Create a route containing the node.
5. Create an entrypoint matching the desired client models and selecting the
   route.

Each create/update already goes through `replaceLocked`, which validates,
reloads the data plane, and writes the config file. No new admin API endpoints
are required.

The web console must be checked with empty lists. `GET` endpoints already
return `[]` and the client normalizes them, but the empty Providers, Nodes,
Routes, Entrypoints and Topology pages need a manual verification pass. A
small empty-state hint on the first page is optional, not required for the
first version.

### 5. Docker

Dockerfile changes:

- `ENV LLM_GW_DATA=/data`
- `ENV LLM_GW_ADMIN_LISTEN=:8788` so the published admin port works on a fresh
  container without the user setting a variable. This is a first-run default;
  an existing mounted config is not overridden. The binary default stays
  loopback-only.
- `CMD ["serve"]` so the default resolution above is used.
- Keep `WORKDIR /data`, `VOLUME ["/data"]`, and UID/GID `10001`.
- Tighten `/data` to mode `0700` where the image creates it.

The legacy `/etc/llm-gw/llm-gw.yaml` fallback means existing users who mount
that path but do not pass `-c` keep working after the image update. Users who
want to be explicit can still pass `-c /etc/llm-gw/llm-gw.yaml`.

Documented Docker quick start:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest

docker logs llm-gw
```

The logs contain the one-time admin password. To choose a fixed password:

```bash
docker run -d --name llm-gw \
  -p 8787:8787 -p 8788:8788 \
  -e LLM_GW_ADMIN_PASSWORD='choose-a-long-password' \
  -v llm-gw-data:/data \
  ghcr.io/ginuerzh/llm-gw:latest
```

To keep the admin API inside the container network on a fresh container, set
`LLM_GW_ADMIN_LISTEN=127.0.0.1:8788` explicitly. For an existing config, edit
`admin.listen` in the file or use the console; the environment variable is a
first-run default only.

### 6. Security

- The generated password is deliberately printed to stderr/logs. Production
  deployments should set `LLM_GW_ADMIN_PASSWORD` through the orchestrator's
  secret mechanism, or capture first-run logs securely.
- The admin API is HTTP. The binary default remains loopback-only; the Docker
  image exposes it on `:8788` for first-run onboarding. A TLS reverse proxy is
  still required before exposing it outside a trusted network.
- The data directory is `0700` and the generated config is `0600`. The stats
  database inherits the directory protection.
- `LLM_GW_ADMIN_PASSWORD` is visible in the container environment. This is an
  accepted tradeoff of the four-variable design; an env file or orchestrator
  secret can be used to inject it.
- The generated config contains the bcrypt hash, not the plaintext password.
  Provider keys later written by the admin console remain `0600` as today.
- A new config forces a new admin password and clears old remembered sessions;
  deleting the config without clearing `stats.sqlite` must not leave a bypass.

### 7. Backward compatibility

- `-c` remains supported and remains strict.
- Existing `./llm-gw.yaml` and `/etc/llm-gw/llm-gw.yaml` configurations are
  found through the legacy fallbacks.
- Existing configs keep their `stats.path`, their `admin.passwordHash`
  semantics, and their listener defaults. The new environment variables do not
  override an existing config.
- The new `LLM_GW_DATA` default `/data` only affects first-run generation. A
  bare-metal user without write access to `/data` must set
  `LLM_GW_DATA=./data` (documented).
- The Docker image's old default path is preserved by the `/etc` fallback, so
  an existing volume mount is not silently ignored.
- The Docker image's new `LLM_GW_ADMIN_LISTEN=:8788` is a first-run default and
  does not change the listen address of an existing mounted config.

## Testing

Go unit tests:

- Path resolution precedence: explicit `-c`, data-dir config, legacy `/etc`,
  legacy `./`, generate.
- Explicit missing `-c` fails.
- `LLM_GW_DATA` creates the directory and places both files there.
- First-run config round-trips through `config.Write`/`config.Load` with strict
  decoding.
- Generated password verifies against the stored bcrypt hash.
- A second start with the generated config does not print a new password and
  reuses the hash.
- `LLM_GW_ADMIN_PASSWORD` suppresses generation, hashes the supplied value, and
  is written into the generated config.
- An existing config ignores `LLM_GW_LISTEN`, `LLM_GW_ADMIN_LISTEN` and
  `LLM_GW_ADMIN_PASSWORD`; a differing value logs a warning, a matching value
  is silent.
- Existing config with an empty hash leaves admin disabled.
- Unwritable data directory fails first-run generation with a clear error.
- Existing config with an unwritable stats path still fails at `stats.Open`
  with the existing error.
- Generating a new config clears persisted remember-me sessions; a stale
  session cookie is rejected after restart.
- `SIGHUP` reloads the file and does not regenerate the config or re-apply
  first-run environment values.

Integration/e2e:

- Start the binary with a temporary `LLM_GW_DATA`, capture stderr, extract the
  generated password, log in to `/api/login`, create a provider/node/route/
  entrypoint against a local fake upstream, send a gateway request, and verify
  the response.
- Restart against the same data directory, verify no new password is printed,
  the topology is still loaded, and a second request succeeds.
- Docker smoke test: run the image with a named volume, read the password from
  `docker logs`, log in, and confirm `/data/llm-gw.yaml` and
  `/data/stats.sqlite` exist.

Web gate:

- `npm test`, `npx tsc --noEmit`, `npm run build`.
- Manual empty-state pass for Providers, Nodes, Routes, Entrypoints and
  Topology with a zero-topology config.

## Documentation

- README: zero-config quick start, the four env vars, first-run password,
  data-directory layout, password reset, and security notes.
- DOCKER.md: new `docker run` examples, named volume, legacy `/etc` mount
  compatibility, and how to read the generated password.
- AGENTS.md: update the configuration invariants for first-run generation,
  environment defaults, and session clearing.
- `example.yaml`: remains the full manual configuration; add a short comment
  that it is optional with the zero-config flow.

## Decisions made (check these)

- **Environment variables are first-run defaults, not runtime overrides.**
  Once a config exists, the file and console are authoritative. This avoids
  admin saves baking env values into YAML and avoids changing existing
  deployments on image upgrade.
- **Empty hash stays disabled for existing configs.** Auto-generation happens
  only when a new config is created. This preserves the current security
  behavior; a fresh Docker volume always gets a password.
- **Binary admin default stays loopback.** The Docker image sets
  `LLM_GW_ADMIN_LISTEN=:8788` for first run; bare-metal users opt in to remote
  admin access.
- **`LLM_GW_DATA` defaults to `/data`.** This is Docker-first; local users
  should set `LLM_GW_DATA=./data`.
- **Generated password goes to stderr, once.** It is not sent through the
  structured logger and is not printed on later starts.
- **A new config clears persisted admin sessions.** This closes the
  delete-config-but-keep-stats.sqlite bypass.
- **No provider/model env variables.** The web console is the only onboarding
  path for upstreams.

## Open questions

None that block planning. The decisions above are the ones to confirm during
design review. If runtime environment overrides of existing configs are wanted,
that is a separate design: it requires an effective-config/file-config split so
admin saves do not persist environment values.
