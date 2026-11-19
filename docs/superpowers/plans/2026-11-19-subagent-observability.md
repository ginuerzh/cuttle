# Subagent Observability (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record, for every request, whether it came from a subagent and which client's marker said so, expose it as a Sessions filter and detail field, and let on-demand captures show the marker header itself — so the operator can measure subagent traffic before any routing changes.

**Architecture:** A new leaf package `internal/agent` owns the marker table and `Classify`. The stats recorder calls it at record time (not through `Facts`, which only exists after a node ran) and writes two additive `requests` columns. The admin API maps `?subagent=1|0` onto a new `Filter.Subagent`, and the request detail — not the list — surfaces the fields through shadow fields on `requestView`, the same way `attempts` is detail-only today. The three marker headers join the capture allowlist.

**Tech Stack:** Go 1.27, SQLite (mattn/go-sqlite3 style store), `GOWORK=off` with published module tags, React + Ant Design console with Vitest.

**Spec:** `docs/superpowers/specs/2026-11-19-subagent-tiered-routing-design.md` (Phase 1 only; Phase 2 is designed there and out of scope).

## Global Constraints

- Phase 1 only. No `when` field, no routing change, no config schema change, no `xrouting` use.
- `github.com/go-gost/x v0.32.0`; no x change, no sibling-repository change, no `go.mod` bump, no local `replace`.
- Both new columns are additive via `ensureColumns` (`subagent INTEGER NOT NULL DEFAULT 0`, `subagent_source TEXT NOT NULL DEFAULT ''`); no backfill, no destructive migration, existing rows read `0` / `''`.
- Classification runs at record time from the client headers (`clientRequestHeaders`: `OriginalRequest.Header` first). Never via `stats.Facts`.
- Classification tests **map-key presence**, never `Get(...) != ""`; an empty-valued marker counts as present.
- Raw header values never reach the `requests` row, the log, or any API payload. Capture's `request_headers` is the deliberate exception.
- `stats.Request` keeps the new fields out of JSON (`json:"-"`); only the request detail exposes them.
- Code, comments, docs and commit messages are English. Console source strings are Chinese and are translated in `web/src/locales/en-US.ts`.
- Run every Go command with `GOWORK=off` from `cuttle/`. Run the web gate (`npm test`, `npx tsc --noEmit`, `npm run build`) before claiming a web task done.
- Commit after each completed task and fill that task's Implementation record in this plan in the same commit. Never push.

## Review Focus

- Header canonicalization: a client sending `X-Parent-Session-Id` must classify exactly like `x-parent-session-id` — Task 1 test `TestClassify`.
- An empty-valued marker (`x-openai-subagent:`) must classify as a subagent — Task 1 test `TestClassify`.
- A request carrying more than one marker (a proxy or a fork adds one) must resolve deterministically to the first table entry — Task 1 test `TestClassifyPrecedence`.
- A row that fails before a node (draft-entrypoint 502, every node skipped) must still carry the class — Task 2 recorder test with an empty route/node.
- A database written before this change must open, gain both columns, and read `0`/`''` without a migration step — Task 2 test `TestStoreSubagentColumns`.

---

### Task 1: `internal/agent` marker table and classifier

**Files:**
- Create: `cuttle/internal/agent/agent.go`
- Test: `cuttle/internal/agent/agent_test.go`

**Interfaces:**
- Produces: `const (SourceClaudeCode = "claude_code"; SourceOpenCode = "opencode"; SourceCodex = "codex")`.
- Produces: `func Classify(h http.Header) (source string, subagent bool)` — returns `""`, `false` when no marker is present, otherwise the first table match's source.
- Produces: `var markers []marker` is package-private; later phases generate the matcher rule from it.

- [ ] **Step 1: Write the failing classification test**

