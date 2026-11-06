# llm-gw Versioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every llm-gw build identifies itself through the CLI, startup log, `/api/health`, and the bottom of the admin navigation panel.

**Architecture:** Build metadata lives in a small `internal/version` package and is injected with `-ldflags -X` by the Dockerfile from CI-supplied build args. The admin health handler and the React sidebar read the same values.

**Tech Stack:** Go 1.26, embedded React/Vite/Ant Design console, Docker Buildx, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-06-versioning-design.md`

## Global Constraints

- Do not add Go or npm dependencies.
- `GET /api/health` stays backward compatible: `200` and `"ok": true` remain.
- Version display lives only at the bottom of the left navigation panel, not in the header.
- Tag builds use the tag name (`v0.1.0`); branch builds use `<ref>-<sha7>`; plain local builds use `dev`.
- UI text added by this plan is technical (`commit`, `built`) and is not translated.
- Run Go commands with `GOWORK=off`; rebuild the web bundle before a Go build that embeds it.
- Never push or tag in this plan. Release actions require explicit approval.
- Fill each task's Implementation record, then commit it with the task.

## Review Focus

- **Plain local build:** `go build` without ldflags must still print `llm-gw dev`, not an empty or broken version.
- **Health unavailable:** if `/api/health` fails or times out, the sidebar hides the version and the console keeps working.
- **Release label:** a `v0.1.0` build shows `v0.1.0 · <sha7>` exactly once, with no doubled `v` or duplicate commit.
- **Short or empty commit:** `ShortCommit` handles commits shorter than seven characters and empty metadata without panics or stray separators.
- **Narrow sidebar:** a long tag or version wraps or truncates inside the 230px drawer instead of overflowing.

---

### Task 1: Build identity, CLI, and startup log

**Files:**
- Create: `internal/version/version.go`
- Create: `internal/version/version_test.go`
- Create: `main_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `version.Version`, `version.Commit`, `version.BuildDate` (string vars); `version.Info{Version, Commit, BuiltAt string}`; `version.Current() Info`; `version.ShortCommit() string`; `version.String() string`; `printVersion(io.Writer)` in `main`.

- [x] **Step 1: Write the failing version tests**

Create `internal/version/version_test.go`:

```go
package version

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		commit   string
		builtAt  string
		want     string
	}{
		{"dev default", "", "", "", "dev"},
		{"main build", "main-694deb5", "", "", "main-694deb5"},
		{"release build", "v0.1.0", "694deb53b2c9abebf6e812505424d8b870a5ec53", "2026-11-06T10:00:00Z", "v0.1.0 (694deb5, 2026-11-06T10:00:00Z)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldV, oldC, oldB := Version, Commit, BuildDate
			t.Cleanup(func() { Version, Commit, BuildDate = oldV, oldC, oldB })
			Version, Commit, BuildDate = tc.version, tc.commit, tc.builtAt
			if got := String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentAndShortCommit(t *testing.T) {
	oldV, oldC, oldB := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldV, oldC, oldB })

	Version, Commit, BuildDate = "  main-694deb5  ", "  694deb53b2c9  ", "  2026-11-06T10:00:00Z  "
	if got := Current().Version; got != "main-694deb5" {
		t.Fatalf("Current().Version = %q", got)
	}
	if got := Current().BuiltAt; got != "2026-11-06T10:00:00Z" {
		t.Fatalf("Current().BuiltAt = %q", got)
	}
	if got := ShortCommit(); got != "694deb5" {
		t.Fatalf("ShortCommit() = %q", got)
	}

	Commit = "abc"
	if got := ShortCommit(); got != "abc" {
		t.Fatalf("ShortCommit() short = %q", got)
	}
	Commit = ""
	if got := ShortCommit(); got != "" {
		t.Fatalf("ShortCommit() empty = %q", got)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/version -count=1`

Expected: FAIL because the package does not exist yet.

- [x] **Step 3: Implement `internal/version/version.go`**

