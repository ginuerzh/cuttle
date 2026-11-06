# Zero-config First Run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `llm-gw serve` start with no YAML file, generate a first-run config and admin password under `$LLM_GW_DATA`, and let the operator create the entire upstream topology in the existing web console.

**Architecture:** A new `internal/bootstrap` package resolves the config location, loads an existing config, or generates a zero-topology config with a hashed admin password. `main.go` uses that result, prints the generated password once, clears persisted admin sessions for a freshly generated config, and keeps the existing admin/data-plane wiring unchanged. YAML remains the source of truth after first run; the four environment variables are first-run defaults, not runtime overrides.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, bcrypt, `crypto/rand`, SQLite, Docker (Alpine), existing React admin console.

**Spec:** [docs/superpowers/specs/2026-11-06-zero-config-design.md](../specs/2026-11-06-zero-config-design.md)

## Global Constraints

- Exactly four optional environment variables: `LLM_GW_DATA` (default `/data`), `LLM_GW_LISTEN` (default `:8787`), `LLM_GW_ADMIN_LISTEN` (binary default `127.0.0.1:8788`; Docker image default `:8788`), `LLM_GW_ADMIN_PASSWORD` (unset).
- The three config-value variables are **first-run defaults**, not runtime overrides. Once a config exists, the file and admin console are authoritative.
- Config resolution: explicit `-c` must exist; otherwise `$LLM_GW_DATA/llm-gw.yaml`, then `/etc/llm-gw/llm-gw.yaml`, then `./llm-gw.yaml`, then generate at `$LLM_GW_DATA/llm-gw.yaml`.
- Generated config: `$LLM_GW_DATA/llm-gw.yaml`; generated stats path: absolute `$LLM_GW_DATA/stats.sqlite`.
- Generated password: 24 random bytes, unpadded base64url (32 chars), bcrypt-hashed into the config, printed once to stderr immediately after `config.Write` succeeds.
- Existing config with an empty `admin.passwordHash` stays admin-disabled; auto-generation only happens when a new config is created.
- A newly generated config clears persisted admin sessions with the existing `stats.Store.DeleteAllSessions`.
- `config.Load` order stays decode -> `expandEnv` -> `applyDefaults` -> `Validate`; `config.Write` stays atomic (`0600`, temp file, `fsync`, rename).
- `admin.passwordHash` must not be added to `expandEnv`; bcrypt hashes contain `$`.
- Do not add provider/model environment variables.
- Docker image keeps UID/GID `10001`, `/data` as `WORKDIR`/volume, and changes `CMD` to `["serve"]`.

## Review Focus

1. **Docker upgrade with an existing mounted config:** the image's `LLM_GW_ADMIN_LISTEN=:8788` must not override an existing config's `admin.listen`; only a differing value logs a warning. Pinned by `TestPrepareExistingConfigWarnings` in Task 2 and the legacy-mount Docker check in Task 5.
2. **First-run password lifecycle:** the password is printed after the config is written, only once; a restart must not generate or print a new one. Pinned by `TestPrepareGeneratesConfig`, `TestPrepareRestart`, `TestPrepareUnwritableDataDir`, and `TestPreparePrintsPasswordBeforeLoadFailure` in Task 2.
3. **Config reset with retained `stats.sqlite`:** deleting the config must not leave old remember-me sessions valid. Pinned by `TestResetAdminSessions` in Task 3 and the stale-cookie case in Task 4.
4. **Unwritable or partial data directory:** first-run generation must fail clearly without printing a password or leaving a half-created config; an existing config with an unwritable stats path must keep the existing stats error. Pinned by `TestPrepareUnwritableDataDir` and `TestPrepareExistingStatsError` in Task 2.
5. **Empty-topology onboarding:** the gateway must start with zero entrypoints, fail requests cleanly before configuration, and serve the first request after the admin console creates provider/node/route/entrypoint. Pinned by `TestZeroConfigOnboarding` in Task 4.

---

### Task 1: Bootstrap path resolution

**Files:**
- Create: `internal/bootstrap/bootstrap.go`
- Test: `internal/bootstrap/bootstrap_test.go`