```go
func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		value   string
		want    string
		wantSub bool
	}{
		{"claude code subagent", "x-claude-code-agent-id", "agent-7", agent.SourceClaudeCode, true},
		{"opencode child session", "X-Parent-Session-Id", "ses_parent1", agent.SourceOpenCode, true},
		{"codex review", "x-openai-subagent", "review", agent.SourceCodex, true},
		{"codex empty value still present", "x-openai-subagent", "", agent.SourceCodex, true},
		{"unrelated header", "user-agent", "opencode/2.3.0", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.header != "" {
				h.Set(tt.header, tt.value)
			}
			got, sub := agent.Classify(h)
			if got != tt.want || sub != tt.wantSub {
				t.Fatalf("Classify = %q,%v want %q,%v", got, sub, tt.want, tt.wantSub)
			}
		})
	}
	if got, sub := agent.Classify(nil); got != "" || sub {
		t.Fatalf("Classify(nil) = %q,%v want empty,false", got, sub)
	}
}

func TestClassifyPrecedence(t *testing.T) {
	h := http.Header{}
	h.Set("x-openai-subagent", "collab_spawn")
	h.Set("x-parent-session-id", "ses_parent1")
	h.Set("x-claude-code-agent-id", "agent-7")
	if got, _ := agent.Classify(h); got != agent.SourceClaudeCode {
		t.Fatalf("Classify = %q want %q (table order)", got, agent.SourceClaudeCode)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/agent -count=1`
Expected: FAIL (package does not exist / `undefined: agent.Classify`).

- [ ] **Step 3: Implement the package**

Create `internal/agent/agent.go` with a package doc comment stating it is the single source of truth for the subagent marker headers and that `internal/gateway` and `internal/stats` both consume it. Define:

```go
type marker struct {
	source string
	header string
}

var markers = []marker{
	{SourceClaudeCode, "x-claude-code-agent-id"},
	{SourceOpenCode, "x-parent-session-id"},
	{SourceCodex, "x-openai-subagent"},
}

func Classify(h http.Header) (source string, subagent bool)
```

`Classify` looks each header up with `http.CanonicalHeaderKey` and tests map-key presence (`_, ok := h[key]`), returning the first match. It must not read header values.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/agent -count=1 -v`
Expected: PASS (`TestClassify` with five subtests, `TestClassifyPrecedence`).

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/agent/agent.go internal/agent/agent_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(agent): add subagent marker classifier"
```

**Implementation record — Task 1**

- Date/status: 2026-11-19, DONE. Classifier implemented and covered; focused package test green.
- Repository/commit: `cuttle` (`/root/code/go-gost/cuttle/.worktrees/feat-subagent-observability`, branch `feat/subagent-observability`), implementation commit `1820a9f9`; this record and the plan/spec travel in the commit that follows it (a commit cannot name its own SHA).
- Files changed: created `internal/agent/agent.go` (source constants, ordered marker table, `Classify`) and `internal/agent/agent_test.go` (`TestClassify` with five subtests, `TestClassifyPrecedence`); this plan and the spec file travel in the same commit because the record lives here.
- Tests/commands and results: RED, before implementation — `GOWORK=off go test ./internal/agent -count=1` → FAIL (`no non-test Go files in .../internal/agent`, build failed). GREEN, after implementation — `GOWORK=off go test ./internal/agent -count=1 -v` → PASS (`TestClassify` with five subtests, `TestClassifyPrecedence`).
- Review findings and fixes: none; the empty-valued marker and the multi-marker precedence cases are pinned by the tests from the start.
- Deferred items/limitations: `markers` is package-private and has no matcher-rule generator yet — Phase 2 adds it, together with the only consumer that needs it.

---

### Task 2: Persist the class on every request row

**Files:**
- Modify: `cuttle/internal/stats/store.go` (`Request`, `ensureColumns`, insert, every select/scan site including the live monitor, `Filter`, `Filter.where`)
- Modify: `cuttle/internal/stats/recorder.go` (`Record`)
- Modify: `cuttle/AGENTS.md` (Stats invariants)
- Test: `cuttle/internal/stats/store_test.go`, `cuttle/internal/stats/recorder_test.go`

