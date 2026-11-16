# Entrypoint longest-match routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) to execute this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Entrypoints are selected by URI-style specificity (`exact > prefix > catchall`) instead of declaration order, regardless of where a `*` entry sits in the list.

**Architecture:** A stable compile-time sort of the entrypoint slice is applied before the gateway compiles matchers; all consumers that assumed declaration order as precedence (the inbound hop group, the count-tokens policy, the admin stats entrypoint filter) switch to the same sorted view. Routing behavior changes only for configs that had specifics shadowed by `*`.

**Tech Stack:** Go, `github.com/go-gost/{core,x}` hop/group (FIFO unchanged).

**Spec:** `docs/superpowers/specs/2026-11-16-entrypoint-longest-match-design.md`

## Global Constraints

- English only in code, comments, tests, docs.
- No secrets.
- `config.providerTypes`, `provider.New`, the web `PROVIDER_TYPES` list and `internal/admin/presets.go` must stay in sync (not touched here — no new type).
- `/api/entrypoints` still returns config order (display); sorting is a compile-time view only.
- Every task appends its implementation record to this plan file before committing.
- GOWORK=off for every Go command. Commit on `main`. Do not push.
- `config.Load` uses `yaml.KnownFields(true)` — unknown fields keep failing loudly (no new fields in this change).
- Entrypoints' `match` remains a model glob; the sort computes a score but adds no config knob.

## Review Focus

1. **Excluded-vs-winning mismatch:** the count-tokens policy and the admin stats `entrypoint=` filter compute precedence from the same list the gateway uses; if either still uses declaration order, its answer diverges from the actual routing. Must switch to `OrderEntrypoints` too.
2. **Multi-glob semantics:** summing glob literals would let a union glob bag outrank an exact match; score must be max across globs. Pinned in unit tests.
3. **Draft entrypoints:** `no routes` draft entries keep their compiled semantics ("earliest still shadows") only under equal specificity; test that a draft at `claude-fable*` ranks above a catchall (and that a catchall still beats a catchall regardless of which declared first).
4. **Stable tie-break:** identical specificity must preserve declaration order — otherwise round-trip confuses operators.
5. **Behavior change is the point:** the old config `*` before `claude-fable*` (this exact production incident) must route `claude-fable-5` to the specific entry. Pinned by a gateway integration test.
6. **No functional regression in web** — no web file is touched; `npm test`/`tsc` are a completeness gate only if anything else changes (skip otherwise).

## File Structure

- `internal/config/ordering.go` — new: `SpecificityScore`, `OrderEntrypoints`.
- `internal/config/ordering_test.go` — new.
- `internal/gateway/build.go` — sort before entry compilation.
- `internal/gateway/build.go` (`buildGateway` or its caller) — apply `OrderEntrypoints` once.
- `internal/gateway/count_tokens_policy.go` — use `OrderEntrypoints`.
- `internal/gateway/ordering_test.go` — new: mid-level tests of the gateway-level consumers? No — scoring lives in config; gateway consumers are covered by the integration test.
- `internal/gateway/build_order_test.go` — new: build with `*` first; standalone entrypoint shadowing test.
- `internal/admin/handlers_stats.go` — entrypoint filter excludes by specificity order.
- `README.md` — one paragraph under the routing section + note in the count_tokens block if it mentions order.

---

### Task 1: Stable specificity sort in internal/config

**Files:**
- Create: `internal/config/ordering.go`
- Create: `internal/config/ordering_test.go`

**Interfaces:**
- Produces: `func SpecificityScore(match []string) int` — max non-`*` literal count across globs.
- Produces: `func OrderEntrypoints(eps []Entrypoint) []Entrypoint` — new slice sorted by score desc, total asterisks asc, then original index; input untouched.

- [ ] **Step 1: Write the failing test**

