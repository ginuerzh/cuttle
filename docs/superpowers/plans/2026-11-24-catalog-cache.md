# Catalog Limits Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove synchronous provider catalog HTTP requests from admin configuration saves while preserving last-good `max_tokens` output caps.

**Architecture:** Add a gateway-owned, per-provider last-good cache with asynchronous, coalesced refreshes. Startup performs one bounded prime; `buildHopGroup` receives resolved limits and performs no catalog I/O. Gateway shutdown cancels and joins refreshes.

**Tech Stack:** Go, `modernc.org/sqlite` (unchanged), `net/http`, React console (unchanged).

**Spec:** `docs/superpowers/specs/2026-11-24-catalog-cache-design.md`

## Global Constraints

- Run Go commands with `GOWORK=off` inside `/root/code/go-gost/cuttle`.
- Code, comments, docs, and commit messages are English.
- No config schema, UI, dependency, or sibling-repository changes.
- Catalog failures remain best-effort and non-fatal.
- Every behavior change gets tests; do not weaken existing assertions.
- Commit after each completed task; never push.
- Each task must fill its Implementation record in this plan before the task commit.

## Review Focus

- A blocked or failed catalog refresh must retain the last good cap and never fail a build (Task 1).
- A cached reload must return without waiting for a stale catalog refresh (Task 2).
- Concurrent saves must coalesce to one in-flight refresh per provider (Task 1).
- One blocked provider must not cancel another provider's independent refresh (Task 1).
- Gateway shutdown must cancel outstanding refresh requests and wait for their goroutines (Task 1 and Task 2).

---

### Task 1: Add the last-good catalog limits cache

**Files:**
- Create: `internal/gateway/catalog_cache.go`
- Create: `internal/gateway/catalog_cache_test.go`
- Modify: `internal/gateway/catalog_limits.go`
- Modify: `docs/superpowers/plans/2026-11-24-catalog-cache.md`

**Interfaces:**
- Consumes: existing `gwconfig.Config`, `gwconfig.Provider`, `logger.Logger`, and provider catalog fetching behavior.
- Produces:
  - `newCatalogLimitsCache() *catalogLimitsCache`
  - `(*catalogLimitsCache).Prime(ctx context.Context, cfg *gwconfig.Config, log logger.Logger)`
  - `(*catalogLimitsCache).Limits(cfg *gwconfig.Config, log logger.Logger) catalogLimits`
  - `(*catalogLimitsCache).Close()`
  - Internal test hooks: `now func() time.Time`, `ttl`, `retryInterval`, and `timeout` fields.

- [x] **Step 1: Write failing cache tests**

Create `internal/gateway/catalog_cache_test.go` with these behaviors:

1. `TestCatalogLimitsCacheServesLastGoodWithoutWaiting`: prime a successful catalog, expire it, make the next request block, call `Limits`, and assert it returns the old cap in under 100 ms.
2. `TestCatalogLimitsCacheKeepsLastGoodAfterRefreshFailure`: prime success, expire it, return HTTP 500 on refresh, wait for the refresh, and assert the old cap remains.
3. `TestCatalogLimitsCacheCoalescesRefreshes`: call `Limits` twice for a missing provider whose request blocks, and assert exactly one catalog request starts.
4. `TestCatalogLimitsCacheRefreshesProvidersIndependently`: block provider A while provider B succeeds, and assert B's cap is cached after both bounded refreshes finish.
5. `TestCatalogLimitsCacheCloseCancelsRefresh`: leave a catalog request blocked and assert `Close` cancels it and returns.