**Interfaces:**
- Produces:
  ```go
  const DefaultDataDir = "/data"
  const ConfigFileName = "llm-gw.yaml"
  var DefaultLegacyPaths []string // {"/etc/llm-gw/llm-gw.yaml", "llm-gw.yaml"}

  type Paths struct {
      DataDir    string // absolute
      ConfigPath string // absolute for data-dir config; as supplied/found otherwise
      Existing   bool
  }

  func Resolve(explicitPath string, getenv func(string) string, legacyPaths []string) (Paths, error)
  ```
- `legacyPaths == nil` uses `DefaultLegacyPaths`; an empty non-nil slice means no legacy candidates.
- `getenv` must be non-nil; `Prepare` normalizes nil to `os.Getenv` in Task 2.
- Explicit path exists -> `Existing: true`, `ConfigPath` unchanged.
- Explicit path missing -> error naming the path.
- Data-dir config exists -> wins over legacy.
- Legacy config exists -> `Existing: true`, `ConfigPath` as found.
- Nothing exists -> `Existing: false`, `ConfigPath = filepath.Join(absDataDir, "llm-gw.yaml")`.
- Non-`os.ErrNotExist` errors while checking the data-dir config must be wrapped with `LLM_GW_DATA <dir>` so the operator can see which setting is wrong. Legacy-path errors keep the path in the message.

- [ ] **Step 1: Write the failing tests**

Add `internal/bootstrap/bootstrap_test.go` with a `envMap(map[string]string) func(string) string` helper and these cases:

```go
func TestResolveExplicitPathMustExist(t *testing.T) {
    _, err := Resolve(filepath.Join(t.TempDir(), "missing.yaml"), envMap(nil), nil)
    if err == nil {
        t.Fatal("want error for missing explicit config")
    }
}

func TestResolvePrecedence(t *testing.T) {
    // Cases:
    // 1. explicit config exists, data config exists, legacy exists -> explicit.
    // 2. no explicit, data config exists, legacy exists -> data config.
    // 3. no explicit, data config missing, legacy exists -> legacy.
    // 4. nothing exists -> dataDir/llm-gw.yaml, Existing false.
}

func TestResolveDefaultDataDir(t *testing.T) {
    paths, err := Resolve("", envMap(nil), []string{})
    if err != nil {
        t.Fatal(err)
    }
    if paths.DataDir != "/data" || paths.ConfigPath != "/data/llm-gw.yaml" {
        t.Fatalf("paths = %+v", paths)
    }
}
```

Use `t.TempDir()` for every candidate; write a minimal `{}` YAML file for existence. Assert `DataDir` is absolute.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: FAIL because `Resolve`, `Paths`, and constants are undefined.

- [ ] **Step 3: Implement `Resolve` and helpers**

Use `os.Stat`; treat `errors.Is(err, os.ErrNotExist)` as "not found" and return other errors. Resolve `LLM_GW_DATA` with `strings.TrimSpace(getenv("LLM_GW_DATA"))`, default `DefaultDataDir`, then `filepath.Abs`. Do not create directories in `Resolve`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "feat(bootstrap): resolve zero-config file locations"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repository + commit: llm-gw / `2d317976` (code, tests and plan); this record commit is the follow-up
- Files actually changed: `internal/bootstrap/bootstrap.go`, `internal/bootstrap/bootstrap_test.go`, this plan
- Tests/commands and results: `GOWORK=off go test ./internal/bootstrap -count=1` -> `ok github.com/go-gost/llm-gw/internal/bootstrap`
- Review findings and fixes: none; self-review confirmed explicit/data/legacy precedence, absolute data dir, and `LLM_GW_DATA` error naming
- Deferred items/limitations: explicit path may point at a directory; `config.Load` reports that in Task 2

---

### Task 2: First-run config generation and environment defaults

**Files:**
- Modify: `internal/bootstrap/bootstrap.go`
- Test: `internal/bootstrap/bootstrap_test.go`

**Interfaces:**
- Consumes: `Resolve`, `config.Load`, `config.Write`, `config.ApplyDefaults`, `config.Validate`, `config.HashPassword`, `config.VerifyPassword`.
- Produces:
  ```go
  type Options struct {
      ExplicitPath string
      LegacyPaths  []string // nil -> DefaultLegacyPaths
      Getenv       func(string) string // nil -> os.Getenv
      Stderr       io.Writer // nil -> os.Stderr
  }

  type Result struct {
      Config            *config.Config
      ConfigPath        string
      Generated         bool
      GeneratedPassword string // tests only; never log this field
      Warnings          []string
  }

  func Prepare(opts Options) (*Result, error)
  func GeneratePassword() (string, error) // 24 random bytes -> 32-char RawURLEncoding
  ```
