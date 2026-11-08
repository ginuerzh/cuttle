# Sticky Route Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a stateless `sticky` route strategy (weighted rendezvous hash) so
`deepseek-flash` uses its 10:3 weights across sessions while one session stays
on one provider and keeps its prompt cache.

**Architecture:** x gains a `sticky` selector strategy and a client-ID hook in
the forward HTTP sniffer. cuttle supplies the client ID from the client's
session headers and selects `strategy: sticky` on the route. No affinity map,
TTL or persistence is introduced; the mapping is deterministic from the
session ID, the node names and the weights.

**Tech Stack:** Go 1.26 (x), Go 1.26 + React/Ant Design (cuttle), SQLite only
for existing stats.

**Spec:** `docs/superpowers/specs/2026-11-08-sticky-route-design.md`

## Global Constraints

- cuttle routing must keep using the x hop group and selector; do not add a
  custom dispatcher.
- Open mode (no `gateway.keys`) must not parse the request. Session extraction
  happens in the x sniffer after `http.ReadRequest`, not in the auth handler.
- cuttle `go.mod` must use tagged modules and no `replace`; run the cuttle gate
  with `GOWORK=off`.
- x changes are committed and tagged in `../x`; record that repository's
  commit in this plan. Do not push or tag without explicit approval.
- The x release also carries the HTTP dial-failure stats fix (`408ad41`).
- Keep `maxFails: 2` on `deepseek-flash`; the backup `deepseek-pi` stays.
- Never store session header values; hash them before they enter the context.

## Review Focus

- The same client ID and eligible set must always select the same node.
- Weights must produce `w_i / sum(w)`, not a 50/50 split.
- Removing a node must not move sessions that were not on it.
- Node order in the config must not change the mapping.
- An empty client ID must fall back to weighted random, not panic.
- The client ID must not be applied when the sniffer does not select a hop.
- Open mode must still be a pass-through with no extra head parse.

---

### Task 1: x weighted rendezvous `sticky` strategy

**Repository:** `../x`

**Files:**
- Create: `selector/sticky.go`
- Create: `selector/sticky_test.go`
- Modify: `selector/strategy.go` (share the weighted-random helper)
- Modify: `config/parsing/selector/parse.go` (map `sticky`)

**Interfaces:**
- Produces:
  - `func StickyHashStrategy[T any](key func(T) string) selector.Strategy[T]`
  - `case "sticky", "stickyhash"` in `ParseNodeSelector`, using
    `func(n *chain.Node) string { return n.Name }`
- Consumes: `xctx.ClientIDFromContext`, node metadata `weight`.

- [ ] **Step 1: Write the failing tests**

Cover: same client ID + same nodes returns the same node; 10,000 client IDs
with weights 10/3 split within 0.74–0.80 for the weight-10 node; removing a
node only moves the sessions that were on it; node order does not matter;
empty client ID returns a valid node; one node returns it; zero weight is
treated as 1.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./selector -run Sticky -count=1`
Expected: FAIL because `StickyHashStrategy` does not exist.

- [ ] **Step 3: Implement the strategy**

Use FNV-1a 64 over `clientID + "\x00" + key(node)`, map the top 53 bits to
`u` in `(0,1)`, and select the minimum `-ln(u)/weight`. Fall back to the
existing weighted-random selection when the client ID or key function is
empty. Keep the random strategy's behavior unchanged.

- [ ] **Step 4: Map the strategy name**

In `config/parsing/selector/parse.go`, add `sticky`/`stickyhash` to the
strategy switch with the `*chain.Node` name key.

- [ ] **Step 5: Run the tests**

Run: `GOWORK=off go test ./selector ./config/parsing/selector -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: x / `3788eef` (`feat(selector): add sticky weighted rendezvous strategy`)
- Files: `selector/sticky.go`, `selector/sticky_test.go`, `selector/strategy.go`, `config/parsing/selector/parse.go`
- Tests: `GOWORK=off go test ./selector ./config/parsing/selector -count=1` → PASS (8 sticky tests)
- Review findings/fixes: shared the weight lookup with the random strategy (`nodeWeight`); zero weight defaults to 1; empty client ID falls back to weighted random. FNV-1a's weak avalanche correlated the two scores for the real node names `r/oc/oc` and `r/cc/cc` and skewed the 10:3 split to 0.895; switched to SHA-256, which gives 0.769. The distribution test now uses the real node-name shape.
- Deferred: none.

---

### Task 2: x client-ID hook in the HTTP sniffer

**Repository:** `../x`

**Files:**
- Modify: `internal/util/sniffing/sniffer.go` (`HandleOptions.ClientID`,
  `WithClientID`)
- Modify: `internal/util/forwarder/sniffer_http.go` (apply after
  `http.ReadRequest`)
- Modify: `handler/forward/local/metadata.go` (`clientID` metadata)
- Modify: `handler/forward/local/sniffing.go` (pass `WithClientID`)
- Create/modify: `internal/util/forwarder/sniffer_test.go` (client ID reaches
  hop selection)

**Interfaces:**
- Produces:
  - `sniffing.WithClientID(func(*http.Request) string)`
  - `forward.local` metadata key `clientID` (a `func(*http.Request) string`)
- Consumes: `xctx.ContextWithClientID`.

- [ ] **Step 1: Write the failing test**