```go
func TestOrderEntrypointsSpecificityThenDeclaration(t *testing.T) {
	eps := []Entrypoint{
		{ID: "catchall-early", Match: []string{"*"}},
		{ID: "prefix-later", Match: []string{"claude-*"}},
		{ID: "exact-later", Match: []string{"claude-fable-5"}},
		{ID: "multi-glob", Match: []string{"dental-*", "claude-fable-5-*"}},
		{ID: "second-catchall", Match: []string{"*"}},
	}
	got := []string{}
	for _, e := range OrderEntrypoints(eps) { got = append(got, e.ID) }
	want := []string{"multi-glob", "exact-later", "prefix-later", "catchall-early", "second-catchall"}
	if !slices.Equal(got, want) { t.Fatalf("order = %v, want %v", got, want) }
}

func TestGatewaySpecificityScoreMaxAcrossGlobs(t *testing.T) {
	// multi-glob max: 'claude-fable-5-*' carries 15 literal bytes vs 'dental-*' 7
	if got := SpecificityScore([]string{"dental-*", "claude-fable-5-*"}); got != 15 {
		t.Fatalf("score = %d, want 15", got)
	}
	if got := SpecificityScore(nil); got != 0 { t.Fatalf("nil match = %d, want 0", got) }
	if got := SpecificityScore([]string{"**", "*"}); got != 0 {
		t.Fatalf("all-wildcard match = %d, want 0", got)
	}
	if SpecificityScore([]string{"claude-fable-5"}) <= SpecificityScore([]string{"claude-fable*"}) {
		t.Fatal("exact must outrank prefix")
	}
}

func TestOrderEntrypointsStarCountTiebreak(t *testing.T) {
	eps := []Entrypoint{
		{ID: "starred", Match: []string{"a*"}},
		{ID: "plain", Match: []string{"a"}},
	}
	// equal literal count (1), fewer '*' wins
	got := []string{}
	for _, e := range OrderEntrypoints(eps) { got = append(got, e.ID) }
	if got[0] != "plain" || got[1] != "starred" {
		t.Fatalf("order = %v, want [plain starred]", got)
	}
}
```

(Note the multi-glob max: `claude-fable-5-*` is 14 literals — `dental-*` is 6, so max wins and the exact-15 alternative `claude-fable-5` beats both.)

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/config/ -run 'Specificity|OrderEntrypoint' -count=1 -v`
Expected: FAIL (undefined symbols).

- [ ] **Step 3: Implement**

```go
// SpecificityScore returns the most specific literal count across the globs
// in match: the number of non-'*' bytes of the single most specific glob.
// A request can satisfy at most one glob per entrypoint, so summing would
// let a redundant glob outscore an exact match.
func SpecificityScore(match []string) int

// OrderEntrypoints returns a copy of eps sorted by specificity (highest
// first): score descending, then fewer total '*' characters, then original
// position. The input is untouched; sorting exists so the compiled hop
// group matches by match"weight, URI routing style, instead of
// declaration order.
func OrderEntrypoints(eps []Entrypoint) []Entrypoint
```

- [ ] **Step 4: Run test to verify it passes**

Run the same command; expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/ordering.go internal/config/ordering_test.go
git commit -m "feat(config): stable specificity sort for entrypoints"
```

---

### Task 2: Gateway honors specificity order

**Files:**
- Modify: `internal/gateway/build.go` — apply `OrderEntrypoints` once before compiling entries; the loop iterates the sorted list instead of `cfg.Entrypoints`.
- Test: `internal/gateway/build_order_test.go` (new)

**Interfaces:**
- Consumes Task 1's `OrderEntrypoints`.

- [ ] **Step 1: Add failing integration test**

```go
func TestCatchallBeforeSpecificStillRoutesSpecific(t *testing.T) {
	// scratch config: catchall first, e_pc3 spread second with claude-fable*,
	// assert POST /v1/messages? model claude-fable-5 requests the route/entries
	// bound to the specific entrypoint, not the catchall's route.
}
```

(Fixture pattern: see `internal/gateway/protocol_auto_test.go` and/or `provider_capabilities_test.go`; assert the upstream hit lands on the node serving the specific route, i.e. which node got the request.)

- [ ] **Step 2: Run to verify it fails**

Expected: FAIL — hit the catchall's upstream.

- [ ] **Step 3: Apply the sort**

In `build.go`:
```go
eps := OrderEntrypoints(cfg.Entrypoints)
for _, e := range eps { ... }   // same body; every subsequent consumer sees specificity order
```
Import `gwconfig.OrderEntrypoints`/`SpecificityScore` if in config package is exported (same package name import alias gwconfig).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/gateway/ -run 'CatchallBeforeSpecific|gemini_type|TestProviderCapabilities' -count=1 -v` — expected: PASS including the gemini pin (proof no regression on its route precedence).

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/build.go internal/gateway/build_order_test.go
git commit -m "feat(gateway): match entrypoints by longest specificity, not file order"
```

Also run `GOWORK=off go test ./internal/gateway/ -count=1` per-package before commit (no wildcard hang).

---

### Task 3: count_tokens policy and admin stats filter honor specificity

