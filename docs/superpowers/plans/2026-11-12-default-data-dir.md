# Default Data Directory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Default `CUTTLE_DATA` to `<os.UserConfigDir>/cuttle` (falling back to the working directory with a warning) instead of `/data`, matching wisper, while the Docker image keeps `/data` through its env var.

**Architecture:** Replace the `DefaultDataDir` constant in `internal/bootstrap` with a resolver that reads `os.UserConfigDir()`, exposed through test seams. `Resolve` uses it only when `CUTTLE_DATA` is empty and records when the working-directory fallback was used; `Prepare` turns that flag into a warning on both the existing-config and first-run paths.

**Tech Stack:** Go (cuttle module, `GOWORK=off`), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-12-default-data-dir-design.md`

## Global Constraints

- Run every Go command in `cuttle/` with `GOWORK=off`.
- Pure switch: never probe, read, copy or migrate a legacy `/data` install.
- No container detection in Go; the Docker image keeps `/data` via `ENV CUTTLE_DATA=/data`.
- No new environment variable, CLI flag, or change to the config search order (`$CUTTLE_DATA/cuttle.yaml`, `/etc/cuttle/cuttle.yaml`, `./cuttle.yaml`).
- An explicit `CUTTLE_DATA` must behave exactly as today, including `filepath.Abs` and the `0700` permission warning; the user config directory must not be consulted then.
- Tests drive `userConfigDir`/`getwd` seams, never the real `$HOME` or the real working directory.
- Do not weaken existing assertions.
- Commit after each task with that task's Implementation record in this file; conventional message; never push.

## Review Focus

1. `CUTTLE_DATA` set to anything (including a relative path) must never read the user config directory — pinned by `TestResolveExplicitDataDirIgnoresUserConfigDir` (Task 1).
2. A host without a usable user config directory (systemd unit without `$HOME`) must still start: working-directory fallback, warning, no error — pinned by `TestResolveDefaultDataDirFallsBackToWorkingDir` and `TestPrepareWarnsOnDataDirFallback` (Tasks 1–2).
3. The warning must also appear when an existing config is loaded, not only on first-run generation — pinned by `TestPrepareExistingConfigWarnsOnDataDirFallback` (Task 2).
4. A legacy `/data` install must not be picked up: with `CUTTLE_DATA` unset and no user-config config, resolution must target the user config directory, never `/data` — pinned by `TestResolveDefaultDataDir` (Task 1); the user-visible consequence is the README upgrade note (Task 3).
5. The Docker image must stay on `/data`: no Go test covers this; verified by reading `Dockerfile`'s `ENV CUTTLE_DATA=/data` and the DOCKER.md wording (Task 3).

---

### Task 1: Resolve the default data directory from the user config directory

**Files:**
- Modify: `internal/bootstrap/bootstrap.go` (constants block at lines 22–32, `Paths` at 41–46, `Resolve` at 54–120)
- Test: `internal/bootstrap/bootstrap_test.go` (`TestResolveDefaultDataDir` at 110–118; new tests beside it)

**Interfaces:**
- Consumes: nothing new.
- Produces: `const DirName = "cuttle"`; package seams `var userConfigDir func() (string, error)` (defaults to `os.UserConfigDir`) and `var getwd func() (string, error)` (defaults to `os.Getwd`); `func defaultDataDir() (dir string, fallback bool)`; field `Paths.DataDirFromFallback bool`. The `Resolve` signature is unchanged.

- [ ] **Step 1: Write the failing tests**

Replace `TestResolveDefaultDataDir` and add three tests plus two seam helpers in `internal/bootstrap/bootstrap_test.go`:

```go
func setUserConfigDir(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := userConfigDir
	userConfigDir = fn
	t.Cleanup(func() { userConfigDir = old })
}

func setGetwd(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := getwd
	getwd = fn
	t.Cleanup(func() { getwd = old })
}