The package trims injected whitespace, falls back to `dev`, and keeps the JSON
field names `version`, `commit`, `builtAt`.

```go
package version

import "strings"

var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"builtAt,omitempty"`
}

func Current() Info {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "dev"
	}
	return Info{
		Version: v,
		Commit:  strings.TrimSpace(Commit),
		BuiltAt: strings.TrimSpace(BuildDate),
	}
}

func ShortCommit() string {
	c := strings.TrimSpace(Commit)
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func String() string {
	info := Current()
	parts := make([]string, 0, 2)
	if c := ShortCommit(); c != "" {
		parts = append(parts, c)
	}
	if info.BuiltAt != "" {
		parts = append(parts, info.BuiltAt)
	}
	if len(parts) == 0 {
		return info.Version
	}
	return info.Version + " (" + strings.Join(parts, ", ") + ")"
}
```

- [x] **Step 4: Run the version tests to verify they pass**

Run: `GOWORK=off go test ./internal/version -count=1`

Expected: PASS.

- [x] **Step 5: Write the failing CLI test**

Create `main_test.go`:

```go
package main

import (
	"bytes"
	"testing"

	"github.com/go-gost/llm-gw/internal/version"
)

func TestPrintVersion(t *testing.T) {
	var buf bytes.Buffer
	printVersion(&buf)
	want := "llm-gw " + version.String() + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("printVersion() = %q, want %q", got, want)
	}
}
```

- [x] **Step 6: Run the CLI test to verify it fails**

Run: `GOWORK=off go test . -run TestPrintVersion -count=1`

Expected: FAIL with `undefined: printVersion`.

- [x] **Step 7: Wire the CLI and startup log**

In `main.go`:

- Import `github.com/go-gost/llm-gw/internal/version`.
- At the top of `run`, handle `version`, `-v`, and `--version` by calling
  `printVersion(os.Stdout)` and returning nil; keep `hash-password` next.
- Add `func printVersion(w io.Writer) { fmt.Fprintf(w, "llm-gw %s\n", version.String()) }`.
- Update the usage error to
  `usage: llm-gw <serve [-c <config>] | version | hash-password>`.
- After `log.SetDefault()`, log `log.Infof("llm-gw %s starting", version.String())`.

- [x] **Step 8: Run the focused tests**

Run: `GOWORK=off go test ./internal/version . -count=1`

Expected: PASS.

- [x] **Step 9: Fill the Implementation record and commit**

```bash
git add internal/version/version.go internal/version/version_test.go main.go main_test.go \
  docs/superpowers/specs/2026-11-06-versioning-design.md docs/superpowers/plans/2026-11-06-versioning.md
git commit -m "feat(version): report the build identity"
```

**Implementation record**:
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` @ `a649295a`
- Files changed: `internal/version/version.go`, `internal/version/version_test.go`, `main.go`, `main_test.go`, design spec, plan
- Tests/commands and results: version tests RED (`undefined: Version`) then PASS; `TestPrintVersion` RED (`undefined: printVersion`) then PASS; `GOWORK=off go test ./internal/version . -count=1` PASS.
- Review findings and fixes: none.
- Deferred items/limitations: none.

---

### Task 2: Health endpoint and sidebar display

**Files:**
- Modify: `internal/admin/server.go`
- Create: `internal/admin/health_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Create: `web/src/version.ts`
- Create: `web/src/version.test.ts`
- Create: `web/src/components/VersionFooter.tsx`
- Create: `web/src/components/VersionFooter.test.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: `version.Current()` from Task 1.
- Produces: `GET /api/health` fields `version`, `commit`, `builtAt`; web `Health{ok, version, commit?, builtAt?}`; `getHealth()`, `versionLabel(health)`, `versionTitle(health)`, `VersionFooter({health})`.

- [x] **Step 1: Write the failing health test**