**Files:**
- Modify: `internal/gateway/count_tokens_policy.go` — iterate `OrderEntrypoints(cfg.Entrypoints)`.
- Modify: `internal/admin/handlers_stats.go` — entrypoint filter excludes shadowing entrypoints in specificity order (same sorted slice; earlier in sorted order means higher precedence). Update the stale comment.
- Optional targeted unit test: buildCountTokensPolicy in which wildcard is declared first but the exact entrypoint needs estimate→returns estimate for the exact's models.

**Interfaces:**
- Consumes Task 1's `OrderEntrypoints` via gwconfig import.

- [ ] **Step 1: Write failing unit test** for `buildCountTokensPolicy` with `*`-catchall declared before an exact entrypoint whose bound route needs estimate: expect `estimateFor(exact-model) == true` (today returns false because wildcard-wins).
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Apply `OrderEntrypoints` in both files; update the stale comment** ("Order is precedence" stays true conceptually; `Earlier` now means more specific).
- [ ] **Step 4: Run `GOWORK=off go test ./internal/gateway/ ./internal/admin/ -count=1` → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "fix(gateway,admin): shadow entrypoint artifacts by specificity order"`.

---

### Task 4: Docs + gates

- [ ] **Step 1: README** — add a line under the config/routing section (README's routing description; short): entrypoints are matched by specificity (`exact > prefix > catchall`), declaration order is only the tie-break for equal specificity; note a `*` entry no longer shadows more specific entries regardless of where it is listed.

- [ ] **Step 2: Gates** — `GOWORK=off go build ./...` and `go test ./internal/config/ ./internal/gateway/ ./internal/admin/ -count=1`; `go vet ./internal/gateway/ ./internal/config/ ./internal/admin/`. No web changes — run `npm test` anyway per repo habit only if anything web-facing is touched (it isn't; note in the record).

- [ ] **Step 3: Commit** — `git commit -m "docs: entrypoints matched by specificity (README note)"`.

- [ ] **Step 4: Implementation record** — append per task (this plan file); dates/SHAs.

## Implementation record

(Per task: date/status, repo+SHA, files actually changed, tests and results, review findings, deferred items.)

### Task 1 - 2026-11-16 - status: complete

**Repository:** /root/code/go-gost/cuttle.
**Final commit:** (recorded in commit message / git log).
**Files changed:** `internal/config/ordering.go` (new: SpecificityScore max-literal rule, StarCount tiebreak, OrderEntrypoints stable sort), `internal/config/ordering_test.go` (new).
**Tests:** `GOWORK=off go test ./internal/config/ -run 'Specificity|OrderEntrypoints' -count=1 -v` → all PASS.
**Findings:** The plan's original test expectation (all-wildcard → declaration order) was wrong: `*` beats `**`+`*` on the fewer-stars tiebreak, so the test was corrected and a pure `*`-vs-`*` declaration-order case added.
**Deferred:** none.

### Task 2 - 2026-11-16 - status: complete

**Repository:** /root/code/go-gost/cuttle.
**Files changed:** `internal/gateway/build.go` (sort `cfg.Entrypoints` via `gwconfig.OrderEntrypoints` before compiling entries; comment), `internal/gateway/entrypoint_e2e_test.go` (renamed param, updated the reload section to assert specificity: `claude-opus-4-5` AND `gpt-5.5` still route to `strong-model` when the catchall is declared before it).
**Tests:** `GOWORK=off go test ./internal/gateway/ -count=1` → ok; `TestEntrypointConfigEndToEnd` → PASS (0.30s); `TestProviderCapabilities` → PASS.
**Deferred:** admin stats entrypoint filter and count-tokens policy ordering → Task 3.

### Task 3 - 2026-11-16 - status: complete

**Repository:** /root/code/go-gost/cuttle.
**Files changed:** `internal/gateway/count_tokens_policy.go` (policy now iterates `OrderEntrypoints`), `internal/admin/handlers_stats.go` (entrypoint filter's ClientModelExcludes now derived from specificity order; stale comment updated), `internal/gateway/count_tokens_policy_test.go` (new `TestBuildCountTokensPolicySpecificBeatsWildcardRegardlessOfOrder` — wildcard declared first must not shadow a specific passthrough binding).
**Tests:** `GOWORK=off go test ./internal/gateway/ -count=1` (incl. CountTokensPolicy group) PASS; `GOWORK=off go test ./internal/admin/ -count=1` PASS; `GOWORK=off go build ./...` ok; `go vet` on the three packages clean; gofmt none.
**Deferred:** none.
