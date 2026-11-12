# Default data directory — design

Date: 2026-11-12
Repository: `ginuerzh/cuttle`
Status: proposed

## Intent

`CUTTLE_DATA` defaults to `/data` in every runtime. That path only makes sense
inside the Docker image; a binary run by a normal user either needs root or has
to set the variable on every start. wisper already solved this with
`os.UserConfigDir()/wisper`, and cuttle should follow the same rule.

Chosen approach (A): default to `<os.UserConfigDir>/cuttle`, falling back to the
working directory when the user config directory is unavailable. Pure switch —
no detection or migration of a legacy `/data` install.

## Goals

- With `CUTTLE_DATA` unset, resolve the data directory as
  `<os.UserConfigDir>/cuttle`: `~/.config/cuttle` on Linux (`$XDG_CONFIG_HOME`
  wins), `~/Library/Application Support/cuttle` on macOS,
  `%AppData%\cuttle` on Windows.
- When `os.UserConfigDir()` fails or returns empty (no `$HOME`, e.g. a systemd
  unit without `User=` env), fall back to the working directory and surface a
  warning instead of failing.
- An explicit `CUTTLE_DATA` keeps working exactly as today, including relative
  values (made absolute) and the `0700` permission warning.
- The Docker image keeps using `/data`, via its existing
  `ENV CUTTLE_DATA=/data`.

## Non-goals

- No container detection in Go: the image's env var is the whole Docker story.
- No legacy compatibility: an existing `/data/cuttle.yaml` is not detected and
  not migrated. Upgraded non-Docker installs must set `CUTTLE_DATA=/data` or move
  the directory; the README upgrade note says so.
- No split between config and data locations: `cuttle.yaml`, `stats.sqlite` and
  the session-reset marker stay in one directory.
- No change to the config search order (`$CUTTLE_DATA/cuttle.yaml`,
  `/etc/cuttle/cuttle.yaml`, `./cuttle.yaml`).
- No new environment variable or CLI flag.

## Anti-goals

- Never create or read a per-user directory when `CUTTLE_DATA` is set.
- Never write to `/data` unless the user asked for it.
- Never fail startup because the user config directory is unavailable; the
  working-directory fallback must keep first-run generation working.

## Design

### `internal/bootstrap`

- Replace `const DefaultDataDir = "/data"` with:

  ```go
  // DirName is the directory name used under the user config directory.
  const DirName = "cuttle"

  // Test seams.
  var (
      userConfigDir = os.UserConfigDir
      getwd         = os.Getwd
  )
  ```

- Add `DefaultDataDir() string`: `filepath.Join(userConfigDir(), DirName)` when
  the call succeeds with a non-empty value; otherwise the working directory
  (`getwd()`); otherwise `"."` (made absolute by `Resolve`). Trim the returned
  config dir before joining, mirroring wisper.
- `Resolve` uses `DefaultDataDir()` only when `CUTTLE_DATA` is empty, then
  `filepath.Abs` as today.
- `Paths` gains `DataDirFromFallback bool`, true when the default was used and
  the user config directory was unavailable. `Prepare` turns it into
  `Result.Warnings`: `CUTTLE_DATA is unset and the user config directory is
  unavailable; using <dir>`. Existing-config and first-run paths both report it.

### Docs

- `README.md`: the `CUTTLE_DATA` table row and the zero-config section show the
  new default; the upgrade note states that a non-Docker install that relied on
  the old `/data` default must set `CUTTLE_DATA` or move its data.
- `DOCKER.md`: note that `CUTTLE_DATA=/data` is now what keeps the image on
  `/data` (the binary default is the user config directory).
- `AGENTS.md`: update the invariants/gotchas that name `/data` as the binary
  default, and the `CUTTLE_DATA=./data` example stays valid.

### Tests

- `TestResolveDefaultDataDir`: override `userConfigDir` to a temp dir, expect
  `<temp>/cuttle` and `<temp>/cuttle/cuttle.yaml`.
- New fallback test: `userConfigDir` returns an error and `getwd` a temp dir →
  data dir is that temp dir and `Prepare` reports the warning.
- Every existing test that passes `CUTTLE_DATA` is unaffected; default-path tests
  use the seams rather than the real `$HOME`.

## Implementation calls

- Keep the binary's `-c` help text unchanged (`$CUTTLE_DATA/cuttle.yaml` is
  still where the config lives); the default value of the variable is documented
  instead.
- Warn, do not fail, on the working-directory fallback: a service without
  `$HOME` should still run.
- `DirName` stays lowercase `cuttle` to match the module and wisper's `wisper`.

## Verification

```bash
cd cuttle
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

No web bundle change, so the web gate is not required.