Create `internal/admin/health_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-gost/llm-gw/internal/version"
)

func TestHealthReportsBuildIdentity(t *testing.T) {
	oldV, oldC, oldB := version.Version, version.Commit, version.BuildDate
	t.Cleanup(func() { version.Version, version.Commit, version.BuildDate = oldV, oldC, oldB })
	version.Version = "v9.9.9-test"
	version.Commit = "deadbeefcafe"
	version.BuildDate = "2026-11-06T10:00:00Z"

	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		BuiltAt string `json:"builtAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Version != "v9.9.9-test" || got.Commit != "deadbeefcafe" || got.BuiltAt != "2026-11-06T10:00:00Z" {
		t.Fatalf("health = %+v", got)
	}
}
```

- [x] **Step 2: Run the health test to verify it fails**

Run: `GOWORK=off go test ./internal/admin -run TestHealthReportsBuildIdentity -count=1`

Expected: FAIL because `version`, `commit`, and `builtAt` are absent from the payload.

- [x] **Step 3: Implement the health payload**

In `internal/admin/server.go`, import `internal/version` and replace
`handleHealth` with a response struct:

```go
type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"builtAt,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	info := version.Current()
	writeJSON(w, http.StatusOK, healthResponse{
		OK:      true,
		Version: info.Version,
		Commit:  info.Commit,
		BuiltAt: info.BuiltAt,
	})
}
```

- [x] **Step 4: Run the health test to verify it passes**

Run: `GOWORK=off go test ./internal/admin -run TestHealthReportsBuildIdentity -count=1`

Expected: PASS.

- [x] **Step 5: Write the failing web label test**

Create `web/src/version.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { versionLabel, versionTitle } from './version';

describe('versionLabel', () => {
  it('appends the short commit to a release tag', () => {
    expect(versionLabel({ ok: true, version: 'v0.1.0', commit: '694deb53b2c9' })).toBe('v0.1.0 · 694deb5');
  });

  it('does not repeat the commit in an automatic build version', () => {
    expect(versionLabel({ ok: true, version: 'main-694deb5', commit: '694deb53b2c9' })).toBe('main-694deb5');
  });

  it('omits the commit when missing', () => {
    expect(versionLabel({ ok: true, version: 'dev' })).toBe('dev');
  });
});

describe('versionTitle', () => {
  it('joins the commit and build time', () => {
    expect(versionTitle({ ok: true, version: 'v0.1.0', commit: '694deb53b2c9', builtAt: '2026-11-06T10:00:00Z' }))
      .toBe('commit 694deb53b2c9 · built 2026-11-06T10:00:00Z');
  });

  it('returns undefined without metadata', () => {
    expect(versionTitle({ ok: true, version: 'dev' })).toBeUndefined();
  });
});
```

- [x] **Step 6: Run the web test to verify it fails**

Run: `cd web && npm test -- src/version.test.ts`

Expected: FAIL because `./version` does not exist.

- [x] **Step 7: Add the web types, client, and label helpers**

In `web/src/api/types.ts`, add:

```ts
export interface Health {
  ok: boolean;
  version: string;
  commit?: string;
  builtAt?: string;
}
```

In `web/src/api/client.ts`, import `Health` and add:

```ts
export function getHealth(): Promise<Health> {
  return request<Health>('GET', '/api/health');
}
```

Create `web/src/version.ts`:

```ts
import type { Health } from './api/types';

export function shortCommit(commit?: string): string {
  return (commit ?? '').trim().slice(0, 7);
}

export function versionLabel(health: Health): string {
  const commit = shortCommit(health.commit);
  if (!commit || health.version.endsWith(commit)) {
    return health.version;
  }
  return `${health.version} · ${commit}`;
}

export function versionTitle(health: Health): string | undefined {
  const parts: string[] = [];
  if (health.commit) parts.push(`commit ${health.commit}`);
  if (health.builtAt) parts.push(`built ${health.builtAt}`);
  return parts.length ? parts.join(' · ') : undefined;
}
```

- [x] **Step 8: Run the web label test to verify it passes**

Run: `cd web && npm test -- src/version.test.ts`

Expected: PASS.

- [x] **Step 9: Add the failing footer test**

Create `web/src/components/VersionFooter.test.tsx`:

```tsx
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { VersionFooter } from './VersionFooter';