- On generation: `os.MkdirAll(paths.DataDir, 0o700)`, build a zero-topology config, set listens, hash the env password or a generated password, set absolute stats path, `config.ApplyDefaults`, `config.Validate`, `config.Write`, print the generated password, then `config.Load` the written file and return it.
- Test seam: `var loadConfig = config.Load` in `internal/bootstrap`; tests may replace it with a failing function and restore it. Production code must call `loadConfig`, not `config.Load` directly, in `Prepare`.
- Print exact lines:
  ```text
  llm-gw: generated admin password: <password>
  llm-gw: admin console listening on <admin.listen>
  llm-gw: set LLM_GW_ADMIN_PASSWORD to choose your own password
  ```
- On existing config: return `Generated: false`, no password print, and warnings for ignored env values that differ from the loaded config. A matching value is silent.
- Password validation: 8..72 bytes inclusive; clear error outside that range. Empty `LLM_GW_ADMIN_PASSWORD` means unset.

- [ ] **Step 1: Write the failing tests**

Add tests with these names and assertions:

- `TestPrepareGeneratesConfig`: `Options{Getenv: envMap(map[string]string{"LLM_GW_DATA": filepath.Join(t.TempDir(), "nested", "data")}), LegacyPaths: []string{}, Stderr: &buf}`. Assert `Generated == true`, the data dir was created with mode `0700`, `GeneratedPassword` has length 32, `buf` contains the password, `config.VerifyPassword(res.Config.Admin.PasswordHash, res.GeneratedPassword)` is true, `res.Config.Stats.Path == filepath.Join(dataDir, "stats.sqlite")`, `res.Config.Gateway.Listen == ":8787"`, `res.Config.Admin.Listen == "127.0.0.1:8788"`, all topology lists are empty, file mode is `0600`, and `config.Load(res.ConfigPath)` succeeds.
- `TestPrepareUsesEnvDefaults`: set `LLM_GW_LISTEN=127.0.0.1:18080`, `LLM_GW_ADMIN_LISTEN=127.0.0.1:18081`, `LLM_GW_ADMIN_PASSWORD=correct-horse`. Assert the generated config uses those values, `GeneratedPassword == ""`, `buf.Len() == 0`, and the hash verifies the env password.
- `TestPrepareRejectsShortPassword`: env password `short`; expect an error and no config file.
- `TestPrepareRestart`: call `Prepare` twice with the same data dir; reset `buf` before the second call; second result has `Generated == false`, `GeneratedPassword == ""`, empty `buf`, same password hash.
- `TestPrepareExistingConfigWarnings`: write an existing config with `config.Write` and a password hash. With differing env values, assert three warnings containing `LLM_GW_LISTEN`, `LLM_GW_ADMIN_LISTEN`, and `LLM_GW_ADMIN_PASSWORD`; with matching values, assert no warnings. Also assert the loaded config values did not change.
- `TestPrepareExistingEmptyHash`: config with `admin.passwordHash: ""` plus env password -> warning; admin remains disabled.
- `TestPrepareUnwritableDataDir`: set `LLM_GW_DATA` to a path under a regular file; expect an error containing `LLM_GW_DATA` and no password output.
- `TestPrepareExistingStatsError`: write an existing config with `stats.path` under an unwritable location (e.g. a child of a regular file); `Prepare` itself succeeds (it does not open stats), and the later `stats.Open` failure is the existing error. This pins the boundary: bootstrap must not claim to fix stats paths.
- `TestGeneratePassword`: length 32 and `base64.RawURLEncoding.DecodeString` succeeds.
- `TestPreparePrintsPasswordBeforeLoadFailure`: replace `loadConfig` with a function that returns an error, call `Prepare` with a fresh data dir and a stderr buffer, and assert the error is returned, the buffer contains the generated password, and the config file exists. This pins "print after write, before load". Restore `loadConfig` with `defer`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: FAIL because `Prepare`, `Options`, `Result`, and `GeneratePassword` are undefined.