**Interfaces:**
- Consumes: `agent.Classify`, `agent.SourceCodex` and friends.
- Produces: `stats.Request.Subagent bool` and `stats.Request.SubagentSource string`, both `json:"-"`.
- Produces: `stats.Filter.Subagent *bool` (`nil` = no constraint).

- [ ] **Step 1: Write the failing store test**

Follow the existing temp-store pattern in `store_test.go`:

```go
func TestStoreSubagentColumns(t *testing.T) {
	// Open a store at t.TempDir(), insert two requests: one with
	// Subagent: true, SubagentSource: agent.SourceCodex, one with the zero
	// value. Read both back with Filter{} and assert the values survive.
	// Then read with Filter{Subagent: ptr(true)} and assert only the marked
	// row returns; repeat with ptr(false) for the other row.
}
```

Include a legacy-database case: create the database with a `requests` table that lacks both columns (copy the existing `open_version_test.go` style helper if it fits), reopen through `Open`, and assert the columns were added and the pre-existing row reads `Subagent == false` / `SubagentSource == ""`.

- [ ] **Step 2: Write the failing recorder test**

```go
func TestRecorderSubagentClass(t *testing.T) {
	// Build the recorder object the way recorder_test.go already does, with
	// HTTP.OriginalRequest carrying only "x-parent-session-id", and an empty
	// Route/Node (the request failed before a node). Record it and assert the
	// stored row has Subagent true and SubagentSource "opencode".
}
```

Add a second case with no marker headers asserting `false` / `""`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/stats -run 'TestStoreSubagentColumns|TestRecorderSubagentClass' -count=1`
Expected: FAIL (`unknown field Subagent` / column missing).

- [ ] **Step 4: Implement the columns and the classification**

`store.go`: add the two fields to `Request` with `json:"-"`; add `"subagent INTEGER NOT NULL DEFAULT 0"` and `"subagent_source TEXT NOT NULL DEFAULT ''"` to the `ensureColumns("requests", ...)` list; add both to the INSERT column list and bind values, and to every SELECT list and scan destination (list, detail, live-monitor tail) — let the compiler and the package gate find the sites. Add `Subagent *bool` to `Filter` and one clause in `where()` (`subagent = ?`, binding `1`/`0`).

`recorder.go`: after the request fields are populated, call `agent.Classify(clientRequestHeaders(&ro))` and assign both fields. Do not touch `Facts`.

`AGENTS.md`: one line under Stats invariants stating that `subagent`/`subagent_source` are additive columns filled at record time from the client headers by `internal/agent`, with no backfill and no raw values.

- [ ] **Step 5: Run the tests and the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/stats -count=1 && GOWORK=off go build ./... && GOWORK=off go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 6: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/stats/store.go internal/stats/recorder.go internal/stats/store_test.go internal/stats/recorder_test.go AGENTS.md
git -C /root/code/go-gost/cuttle commit -m "feat(stats): record subagent class on every request"
```

**Implementation record — Task 2**

- Date/status: 2026-11-19, DONE. Both columns are written at record time and filterable; focused and package tests green, full build and vet clean.
- Repository/commit: `cuttle` (`/root/code/go-gost/cuttle/.worktrees/feat-subagent-observability`, branch `feat/subagent-observability`), implementation commit `3850c698`; this record follows it (a commit cannot name its own SHA).
- Files changed: `internal/stats/store.go` (`Request.Subagent`/`SubagentSource` with `json:"-"`, both columns in `ensureColumns`, the INSERT list and values, both SELECT lists, `scanRequests`, `Filter.Subagent` and its `where()` clause); `internal/stats/recorder.go` (`agent.Classify(clientRequestHeaders(&ro))` right after `requestContext`, source first, class second); `internal/stats/store_test.go` (`TestStoreSubagentColumns`, `TestStoreSubagentColumnsAddedToExistingDatabase`); `internal/stats/recorder_test.go` (`TestRecorderSubagentClass`); `AGENTS.md` (one Stats-invariants entry).
- Tests/commands and results: RED — `GOWORK=off go test ./internal/stats -run 'TestStoreSubagentColumns|TestRecorderSubagentClass' -count=1` → FAIL (build: `unknown field SubagentSource in struct literal of type Request`, `got.Subagent undefined`). GREEN — same command → `ok github.com/ginuerzh/cuttle/internal/stats 0.812s`. Package gate — `GOWORK=off go test ./internal/stats -count=1` → `ok ... 22.575s`. `GOWORK=off go build ./... && GOWORK=off go vet ./...` → clean; `gofmt -l internal/agent internal/stats` → no output.
- Review findings and fixes: none; the legacy-database test drops both columns with `ALTER TABLE ... DROP COLUMN` and reopens, so `ensureColumns` is proven rather than assumed. The graph queries keep their own explicit column lists and are untouched.
- Deferred items/limitations: no rollups, charts or alerts by class (Phase 2 decision 8); pre-upgrade rows read `0`/`''` with no backfill; `Filter.Subagent` has no admin surface until Task 3.