describe('VersionFooter', () => {
  it('renders nothing when health is unavailable', () => {
    expect(renderToStaticMarkup(<VersionFooter health={null} />)).toBe('');
  });

  it('renders the version label with the full metadata in the title', () => {
    const html = renderToStaticMarkup(
      <VersionFooter health={{ ok: true, version: 'v0.1.0', commit: '694deb53b2c9', builtAt: '2026-11-06T10:00:00Z' }} />,
    );
    expect(html).toContain('v0.1.0 · 694deb5');
    expect(html).toContain('commit 694deb53b2c9 · built 2026-11-06T10:00:00Z');
  });
});
```

- [x] **Step 10: Run the footer test to verify it fails**

Run: `cd web && npm test -- src/components/VersionFooter.test.tsx`

Expected: FAIL because the component does not exist.

- [x] **Step 11: Implement the footer and wire it into the sidebar**

Create `web/src/components/VersionFooter.tsx`:

```tsx
import { Typography } from 'antd';
import type { Health } from '../api/types';
import { versionLabel, versionTitle } from '../version';

export function VersionFooter({ health }: { health: Health | null }) {
  if (!health) return null;
  return (
    <Typography.Text
      type="secondary"
      style={{ fontSize: 12, display: 'block', textAlign: 'center', wordBreak: 'break-all' }}
      title={versionTitle(health)}
    >
      {versionLabel(health)}
    </Typography.Text>
  );
}
```

In `web/src/App.tsx`:

- Import `getHealth` and `Health`, plus `VersionFooter`.
- Add `const [health, setHealth] = useState<Health | null>(null);` and a
  mount effect that calls `getHealth()` once, ignores failures, and guards
  against unmount.
- Add `health: Health | null` to the `Sidebar` props and pass it at the call site.
- Inside the sidebar footer `Space`, after the logout button, render
  `<VersionFooter health={health} />`.

- [x] **Step 12: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`

Expected: all Vitest files pass, TypeScript passes, Vite builds into
`internal/admin/dist` (git-ignored, not committed).

- [x] **Step 13: Run the Go gate for the touched packages**

Run: `GOWORK=off go test ./internal/admin -count=1`

Expected: PASS.

- [x] **Step 14: Fill the Implementation record and commit**

```bash
git add internal/admin/server.go internal/admin/health_test.go \
  web/src/api/types.ts web/src/api/client.ts web/src/version.ts web/src/version.test.ts \
  web/src/components/VersionFooter.tsx web/src/components/VersionFooter.test.tsx web/src/App.tsx \
  docs/superpowers/plans/2026-11-06-versioning.md
git commit -m "feat(admin): expose and display the build version"
```

**Implementation record**:
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` @ `3464beea`
- Files changed: `internal/admin/server.go`, `internal/admin/health_test.go`, `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/version.ts`, `web/src/version.test.ts`, `web/src/components/VersionFooter.tsx`, `web/src/components/VersionFooter.test.tsx`, `web/src/App.tsx`, plan
- Tests/commands and results: health test RED (`Version:""`) then PASS; `version.test.ts` RED (missing module) then 5 PASS; footer test RED (missing component) then 2 PASS; `cd web && npm test` 103 PASS; `npx tsc --noEmit` PASS; `npm run build` PASS; `GOWORK=off go test ./internal/admin -count=1` PASS.
- Review findings and fixes: none.
- Deferred items/limitations: UI placement and wrapping were verified by Vitest, TypeScript and the production build only; the repository has no browser visual regression tests.

---

### Task 3: Docker, CI, and documentation

**Files:**
- Modify: `Dockerfile`
- Modify: `.github/workflows/docker.yml`
- Modify: `README.md`
- Modify: `DOCKER.md`

**Interfaces:**
- Consumes: the `internal/version` link-time variables from Task 1.
- Produces: Docker build args `VERSION`, `COMMIT`, `BUILT_AT`; CI-computed versions.

- [x] **Step 1: Add the build args to the Dockerfile**

In the `golang:1.27-alpine` build stage, after `ARG TARGETARCH`:

```dockerfile
ARG VERSION=dev
ARG COMMIT=
ARG BUILT_AT=
```

Extend the `go build` line with the three `-X` flags (single line to keep the
shell quoting unambiguous):

```dockerfile
    go build -trimpath -ldflags="-s -w -X github.com/go-gost/llm-gw/internal/version.Version=${VERSION} -X github.com/go-gost/llm-gw/internal/version.Commit=${COMMIT} -X github.com/go-gost/llm-gw/internal/version.BuildDate=${BUILT_AT}" -o /out/llm-gw .