- [ ] **Step 3: Implement `Prepare` and helpers**

Keep the package free of logging. Use `fmt.Fprintf` to `opts.Stderr` for the three password lines. Use `config.ApplyDefaults` before `config.Validate`. Do not add `Admin.PasswordHash` to `config.expandEnv`. Warnings are plain strings; `main` logs them after the logger exists.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "feat(bootstrap): generate zero-config first run"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repository + commit: llm-gw / `4a296c4a` (code, tests and plan); this record commit is the follow-up
- Files actually changed: `internal/bootstrap/bootstrap.go`, `internal/bootstrap/bootstrap_test.go`, this plan
- Tests/commands and results: `GOWORK=off go test ./internal/bootstrap -count=1` -> `ok github.com/go-gost/llm-gw/internal/bootstrap` (0.587s)
- Review findings and fixes: generation writes the config before printing, env values only affect a generated config, and ignored env values warn only when they differ from the loaded config; `loadConfig` is the only test seam
- Deferred items/limitations: none

---

### Task 3: Wire bootstrap into `main` and reset sessions

**Files:**
- Modify: `main.go`
- Test: `main_test.go`
- Test: `internal/stats/store_test.go`

**Interfaces:**
- Consumes: `bootstrap.Prepare`, `bootstrap.Options`, `bootstrap.Result`, `gateway.New`, `gateway.Gateway.Stats`, `stats.Store.DeleteAllSessions`.
- Produces:
  ```go
  // package main
  func resetAdminSessions(store *stats.Store, generated bool) error
  ```
  Returns nil when `!generated` or `store == nil`; otherwise calls `store.DeleteAllSessions(context.Background())`.
- `parseServeArgs` default changes from `"llm-gw.yaml"` to `""`. `""` means resolve through bootstrap.

- [ ] **Step 1: Write the failing tests**

Update `TestParseServeArgs`:
```go
got, err := parseServeArgs([]string{"serve"})
if err != nil { t.Fatal(err) }
if got != "" { t.Fatalf("default path = %q, want empty", got) }
```
Keep the explicit `-c` and `--config` cases.

Add `TestResetAdminSessions` in `main_test.go`:
```go
store, err := stats.Open(filepath.Join(t.TempDir(), "stats.sqlite"))
if err != nil { t.Fatal(err) }
defer store.Close()
ctx := context.Background()
if err := store.PutSession(ctx, "old", time.Now().Add(time.Hour)); err != nil { t.Fatal(err) }
if err := resetAdminSessions(store, false); err != nil { t.Fatal(err) }
if got := mustLoadSessions(t, store); len(got) != 1 { t.Fatalf("sessions = %d, want 1", len(got)) }
if err := resetAdminSessions(store, true); err != nil { t.Fatal(err) }
if got := mustLoadSessions(t, store); len(got) != 0 { t.Fatalf("sessions = %d, want 0", len(got)) }
if err := resetAdminSessions(nil, true); err != nil { t.Fatal(err) }
```
Add the small `mustLoadSessions` helper.