func TestResolveDefaultDataDir(t *testing.T) {
	configHome := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return configHome, nil })

	paths, err := Resolve("", envMap(nil), []string{})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(configHome, DirName)
	if paths.DataDir != wantDir || paths.ConfigPath != filepath.Join(wantDir, ConfigFileName) {
		t.Fatalf("paths = %+v, want data dir %q", paths, wantDir)
	}
	if paths.DataDirFromFallback {
		t.Fatal("DataDirFromFallback = true, want false")
	}
}

func TestResolveDefaultDataDirFallsBackToWorkingDir(t *testing.T) {
	workDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return "", errors.New("$HOME is not defined") })
	setGetwd(t, func() (string, error) { return workDir, nil })

	paths, err := Resolve("", envMap(nil), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != workDir || paths.ConfigPath != filepath.Join(workDir, ConfigFileName) {
		t.Fatalf("paths = %+v, want data dir %q", paths, workDir)
	}
	if !paths.DataDirFromFallback {
		t.Fatal("DataDirFromFallback = false, want true")
	}
}

func TestResolveExplicitDataDirIgnoresUserConfigDir(t *testing.T) {
	dataDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) {
		t.Fatal("userConfigDir called although CUTTLE_DATA is set")
		return "", nil
	})
	setGetwd(t, func() (string, error) {
		t.Fatal("getwd called although CUTTLE_DATA is set")
		return "", nil
	})

	paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != dataDir || paths.DataDirFromFallback {
		t.Fatalf("paths = %+v, want %q without fallback", paths, dataDir)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/bootstrap -run 'TestResolve(DefaultDataDir|ExplicitDataDirIgnoresUserConfigDir)' -count=1`
Expected: FAIL to compile — `undefined: userConfigDir`, `undefined: getwd`, `undefined: DirName`, `paths.DataDirFromFallback undefined`.

- [ ] **Step 3: Implement the default resolver**

In `internal/bootstrap/bootstrap.go`:

- Replace the `DefaultDataDir = "/data"` constant with `DirName = "cuttle"` and add the seams and resolver next to the constants:

```go
// DirName is the directory name used under the user config directory.
const DirName = "cuttle"

// Seams for the user config directory and the working directory.
var (
	userConfigDir = os.UserConfigDir
	getwd         = os.Getwd
)

// defaultDataDir returns <os.UserConfigDir>/cuttle. When the user config
// directory is unavailable it falls back to the working directory and reports
// the fallback so Prepare can warn.
func defaultDataDir() (dir string, fallback bool) {
	if dir, err := userConfigDir(); err == nil {
		if dir = strings.TrimSpace(dir); dir != "" {
			return filepath.Join(dir, DirName), false
		}
	}
	if wd, err := getwd(); err == nil {
		if wd = strings.TrimSpace(wd); wd != "" {
			return wd, true
		}
	}
	return ".", true
}
```

- Add `DataDirFromFallback bool` to `Paths` (doc comment: "DataDirFromFallback is true when CUTTLE_DATA was unset and the user config directory was unavailable, so the working directory was used.").
- In `Resolve`, replace the `DefaultDataDir` fallback with:

```go
dataDir := strings.TrimSpace(getenv("CUTTLE_DATA"))
fromFallback := false
if dataDir == "" {
	dataDir, fromFallback = defaultDataDir()
}
```

and construct `paths := Paths{DataDir: absDataDir, DataDirFromFallback: fromFallback}` so every return path carries the flag.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: PASS (all existing `Resolve`/`Prepare` tests plus the four above).

- [ ] **Step 5: Run the build and vet gates**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./...`
Expected: both exit 0.

- [ ] **Step 6: Commit and record**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go docs/superpowers/plans/2026-11-12-default-data-dir.md
git commit -m "feat(bootstrap): default CUTTLE_DATA to the user config directory"
```

Append the Implementation record for this task to this plan file **before** the `git add`/`git commit` (so the record is in the same commit).

**Implementation record (Task 1):**
- Date/status: 2026-11-12, complete.
- Repository + commit: `ginuerzh/cuttle` (branch `feat/default-data-dir`), code commit recorded below; spec/plan committed in the docs commit before it.
- Files changed: `internal/bootstrap/bootstrap.go` (const `DefaultDataDir` → `DirName`, `userConfigDir`/`getwd` seams, `defaultDataDir`, `Paths.DataDirFromFallback`, `Resolve` fallback), `internal/bootstrap/bootstrap_test.go` (rewritten `TestResolveDefaultDataDir`, new fallback and explicit-env tests, seam helpers).
- Tests/commands and results: RED — `go test ./internal/bootstrap -run 'TestResolve(DefaultDataDir|ExplicitDataDirIgnoresUserConfigDir)'` failed to build with the expected undefined symbols; GREEN — `GOWORK=off go test ./internal/bootstrap -count=1` → `ok`; `GOWORK=off go build ./... && GOWORK=off go vet ./...` → exit 0.
- Review findings and fixes: none yet (final review pending).
- Deferred items/limitations: the working-directory fallback is not yet warned about (Task 2); legacy `/data` is not detected by design.

---

### Task 2: Warn when the data directory fell back to the working directory

**Files:**
- Modify: `internal/bootstrap/bootstrap.go` (`Prepare` existing-config branch around lines 187–203, `generate` around lines 208–213)
- Test: `internal/bootstrap/bootstrap_test.go` (beside `TestPrepareUsesEnvDefaults`)

**Interfaces:**
- Consumes: `Paths.DataDirFromFallback` from Task 1.
- Produces: `func dataDirFallbackWarning(paths Paths) string`, returning `""` unless the fallback was used; the exact message `CUTTLE_DATA is unset and the user config directory is unavailable; using <dir>`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/bootstrap/bootstrap_test.go`:

```go
func TestPrepareWarnsOnDataDirFallback(t *testing.T) {
	workDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return "", errors.New("$HOME is not defined") })
	setGetwd(t, func() (string, error) { return workDir, nil })

	var buf bytes.Buffer
	res, err := Prepare(Options{Getenv: envMap(nil), SearchPaths: []string{}, Stderr: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || res.DataDir != workDir {
		t.Fatalf("Generated = %v, DataDir = %q, want a first run in %q", res.Generated, res.DataDir, workDir)
	}
	want := "CUTTLE_DATA is unset and the user config directory is unavailable; using " + workDir
	if !strings.Contains(strings.Join(res.Warnings, "\n"), want) {
		t.Fatalf("warnings = %v, want %q", res.Warnings, want)
	}
}

func TestPrepareExistingConfigWarnsOnDataDirFallback(t *testing.T) {
	workDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return "", errors.New("$HOME is not defined") })
	setGetwd(t, func() (string, error) { return workDir, nil })

	hash, err := config.HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	writeBootstrapConfig(t, workDir, &config.Config{
		Admin: config.Admin{PasswordHash: hash},
		Stats: config.Stats{Path: filepath.Join(workDir, "stats.sqlite")},
	})

	res, err := Prepare(Options{Getenv: envMap(nil), SearchPaths: []string{}, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated {
		t.Fatal("Generated = true, want the existing config")
	}
	want := "CUTTLE_DATA is unset and the user config directory is unavailable; using " + workDir
	if !strings.Contains(strings.Join(res.Warnings, "\n"), want) {
		t.Fatalf("warnings = %v, want %q", res.Warnings, want)
	}
}
```

Add `io` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/bootstrap -run 'TestPrepare(Warns|ExistingConfigWarns)OnDataDirFallback' -count=1`
Expected: FAIL on the assertion — `warnings = []` does not contain the fallback message.

- [ ] **Step 3: Implement the warning**

In `internal/bootstrap/bootstrap.go`, add below `defaultDataDir`:

```go
// dataDirFallbackWarning reports the working-directory fallback, or "" when the
// data directory came from CUTTLE_DATA or the user config directory.
func dataDirFallbackWarning(paths Paths) string {
	if !paths.DataDirFromFallback {
		return ""
	}
	return fmt.Sprintf("CUTTLE_DATA is unset and the user config directory is unavailable; using %s", paths.DataDir)
}
```

- Existing-config branch of `Prepare`: build the warnings as `ignoredEnvWarnings(cfg, getenv)`, prepend `dataDirFallbackWarning(paths)` when non-empty, and assign that slice to `Result.Warnings`.
- `generate`: after `warnings := dataDirWarnings(paths.DataDir)`, prepend `dataDirFallbackWarning(paths)` when non-empty so the fallback warning comes first.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/bootstrap -count=1`
Expected: PASS.

- [ ] **Step 5: Run the build and vet gates**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./...`
Expected: both exit 0.

- [ ] **Step 6: Commit and record**

Append the Implementation record for this task to this plan file, then:

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go docs/superpowers/plans/2026-11-12-default-data-dir.md
git commit -m "feat(bootstrap): warn when CUTTLE_DATA falls back to the working directory"
```

**Implementation record (Task 2):**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 3: Document the new default and keep the image on `/data`

**Files:**
- Modify: `README.md` (`CUTTLE_DATA` table row around line 213, zero-config section 197–239, Docker section around 1103)
- Modify: `DOCKER.md` (image env var paragraph around line 44)
- Modify: `AGENTS.md` (configuration invariants around line 197, gotchas around line 579)
- Modify: `Dockerfile` (comment above `ENV CUTTLE_DATA=/data` at line 45)

**Interfaces:**
- Consumes: the behavior from Tasks 1–2.
- Produces: documentation only.

- [ ] **Step 1: Update the README**

- Table row: `| `CUTTLE_DATA` | `<os.UserConfigDir>/cuttle` (`~/.config/cuttle` on Linux) | Directory for the generated config and stats database |`
- In the zero-config section, after the `CUTTLE_DATA=./data ./cuttle serve` example, add:

```markdown
With `CUTTLE_DATA` unset the directory is `<os.UserConfigDir>/cuttle`:
`~/.config/cuttle` on Linux (`$XDG_CONFIG_HOME` wins),
`~/Library/Application Support/cuttle` on macOS and `%AppData%\cuttle` on
Windows. If the user config directory is unavailable — a service without
`$HOME`, for example — Cuttle uses the working directory and logs a warning.
```

- At the end of the same section, add the upgrade note:

```markdown
> Upgrading from a build whose default was `/data`: the binary no longer uses
> `/data` unless `CUTTLE_DATA=/data` is set. Set the variable or move the
> directory before starting the new version; there is no automatic migration.
```

- Docker section: state that the image's `CUTTLE_DATA=/data` is what keeps it on `/data` and that the binary default is `<os.UserConfigDir>/cuttle`, while `CUTTLE_ADMIN_LISTEN` stays a first-run default.

- [ ] **Step 2: Update DOCKER.md**

In the paragraph that says the image sets `CUTTLE_DATA=/data`, add the parenthetical that the binary default is `<os.UserConfigDir>/cuttle`, so the env var is what keeps the container on `/data`.

- [ ] **Step 3: Update AGENTS.md**

- Configuration invariant: `CUTTLE_DATA` default becomes "`<os.UserConfigDir>/cuttle`, falling back to the working directory with a warning".
- Gotcha: "First-run config and stats live in `$CUTTLE_DATA` (default `<os.UserConfigDir>/cuttle`; the Docker image sets `/data`)".

- [ ] **Step 4: Annotate the Dockerfile**

Above `ENV CUTTLE_DATA=/data \` add: `# Keep the image on /data; the binary default is <user config dir>/cuttle.`
Do not change the value.

- [ ] **Step 5: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: all pass. No web change, so the web gate is not required.

- [ ] **Step 6: Commit and record**

Append the Implementation record for this task, then:

```bash
git add README.md DOCKER.md AGENTS.md Dockerfile docs/superpowers/plans/2026-11-12-default-data-dir.md
git commit -m "docs: document the user-config-dir default for CUTTLE_DATA"
```

**Implementation record (Task 3):**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