```

- [x] **Step 2: Verify a local build with injected metadata**

Run:

```bash
GOWORK=off go build \
  -ldflags="-X github.com/go-gost/llm-gw/internal/version.Version=v9.9.9-test -X github.com/go-gost/llm-gw/internal/version.Commit=deadbeefcafe -X github.com/go-gost/llm-gw/internal/version.BuildDate=2026-11-06T10:00:00Z" \
  -o /tmp/opencode/llm-gw-version-test .
/tmp/opencode/llm-gw-version-test version
```

Expected: `llm-gw v9.9.9-test (deadbee, 2026-11-06T10:00:00Z)`.

- [x] **Step 3: Add the version step to the workflow**

In `.github/workflows/docker.yml`, before `Build and push`, add:

```yaml
      - name: Resolve version
        id: version
        shell: bash
        run: |
          if [[ "${GITHUB_REF}" == refs/tags/v* ]]; then
            version="${GITHUB_REF_NAME}"
          else
            version="${GITHUB_REF_NAME}-${GITHUB_SHA::7}"
          fi
          echo "version=${version}" >> "$GITHUB_OUTPUT"
          echo "built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$GITHUB_OUTPUT"
```

Add to the `Build and push` step:

```yaml
          build-args: |
            VERSION=${{ steps.version.outputs.version }}
            COMMIT=${{ github.sha }}
            BUILT_AT=${{ steps.version.outputs.built_at }}
```

- [x] **Step 4: Document the version surfaces and build args**

In `README.md`, add a `### Versioning` subsection after `### Build` covering
`llm-gw version`, the three version forms, the sidebar/health surfaces, and a
local build with explicit ldflags.

In `DOCKER.md`, show a build with the three build args and note that `v*` tags
publish the semver image tags while `main` builds report `main-<sha7>`.

- [x] **Step 5: Run the full local gates**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit && npm run build
```

Expected: all pass.

- [x] **Step 6: Fill the Implementation record and commit**

```bash
git add Dockerfile .github/workflows/docker.yml README.md DOCKER.md docs/superpowers/plans/2026-11-06-versioning.md
git commit -m "build: inject the version into images"
```

**Implementation record**:
- Date/status: 2026-11-06 / complete
- Repository + commit SHA: `llm-gw` @ `125144c0`
- Files changed: `Dockerfile`, `.github/workflows/docker.yml`, `README.md`, `DOCKER.md`, plan
- Tests/commands and results: injected test build printed `llm-gw v9.9.9-test (deadbee, 2026-11-06T10:00:00Z)` for both `version` and `--version`; `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1` PASS; `cd web && npm test` 103 PASS, `npx tsc --noEmit` PASS, `npm run build` PASS; workflow YAML parses; `git diff --check` clean.
- Review findings and fixes: none.
- Deferred items/limitations: the Docker image was not built locally (multi-stage network build); the next CI run validates the Dockerfile and build args. UI placement is covered by Vitest/TypeScript/build only.

---

## Post-plan release (do not execute without explicit approval)

After the changes are merged and verified:

```bash
git tag -a v0.1.0 -m "llm-gw v0.1.0"
git push origin v0.1.0
```

Then confirm the CI run publishes `0.1.0`, `0.1`, and `latest`, and that the
deployed pod reports `v0.1.0` through `llm-gw version` and `/api/health`.