---

### Task 3: `?subagent=` filter and detail-only fields

**Files:**
- Modify: `cuttle/internal/admin/handlers_stats.go` (list filter parsing, detail handler)
- Modify: `cuttle/internal/admin/labels.go` (`requestView`)
- Test: `cuttle/internal/admin/handlers_stats_test.go`

**Interfaces:**
- Consumes: `stats.Filter.Subagent`, `stats.Request.Subagent`, `stats.Request.SubagentSource`.
- Produces: `GET /api/requests?subagent=1|0`; `GET /api/requests/{id}` payload gains `subagent` (bool) and `subagentSource` (string); the list payload keeps both keys out.

- [ ] **Step 1: Write the failing API tests**

Add three cases in the existing handler-test style: `subagent=1` and `subagent=0` reach the store as a non-nil filter (assert via the returned rows), `subagent=`/absent leaves it `nil`, and `subagent=maybe` answers `400` with a JSON `error`. Add a detail test asserting `subagent`/`subagentSource` appear for a marked row, and a list test asserting the raw JSON of `GET /api/requests` has neither key.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -run 'TestRequestsSubagent' -count=1`
Expected: FAIL (unknown query handling / missing fields).

- [ ] **Step 3: Implement**

`handlers_stats.go`: parse `q.Get("subagent")` — `"1"` → `&true`, `"0"` → `&false`, empty → `nil`, anything else → the existing JSON `400` pattern.

`labels.go`: add to `requestView` two shadow fields, mirroring the `Attempts` comment pattern:

```go
Subagent       *bool  `json:"subagent,omitempty"`
SubagentSource string `json:"subagentSource,omitempty"`
```

Fill them only in the detail handler (a small helper on `requestView` or assignment at the call site); the list leaves them at their zero value so the keys stay out.

- [ ] **Step 4: Run the tests and the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/admin/handlers_stats.go internal/admin/labels.go internal/admin/handlers_stats_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(admin): filter sessions by subagent and show it in the detail"
```

**Implementation record — Task 3**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Marker headers on the capture allowlist

**Files:**
- Modify: `cuttle/internal/stats/headers.go`
- Test: `cuttle/internal/stats/headers_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `captureHeaders` keeps `x-claude-code-agent-id`, `x-parent-session-id` and `x-openai-subagent`.

- [ ] **Step 1: Extend the allowlist test**

In the existing allowlist test add the three headers to the kept set, and keep (or add) a non-allowlisted header such as `authorization` asserting it is still dropped.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/stats -run TestCaptureHeadersAllowlist -count=1`
Expected: FAIL (three headers missing from the kept map).

- [ ] **Step 3: Implement**

Add the three names to `headerExact` with a comment saying they are the subagent markers `internal/agent` classifies on, kept so a capture can show why a request was classified as it was.