Add a focused `TestDeleteAllSessions` in `internal/stats/store_test.go` if the store package does not already cover it: open a temp store, `PutSession`, `DeleteAllSessions`, assert `LoadSessions` is empty.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test . ./internal/stats -count=1`
Expected: FAIL because the default path is still `llm-gw.yaml` and `resetAdminSessions` is undefined.

- [ ] **Step 3: Implement the main wiring**

In `run`:
1. `configPath, err := parseServeArgs(args)`; `configPath` may be empty.
2. `res, err := bootstrap.Prepare(bootstrap.Options{ExplicitPath: configPath, Getenv: os.Getenv, Stderr: os.Stderr})`.
3. `log, err := logging.New(res.Config.Log.Level, res.Config.Log.Format)`; `log.SetDefault()`.
4. Log `res.Warnings` with `log.Warnf("%s", w)`.
5. If `res.Generated`, log `generated config %s; admin password printed to stderr` when `res.GeneratedPassword != ""`, otherwise log `generated config %s with the password from LLM_GW_ADMIN_PASSWORD`. Never log the password itself.
6. If `len(res.Config.Entrypoints) == 0`, log the no-entrypoints warning. If `res.Config.Admin.PasswordHash == ""`, say the admin console is disabled; otherwise say to use the admin console.
7. `gw, err := gateway.New(res.Config, log)`; set `defer gw.Close()` immediately after the successful call.
8. `if err := resetAdminSessions(gw.Stats(), res.Generated); err != nil { return fmt.Errorf("reset admin sessions: %w", err) }`.
9. `if err := gw.Start(); err != nil { return err }`.
10. `adm = admin.New(res.ConfigPath, res.Config, gw, log)`.
11. On `SIGHUP`, `config.Load(res.ConfigPath)` and `adm.SetConfig(newCfg)`.

Do not print `res.GeneratedPassword` in `main`; `bootstrap.Prepare` already printed it.

- [ ] **Step 4: Run the tests and build**

Run:
```bash
GOWORK=off go test . ./internal/stats -count=1
GOWORK=off go build ./...
```
Expected: PASS and build succeeds. If `internal/admin/dist` is absent (fresh checkout), run `(cd web && npm ci && npm run build)` first; `go:embed all:dist` requires it.

- [ ] **Step 5: Smoke-test the fresh-start path**

```bash
tmp=$(mktemp -d)
GOWORK=off go build -o /tmp/llm-gw-zero .
LLM_GW_DATA="$tmp" LLM_GW_LISTEN=127.0.0.1:0 LLM_GW_ADMIN_LISTEN=127.0.0.1:0 timeout 2s /tmp/llm-gw-zero serve 2>&1 | tee /tmp/llm-gw-zero.log || true
grep -q "generated admin password" /tmp/llm-gw-zero.log
test -f "$tmp/llm-gw.yaml"
test -f "$tmp/stats.sqlite"
```
Expected: password line present, both files exist.

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go internal/stats/store_test.go docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "feat: start without a config file"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repository + commit: llm-gw / `f5318e36` (`feat: start without a config file`); this record commit is the follow-up
- Files actually changed: `main.go`, `main_test.go`, `internal/stats/store_test.go`, this plan
- Tests/commands and results: `GOWORK=/tmp/llm-gw-work/go.work go test . ./internal/stats -count=1` -> ok; `GOWORK=/tmp/llm-gw-work/go.work go test ./... -count=1` -> all packages ok; smoke run with a temp `LLM_GW_DATA` printed one generated password and created `llm-gw.yaml` (0600) and `stats.sqlite`
- Review findings and fixes: `defer gw.Close` is installed before session reset; `resetAdminSessions` only runs for a generated config and clears persisted sessions before `admin.New`; `parseServeArgs` defaults to empty so bootstrap resolves the file; warnings are logged after the logger exists
- Deferred items/limitations: verification used a detached `llm-api-converter` worktree at `7f18a6d` because the sibling working tree has uncommitted P1 changes that intermittently fail to compile; the Task 5 Docker context needs the same stable sibling or the working tree must be fixed

---

### Task 4: Zero-config onboarding end-to-end

**Files:**
- Create: `internal/gateway/zero_config_e2e_test.go`

**Interfaces:**
- Consumes: `bootstrap.Prepare`, `bootstrap.Options`, `gateway.New`, `admin.New`, `stats.Open`, `stats.Store.PutSession`, `config.VerifyPassword`.
- Produces: `TestZeroConfigOnboarding`, a package `gateway_test` test that follows the same startup order as `main`.

- [ ] **Step 1: Write the failing/verification test**

The test must:

1. Create a temp data dir and pre-populate `stats.sqlite` with a stale remembered session:
   ```go
   staleToken := "stale-session-token"
   sum := sha256.Sum256([]byte(staleToken))
   staleHash := hex.EncodeToString(sum[:])
   store, _ := stats.Open(filepath.Join(dataDir, "stats.sqlite"))
   _ = store.PutSession(context.Background(), staleHash, time.Now().Add(time.Hour))
   store.Close()
   ```
2. `res, err := bootstrap.Prepare(bootstrap.Options{Getenv: envMap(map[string]string{
       "LLM_GW_DATA": dataDir,
       "LLM_GW_LISTEN": "127.0.0.1:0",
       "LLM_GW_ADMIN_LISTEN": "127.0.0.1:0",
   }), LegacyPaths: []string{}, Stderr: &buf})`.
3. Start `gateway.New(res.Config, xlogger.Nop())`, call `gw.Stats().DeleteAllSessions(context.Background())` because `res.Generated` is true, then `gw.Start()`. Keep the same order as `main`.
4. Start `admin.New(res.ConfigPath, res.Config, gw, xlogger.Nop())` behind `httptest.NewServer`.
5. `GET /api/config` with `Cookie: llm_gw_session=<staleToken>`; expect `401`.
6. Before creating an entrypoint, send a POST to `http://<gw.Addr()>/v1/chat/completions` with an HTTP client that has a short timeout, and assert it does not succeed (`err != nil` or status >= 500). This pins the empty-topology behavior.
7. Log in with `res.GeneratedPassword`; use a cookie jar and the `llm_gw_csrf` cookie exactly like `admin_e2e_test.go`.
8. Create provider (`generic`, `chat: upstream.URL`, `keys: ["sk-zero"]`), node (`model: "m1"`), route, and PUT the entrypoint `[{"name":"default","match":["*"],"route":"r"}]`.
9. Send a request to the gateway; assert the fake upstream saw `/chat/completions` and `Authorization: Bearer sk-zero`.
10. Restart: close the gateway and admin test server; `buf.Reset()`; `res2, _ := bootstrap.Prepare` with the same options. Assert `Generated == false`, `GeneratedPassword == ""`, `buf.Len() == 0`, and `len(res2.Config.Providers) == 1`. Start a second gateway from `res2.Config`, send the request again, and assert the upstream was hit.