Use the existing `mockHop`/`configurableHop` test helpers. A sniffer with
`WithClientID(func(*http.Request) string { return "sess-1" })` and a hop whose
`Select` records the context must see `xctx.ClientIDFromContext == "sess-1"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/util/forwarder -run ClientID -count=1`
Expected: FAIL because the option does not exist.

- [ ] **Step 3: Implement the hook**

Add the field and option, apply it to `ctx` before `resolveHTTPNode`/hop
selection, and wire the forward handler metadata. Do not change the auth
handler or add a request parse.

- [ ] **Step 4: Run the tests**

Run: `GOWORK=off go test ./internal/util/forwarder ./handler/forward/local -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: x / `016912a` (`feat(forward): expose a client-ID hook for HTTP selection`)
- Files: `internal/util/sniffing/sniffer.go`, `internal/util/forwarder/sniffer_http.go`, `internal/util/forwarder/sniffer_test.go`, `handler/forward/local/metadata.go`, `handler/forward/local/sniffing.go`
- Tests: `GOWORK=off go test ./internal/util/forwarder ./handler/forward/local ./internal/util/sniffing -count=1` → PASS; `GOWORK=off go test -p 1 -count=1 ./...` → PASS.
- Review findings/fixes: the first test hung because the client side of `net.Pipe` was not closed before waiting for the handler; the test now closes it. The hook runs after `http.ReadRequest`, so open mode gains no extra parse.
- Deferred: none.

---

### Task 3: Release x

**Repository:** `../x`

- [ ] Push the Task 1+2 commits and tag the release. **Requires explicit
  user approval (push/tag/release).**
- [ ] Verify the tag resolves to the commit that contains both changes.
- [ ] Record the tag and commit.

**Implementation record**

- Date/status:
- Repository + commit/tag:
- Files: none
- Tests: `GOWORK=off go test ./... -count=1` before tagging
- Review findings/fixes:
- Deferred:

---

### Task 4: cuttle session ID and forward wiring

**Repository:** `cuttle` (depends on Task 3)

**Files:**
- Create: `internal/gateway/session.go`
- Create: `internal/gateway/session_test.go`
- Modify: `internal/gateway/gateway.go` (pass `clientID` metadata to
  `forwardlocal`)
- Modify: `go.mod`/`go.sum` (bump `github.com/go-gost/x` to the Task 3 tag)
- Add: `internal/gateway/sticky_test.go` (integration)
- Keep: `internal/gateway/stats_test.go::TestStatsRecordsDialFailure`

**Interfaces:**
- Produces: `clientIDFromRequest(*http.Request) string`, using the session
  header list and `x-claude-code-agent-id`.
- Consumes: `forward.local` metadata key `clientID`.

- [ ] **Step 1: Write the failing tests**

Session extraction: each known header; precedence; empty header; subagent
suffix changes the ID; raw values never appear in the result.
Integration: two `httptest` upstreams, route nodes with weights 10/3,
`strategy: sticky`; the same `x-claude-code-session-id` always reaches one
upstream; many sessions split about 10:3; a 5xx marks the node and the session
moves, then sticks to the new node.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'Session|Sticky' -count=1`
Expected: FAIL because `session.go` and the strategy wiring do not exist
(after the x bump, the strategy exists but cuttle does not pass a client ID).

- [ ] **Step 3: Implement session extraction and wiring**

Add `clientIDFromRequest`, pass it in the `forwardlocal` metadata, bump x to
the Task 3 tag. Do not touch `internal/gateway/auth.go`.

- [ ] **Step 4: Run the tests**

Run: `GOWORK=off go test ./internal/gateway -count=1`
Expected: PASS, including the dial-failure stats row.

**Implementation record**

- Date/status:
- Repository + commit:
- Files:
- Tests:
- Review findings/fixes:
- Deferred:

---

### Task 5: cuttle UI and docs

**Repository:** `cuttle`

**Files:**
- Modify: `web/src/pages/Edit.tsx` (both strategy selects)
- Modify: `README.md` (strategy lists)
- Modify: `AGENTS.md` (routing invariant)
- Test: `web` Vitest if a route form test exists; otherwise `npx tsc --noEmit`

- [ ] **Step 1: Add `sticky` to the route editor options**
- [ ] **Step 2: Update README/AGENTS strategy lists**
- [ ] **Step 3: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS; rebuild `internal/admin/dist`.

- [ ] **Step 4: Run the cuttle Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

**Implementation record**

- Date/status:
- Repository + commit:
- Files:
- Tests:
- Review findings/fixes:
- Deferred:

---

### Task 6: Deploy and switch the live route

**Repository:** `cuttle` + k3s `pi`

- [ ] Push cuttle `main`; wait for the Docker workflow.
- [ ] `kubectl -n llm rollout restart deploy/llm-gw`; wait for ready.
- [ ] Verify `/api/health` version and the image digest.
- [ ] Edit `/data/cuttle.yaml` (backup first) so `deepseek-flash` uses
  `strategy: sticky`; keep `maxFails: 2`; `kill -HUP 1`.
- [ ] Verify `config reloaded`, health OK, and that sessions distribute across
  `oc-go-dsf` and `cc-dsf` (stats by node) without per-request bouncing.
- [ ] Record the image digest, pod and observed distribution.

**Requires explicit user approval for push/deploy.**

**Implementation record**

- Date/status:
- Repository + commit:
- Files: `/data/cuttle.yaml` (backup path)
- Tests/commands:
- Review findings/fixes:
- Deferred:
