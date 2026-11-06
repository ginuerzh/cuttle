# Runtime-configurable log level

Date: 2026-11-06
Status: design proposed

## Intent

llm-gw currently creates `x/logger` with its default `info` level and JSON
format. Operators cannot raise or lower verbosity without rebuilding, and the
`llm-api-converter` debug output (global `slog`) never reaches the gateway log.
Add a `log` configuration block whose level can be changed at runtime.

## Goals

- Add `log.level` and `log.format` to the YAML config.
- Levels: `trace`, `debug`, `info`, `warn`, `error`, `fatal`.
- Formats: `json` (default) and `text`.
- Default level `info`; existing configs without a `log` block keep the current
  behavior.
- Apply a level change on `SIGHUP` and immediately after an admin config save,
  without restarting the gateway.
- Route `llm-api-converter`'s global `slog` output through the same handler and
  level, so `debug` also exposes converter diagnostics.
- Keep one line-oriented JSON/text stream on stderr.

## Non-goals

- Rotating files, log shipping, sampling or redaction.
- Changing the format at runtime. `log.format` is read at startup; a changed
  value is accepted and a warning says a restart is needed.
- Per-component levels or per-request log levels.
- Changing the admin UI beyond exposing the config value in `GET /api/config`.

## Approaches considered

### A. Startup-only with `x/logger` options

Pass `LevelOption` / `FormatOption` to `x/logger` from `main`. Smallest change,
but `x/logger` exposes no level setter, so runtime changes require rebuilding
the logger and the components that captured it. It also does not fix converter
visibility unless global `slog` is wired separately.

### B. llm-gw-owned logger backed by `log/slog` (chosen)

Add `internal/logging`, a small `core.Logger` implementation using a shared
`slog.LevelVar`:

- one `slog.LevelVar` used by the gateway logger and by `slog.SetDefault` for
  the converter;
- `SetLevel(string) error` updates the level in place and is safe from
  SIGHUP/admin goroutines;
- `WithFields` children share the same level and handler;
- JSON/text handler selected at startup;
- JSON levels lowercased and timestamps kept consistent with the current
  `x/logger` output.

Trade-off: about 150 lines plus tests, but no dependency on an `x/logger` API
change and no cross-repository pin update.

### C. Add `SetLevel` to `x/logger`

Smallest code change, best reuse. Rejected for this task: it makes llm-gw's
runtime level depend on a new `x` commit, which must be pushed and pinned in the
Docker workflow before the feature ships.

## Configuration

```yaml
log:
  level: info    # trace | debug | info | warn | error | fatal
  format: json   # json | text
```

- `applyDefaults`: empty level -> `info`, empty format -> `json`; trim and
  lowercase both.
- `expandEnv` applies to both fields, so `${LOG_LEVEL}` works.
- `Validate` rejects unknown levels/formats and names `log.level` or
  `log.format` in the error.
- `GET /api/config` includes `log`; `PUT /api/config` accepts it.

## Behavior

- Startup: `main` loads the config first, creates `internal/logging` with the
  configured level/format, then passes it as the gateway/admin logger and calls
  the logging package's `SetDefault` for global `slog`.
- `SIGHUP`: after a successful gateway reload, call `SetLevel(new level)`. If
  the configured format differs from the active format, log a warning that a
  restart is required.
- Admin save: after the config is validated, reloaded and written, apply the
  same level/format handling. The level change takes effect before the HTTP
  response returns.
- Invalid level/format is rejected before the config is written; a failed
  `SetLevel` is logged and does not roll back an otherwise valid config.
- `fatal` exits the process only when a `Fatal` call is made; it is a valid
  threshold, not a startup mode.

## Testing

- `internal/logging`: default filtering, each level threshold, live
  `SetLevel`, `WithFields` sharing the level, JSON vs text output, invalid level
  rejection.
- `internal/config`: defaults, lowercasing/trimming, env expansion, invalid
  level/format errors, `GET /api/config` shape.
- Admin: a config save with a different level invokes the setter (use a small
  test logger double).
- Manual: start with `level: info`, send an info+debug request, change to
  `debug` via SIGHUP, confirm debug lines and converter output appear without a
  restart.

## Acceptance criteria

- A config with no `log` block behaves exactly as today.
- `log.level: debug` shows debug logs from llm-gw and `llm-api-converter`.
- Changing `log.level` via SIGHUP or admin config save changes filtering
  immediately; no restart.
- Changing `log.format` takes effect after restart and emits a warning when
  changed at runtime.
- Invalid levels/formats fail config validation.
- All Go and web gates pass.