- [ ] **Step 2: Run the test**

Run: `GOWORK=off go test ./internal/gateway -run TestZeroConfigOnboarding -count=1 -v`
Expected: PASS. If it fails, fix the wiring (not the test assertions).

- [ ] **Step 3: Run the gateway package**

Run: `GOWORK=off go test ./internal/gateway -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/zero_config_e2e_test.go docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "test(gateway): cover zero-config onboarding"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repository + commit: llm-gw / `c7b2a504` (`test(gateway): cover zero-config onboarding`); this record commit is the follow-up
- Files actually changed: `internal/gateway/zero_config_e2e_test.go`, this plan
- Tests/commands and results: `GOWORK=/tmp/llm-gw-work/go.work go test ./internal/gateway -run TestZeroConfigOnboarding -count=1 -v` -> PASS (0.31s); `GOWORK=/tmp/llm-gw-work/go.work go test ./internal/gateway -count=1` -> ok
- Review findings and fixes: added deferred `Close` guards so a failure before the restart does not leak the gateway or admin test server; the stale remembered session is rejected only because the generated-config path clears it; pre-config requests fail without a panic
- Deferred items/limitations: verification uses the detached `llm-api-converter` worktree from the Task 3 ruling

---

### Task 5: Docker image and documentation

**Files:**
- Modify: `Dockerfile`
- Modify: `DOCKER.md`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `example.yaml`

**Interfaces:**
- Docker image environment: `LLM_GW_DATA=/data`, `LLM_GW_ADMIN_LISTEN=:8788`.
- `CMD ["serve"]`; keep `ENTRYPOINT ["llm-gw"]`, `WORKDIR /data`, `VOLUME ["/data"]`, UID/GID `10001`.
- The legacy `/etc/llm-gw/llm-gw.yaml` mount remains supported through `bootstrap.Resolve`.

- [ ] **Step 1: Update the Dockerfile**

Add:
```dockerfile
ENV LLM_GW_DATA=/data \
    LLM_GW_ADMIN_LISTEN=:8788