- [ ] **Step 4: Run the test and the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/stats/headers.go internal/stats/headers_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(stats): keep subagent markers on captures"
```

**Implementation record — Task 4**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: Sessions filter and detail row in the console

**Files:**
- Modify: `cuttle/web/src/api/types.ts`, `cuttle/web/src/pages/Requests.tsx`, `cuttle/web/src/components/RequestDetail.tsx`, `cuttle/web/src/locales/en-US.ts`
- Test: `cuttle/web/src/pages/Requests.test.ts`, `cuttle/web/src/components/RequestDetail.test.tsx`

**Interfaces:**
- Consumes: `GET /api/requests?subagent=1|0` and the detail fields `subagent` / `subagentSource`.
- Produces: a `subagent` request filter (`'' | '1' | '0'`) and a `标记来源 · <source>` detail row.

- [ ] **Step 1: Write the failing tests**

`Requests.test.ts`: assert the filter state maps to the query (`'1'` → `subagent: '1'`, `''` → key absent), following the existing `errorClass` case. `RequestDetail.test.tsx`: render a marked request and assert the row shows the source; render an unmarked one and assert the row is absent. Add the new strings to both locales and assert the en-US keys exist.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle/web && npm test -- --run src/pages/Requests.test.ts src/components/RequestDetail.test.tsx`
Expected: FAIL (missing field, missing string).

- [ ] **Step 3: Implement**

`types.ts`: add `subagent?: boolean` and `subagentSource?: string` to the request row type, and `subagent?: string` to the requests filter type. `Requests.tsx`: a `subagent` state plus a Select (全部 / 是 / 否) beside the existing filters, wired into the `filters` object. `RequestDetail.tsx`: a conditional row `标记来源 · <source>` when `subagent` is true. `locales/en-US.ts`: both strings.

- [ ] **Step 4: Run the web gate**

Run: `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS, no type errors, bundle builds.

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add web/src/api/types.ts web/src/pages/Requests.tsx web/src/components/RequestDetail.tsx web/src/locales/en-US.ts web/src/pages/Requests.test.ts web/src/components/RequestDetail.test.tsx
git -C /root/code/go-gost/cuttle commit -m "feat(web): sessions subagent filter and marker source"
```

**Implementation record — Task 5**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: README, end-to-end verification and the Phase 1 record

**Files:**
- Modify: `cuttle/README.md`
- Test: manual (recorded below)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Document the two columns and the workflow**

Add a README section: what `subagent` and `subagent_source` mean, that they are **measured only** (no routing changes in this phase), the client/version matrix (Claude Code `x-claude-code-agent-id`; OpenCode `x-parent-session-id` from #43188, missing in v1.18.16–v1.18.18 and in the Kilo fork; Codex `x-openai-subagent`, source-verified only), and the workflow — filter Sessions by `subagent=1`, compare the request and token shares over a week, then decide whether Phase 2 is worth it.

- [ ] **Step 2: Verify end-to-end with OpenCode**

Run a local cuttle (`CUTTLE_DATA=./data ./cuttle serve` after the web bundle build), point one provider node at a local header-echo upstream, route a model through it, and drive one opencode v2.3.0 session plus one `subagent` call. Expected: the parent request row reads `subagent = false`; the child request row reads `subagent = true` / `subagent_source = opencode`; an on-demand capture of the child request shows `x-parent-session-id` in `request_headers`.

- [ ] **Step 3: Record what could not be verified**

State in the implementation record: Claude Code is production-verified through the sticky key but was not smoked here (no binary in the environment); Codex is source-verified only (`codex-rs/core/src/client.rs`), has no binary here, and its header on a third-party base URL is unverified — a smoke run by the owner is required **before relying on `subagent=codex`**, not before merge.

- [ ] **Step 4: Run the full gates**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Run: `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS both.

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add README.md docs/superpowers/plans/2026-11-19-subagent-observability.md
git -C /root/code/go-gost/cuttle commit -m "docs: document subagent observability and its soft measurement workflow"
```

**Implementation record — Task 6**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

## Deferred to Phase 2

Everything design sections 4–6 of the spec describe: the `when: subagent` binding, the matcher rule generator, the class tier in `bindingOrder`/`probePriorityOrder`, health/admin/console `when` surfaces, and the `AGENTS.md` routing-invariant amendment. Do not start them from this plan.