- [x] **Step 2: Run the new tests and verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run TestCatalogLimitsCache -count=1`
Expected: FAIL because `catalogLimitsCache` and its methods do not exist.

- [x] **Step 3: Implement the cache and shared catalog fetch helpers**

Create `internal/gateway/catalog_cache.go` with the interfaces above. Use a ten-minute TTL, a one-minute retry interval after failed/missing attempts, and a five-second per-provider refresh timeout. `Limits` must deep-copy maps, mark in-flight work under the mutex, launch refreshes after unlocking, and retain old limits on error. `Close` cancels the cache context and waits for its `WaitGroup`.

Refactor `catalog_limits.go` only as needed to share provider/model selection and one-provider fetch logic with the cache. Keep `fetchCatalogLimits` behavior and warnings intact for existing callers/tests.

- [x] **Step 4: Run cache and existing catalog tests**

Run: `GOWORK=off go test ./internal/gateway -run 'TestCatalogLimitsCache|TestFetchCatalogLimits' -count=1`
Expected: PASS.

- [x] **Step 5: Run the race detector on the cache tests**

Run: `CGO_ENABLED=1 GOWORK=off go test -race ./internal/gateway -run TestCatalogLimitsCache -count=1`
Expected: PASS with no race reports.

- [x] **Step 6: Record and commit Task 1**

Fill the Task 1 Implementation record, then commit the cache, tests, shared fetch refactor, design, plan, and record together with:

```bash
git add internal/gateway/catalog_cache.go internal/gateway/catalog_cache_test.go internal/gateway/catalog_limits.go docs/superpowers/specs/2026-11-24-catalog-cache-design.md docs/superpowers/plans/2026-11-24-catalog-cache.md
git commit -m "perf(gateway): cache catalog limits outside reloads"
```

### Task 2: Move gateway builds onto the cache

**Files:**
- Modify: `internal/gateway/build.go`
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/build_cap_test.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/bindings_test.go`
- Modify: `internal/gateway/reload_test.go`
- Modify: `docs/superpowers/plans/2026-11-24-catalog-cache.md`

**Interfaces:**
- Consumes: `catalogLimitsCache` and its `Prime`, `Limits`, and `Close` methods from Task 1.
- Produces:
  - `buildHopGroup(cfg *gwconfig.Config, build string, limits catalogLimits, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (hop.Hop, *probeSet, []string, *countTokensPolicy, error)`
  - `Gateway.catalogs *catalogLimitsCache`

- [x] **Step 1: Write the failing reload-latency test**

Add `TestReloadDoesNotWaitForCatalogRefresh` to `internal/gateway/reload_test.go`. Use a catalog server that succeeds during `New`/prime and blocks on later calls. Reload a config that introduces a new provider id so the missing cache entry triggers the background path, then assert the reload returns in under 250 ms while the refresh remains outstanding until `gw.Close`.

- [x] **Step 2: Run the new test and verify it fails**

Run: `GOWORK=off go test ./internal/gateway -run TestReloadDoesNotWaitForCatalogRefresh -count=1`
Expected: FAIL because production `buildHopGroup` still performs the blocking catalog fetch.

- [x] **Step 3: Make builds consume resolved limits**

Change `buildHopGroup` to the signature above and remove its catalog context/fetch. Thread the supplied `limits` into the existing `buildRoutePlan` calls. Update the three direct test callers: cap test fetches or supplies its known limit map; unrelated builds pass `nil`.

- [x] **Step 4: Own the cache in Gateway lifecycle**

Add `catalogs *catalogLimitsCache` to `Gateway`, initialize it in `New`, call `Prime(context.Background(), cfg, log)` before the first `apply`, use `Limits(cfg, g.log)` in `apply`, and close the cache in `Close`. Close the cache on `New` error paths that do not otherwise call `Gateway.Close`.

- [x] **Step 5: Run focused gateway tests**

Run:

```bash
GOWORK=off go test ./internal/gateway -run 'TestReloadDoesNotWaitForCatalogRefresh|TestBuildCapReachesRegisteredRewriter|TestBuildUsesEntityIDs|TestReload' -count=1
```

Expected: PASS.