```
Keep the `chown -R llm-gw:llm-gw /data`; add `chmod 700 /data`. Replace the final `CMD` with:
```dockerfile
CMD ["serve"]
```
Update the bottom comments to describe zero-config, the generated password, and the legacy mount.

- [ ] **Step 2: Update the docs**

`README.md`:
- Add a "Zero-config quick start" section: `docker run` with only ports + volume, `docker logs` for the password, console URL, and the four env vars.
- Add an env-var table and the resolution order.
- Document that the generated config is `$LLM_GW_DATA/llm-gw.yaml`, stats is `$LLM_GW_DATA/stats.sqlite`.
- Document password reset: set `LLM_GW_ADMIN_PASSWORD` **before first run**, or use `hash-password` + edit the file, or delete the config (destructive).
- Document that env vars are first-run defaults and are ignored for an existing config.
- Update the Docker and security sections.

`DOCKER.md`:
- Replace the mount-first run example with the zero-config example.
- Add an "Existing config mount" section showing `-c /etc/llm-gw/llm-gw.yaml` and the legacy fallback.
- Add the `docker logs` password step.

`AGENTS.md`:
- Update the `main.go` row to mention first-run bootstrap and session reset.
- Add configuration invariants: resolution order, four env vars, first-run-only semantics, generated password, empty-hash behavior, session clearing.
- Update the Docker gotchas for `CMD ["serve"]` and `/data`.

`example.yaml`:
- Add a short top comment that the file is optional with the zero-config flow.

- [ ] **Step 3: Run the Go and web gates**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit && npm run build
```

Expected: all pass. If `web/node_modules` is absent, run `npm ci` first. Rebuild the web bundle before the final Go build if the console changed; this task does not change web source.

Then start the built binary with a fresh `LLM_GW_DATA`, log in with the printed password, and open Providers, Nodes, Routes, Entrypoints and Topology. They must render empty lists without errors. Record whether this was browser-verified or only HTTP/TypeScript-verified.

- [ ] **Step 4: Build and smoke-test the Docker image**

From `/root/code/go-gost`:
```bash
docker build -f llm-gw/Dockerfile -t llm-gw-zero-config-test .
docker volume create llm-gw-zero-config-test-data
docker run -d --rm --name llm-gw-zero-config-test \
  -p 18787:8787 -p 18788:8788 \
  -v llm-gw-zero-config-test-data:/data \
  llm-gw-zero-config-test
sleep 2
docker logs llm-gw-zero-config-test 2>&1 | tee /tmp/llm-gw-docker.log
grep -q "generated admin password" /tmp/llm-gw-docker.log
for i in $(seq 1 20); do
  if curl -fsS http://127.0.0.1:18788/api/health >/dev/null; then break; fi
  sleep 0.5
done
curl -fsS http://127.0.0.1:18788/api/health
docker exec llm-gw-zero-config-test ls -l /data
docker rm -f llm-gw-zero-config-test || true
docker volume rm llm-gw-zero-config-test-data || true
```
Expected: build succeeds, password line appears, health returns 200, `/data/llm-gw.yaml` and `/data/stats.sqlite` exist. If the network blocks the build, retry with the `GOPROXY`/`GOSUMDB` build args documented in `DOCKER.md`; record the limitation if it still fails.

Legacy mount check (pins Review Focus 1):

```bash
legacy_dir=$(mktemp -d)
hash=$(printf 'legacy-pass' | /tmp/llm-gw-zero hash-password)
cat > "$legacy_dir/llm-gw.yaml" <<EOF
admin:
  listen: "127.0.0.1:8788"
  passwordHash: "$hash"
stats:
  path: "/data/legacy-stats.sqlite"
EOF
docker run -d --rm --name llm-gw-legacy-test \
  -v "$legacy_dir/llm-gw.yaml:/etc/llm-gw/llm-gw.yaml:ro" \
  llm-gw-zero-config-test
sleep 2
docker logs llm-gw-legacy-test 2>&1 | tee /tmp/llm-gw-legacy.log
grep -q "admin API listening on 127.0.0.1:8788" /tmp/llm-gw-legacy.log
if grep -q "generated admin password" /tmp/llm-gw-legacy.log; then
  echo "legacy config unexpectedly generated a password" >&2
  exit 1
fi
docker rm -f llm-gw-legacy-test || true
```

Expected: the container loads `/etc/llm-gw/llm-gw.yaml` through the legacy fallback, logs the loopback admin address, does not generate a password, and logs a warning that `LLM_GW_ADMIN_LISTEN=:8788` was ignored because it differs from the config. If `/tmp/llm-gw-zero` is absent, build it with `GOWORK=off go build -o /tmp/llm-gw-zero .`.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile DOCKER.md README.md AGENTS.md example.yaml docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "feat(docker): enable zero-config first run"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repository + commit: llm-gw / `00727917` (`feat(docker): enable zero-config first run`); this record commit is the follow-up
- Files actually changed: `Dockerfile`, `DOCKER.md`, `README.md`, `AGENTS.md`, `example.yaml`, this plan
- Tests/commands and results: `GOWORK=/tmp/llm-gw-work/go.work go build ./...` PASS; `go vet ./...` PASS; `go test ./... -count=1` all packages PASS; web `npm test` 86/86 PASS, `npx tsc --noEmit` PASS, `npm run build` PASS; Docker image built from a stable context with `--network host` and `GOPROXY=https://goproxy.cn,direct`; fresh-volume smoke printed one password, `/api/health` returned `{"ok":true}`, and `/data` contained `llm-gw.yaml` + `stats.sqlite`; legacy `/etc` mount kept `admin.listen=127.0.0.1:8788`, did not generate a password, and logged the ignored `LLM_GW_ADMIN_LISTEN` warning
- Review findings and fixes: the Docker build needed `--network host`/`GOPROXY` in this environment; the build context used a stable `llm-api-converter` archive at `7f18a6d` because the sibling working tree has uncommitted P1 changes. Documentation now states the first-run-only env semantics, the one-time password, and the legacy mount path.
- Deferred items/limitations: a Docker build from the live parent working tree will fail while `llm-api-converter` has uncommitted, non-compiling P1 changes; the image itself was verified from the stable sibling archive. The manual empty-state UI pass was not browser-verified; the e2e test covers the admin API flow.

---

### Task 6: Final verification and workspace memory

**Files:**
- Modify: `docs/superpowers/plans/2026-11-06-zero-config.md` (fill records)
- Create: `/root/code/go-gost/.memory/notes/llm-gw-zero-config.md`
- Modify: `/root/code/go-gost/.memory/MEMORY.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: a final verification record and a durable memory note.

- [ ] **Step 1: Run the full gates**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit && npm run build
```
Expected: all pass. Record exact results in the plan. If a gate fails, return to the owning task, fix it there, and record the fix; do not weaken assertions.

- [ ] **Step 2: Re-run the Docker smoke test**

Use the Task 5 commands for the fresh-volume smoke test and the legacy-mount check. Record the image tag, whether the password line appeared, whether the health check passed, and whether the legacy config kept its loopback admin listen.

- [ ] **Step 3: Write the workspace memory note**

Create `/root/code/go-gost/.memory/notes/llm-gw-zero-config.md`:
```markdown
---
title: llm-gw zero-config first run
type: note
tags: [llm-gw, config, docker]
---

# llm-gw zero-config first run

## Observations
- [decision] 2026-11-06: no config file is required; first run generates
  `$LLM_GW_DATA/llm-gw.yaml` and `$LLM_GW_DATA/stats.sqlite`.
- [decision] Only `LLM_GW_DATA`, `LLM_GW_LISTEN`, `LLM_GW_ADMIN_LISTEN` and
  `LLM_GW_ADMIN_PASSWORD` exist; they are first-run defaults, not runtime
  overrides.
- [convention] A generated admin password is printed once to stderr; a new
  config clears persisted sessions with `Store.DeleteAllSessions`.
- [gotcha] Existing configs keep `admin.passwordHash` semantics; an empty hash
  still disables the admin server.
- [gotcha] The Docker image sets `LLM_GW_ADMIN_LISTEN=:8788` for first run, but
  an existing mounted config is not overridden.

## Relations
- relates_to [[llm-gw-docker]]
- relates_to [[llm-gw-admin-api]]
```
Add a line under `## llm-gw` in `.memory/MEMORY.md`:
`- [[llm-gw-zero-config]] — 2026-11-06 无配置文件首次启动；4 个可选 env 仅作首次默认；生成 config/stats 到 LLM_GW_DATA；自动打印一次管理员密码；新配置清 session`

- [ ] **Step 4: Commit**

llm-gw repo:
```bash
git add docs/superpowers/plans/2026-11-06-zero-config.md
git commit -m "docs(plan): record zero-config verification"
```
Workspace repo:
```bash
cd /root/code/go-gost
git add .memory/MEMORY.md .memory/notes/llm-gw-zero-config.md
git commit -m "docs(memory): record llm-gw zero-config decision"
```

**Implementation record**
- Date/status:
- Repository + commit(s): llm-gw `<sha>`; workspace `<sha>`
- Files actually changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