- [x] **Step 6: Run the full Go and web gates**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web
npm test
npx tsc --noEmit
npm run build
```

Expected: all PASS. Rebuild the embedded web bundle before the final Go build if it changed.

- [x] **Step 7: Record and commit Task 2**

Fill the Task 2 Implementation record, then commit all Task 2 files and the updated plan with:

```bash
git add internal/gateway/build.go internal/gateway/gateway.go internal/gateway/build_cap_test.go internal/gateway/gateway_test.go internal/gateway/bindings_test.go internal/gateway/reload_test.go docs/superpowers/plans/2026-11-24-catalog-cache.md
git commit -m "perf(admin): keep catalog refresh off the save path"
```

---

## Implementation records

### Task 1: Add the last-good catalog limits cache

- Date/status: 2026-11-24 complete
- Repository + commit SHA: `cuttle` `d84a760b`
- Files actually changed: `internal/gateway/catalog_cache.go`, `internal/gateway/catalog_cache_test.go`, `internal/gateway/catalog_limits.go`, `docs/superpowers/specs/2026-11-24-catalog-cache-design.md`, `docs/superpowers/plans/2026-11-24-catalog-cache.md`
- Tests/commands and results: cache tests failed RED with undefined `newCatalogLimitsCache`; focused cache/catalog tests PASS; `CGO_ENABLED=1 GOWORK=off go test -race ./internal/gateway -run TestCatalogLimitsCache -count=1` PASS; full `GOWORK=off go test ./... -count=1` PASS.
- Review findings and fixes: self-review confirmed deep-copy boundaries, per-provider in-flight coalescing, retry backoff, last-good retention, cancellation, and idempotent close; no defects found.
- Deferred items/limitations: none.

### Task 2: Move gateway builds onto the cache

- Date/status: 2026-11-24 complete
- Repository + commit SHA: `cuttle` `6502cb0b`
- Files actually changed: `internal/gateway/build.go`, `internal/gateway/gateway.go`, `internal/gateway/build_cap_test.go`, `internal/gateway/gateway_test.go`, `internal/gateway/bindings_test.go`, `internal/gateway/reload_test.go`, `docs/superpowers/specs/2026-11-24-catalog-cache-design.md`, `docs/superpowers/plans/2026-11-24-catalog-cache.md`
- Tests/commands and results: reload latency test failed RED at 5.004s; after cache wiring it passed under 250ms. `TestGatewayUsesCachedCatalogLimits` failed when cache wiring was mutated to nil (`max_tokens = 256000, want 131072`) and passed after restore. Focused gateway tests, `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, full `GOWORK=off go test ./... -count=1`, `npm test -- --run` (505 tests), `npx tsc --noEmit`, and `npm run build` all PASS.
- Review findings and fixes: added production-path cap coverage beyond the latency test; verified deep-copy/cache wiring and shutdown lifecycle; no remaining defects found in self-review.
- Deferred items/limitations: a brand-new provider can lack a cap until its first background refresh completes and a later reload consumes it; offline provider type/base-URL edits can retain old caps until the cache TTL expires because the admin API forbids those edits.

## Final review fixes

- Date/status: 2026-11-24 complete (task code commits `d84a760b`, `6502cb0b`; fix commit recorded below)
- Repository + commit SHA: `cuttle` `138dcc6a`
- Files actually changed: `internal/gateway/catalog_cache.go`, `internal/gateway/catalog_cache_test.go`, `internal/gateway/catalog_limits.go`, design, plan
- Findings fixed: a newly published model on an already-cached provider now triggers a refresh even while the entry is fresh; a provider fetch fingerprint (type, base URLs, keys/auth/headers, excluding display name and local model overrides) invalidates stale limits after live edits such as a generic provider base-URL change; Prime no longer arms the retry gate for providers skipped after the shared startup budget expired.
- Tests/commands and results: three review tests failed RED first (`TestCatalogLimitsCacheRefreshesForNewModel`, `TestCatalogLimitsCacheInvalidatesOnProviderChange`, `TestCatalogLimitsCachePrimeDoesNotGateUnqueriedProvider`); after fixes, focused cache tests and `CGO_ENABLED=1 GOWORK=off go test -race ./internal/gateway -run TestCatalogLimitsCache -count=1` PASS.
- Deferred minors: Prime closed guard, post-close read diagnostic, deep-copy/retry-gate unit tests, `fetchCatalogLimits` test-seam cleanup, pre-existing stats-store leak on `h.Init` failure.
