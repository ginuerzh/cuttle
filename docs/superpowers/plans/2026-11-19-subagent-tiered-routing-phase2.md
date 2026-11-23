# Subagent Route Bindings (Phase 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an entrypoint route binding declare `when: subagent`, so the gateway can send subagent traffic and lead traffic to different routes — the mechanism only; what each route carries stays a configuration choice.

**Architecture:** `when` compiles into an `xrouting.Matcher` on the existing `xhop.HopEntry` (no x change, no dispatcher). The marker table already exists in `internal/agent` from Phase 1; Phase 2 adds the rule generator so the dispatch rule and the recorded class cannot drift. `bindingOrder` gains a class tier as its primary sort key, mirrored by `probePriorityOrder` so health cannot disagree with the data plane. A separate, independent task adds per-client marker health so a harness that stops sending its marker is visible.

**Tech Stack:** Go 1.27, `github.com/go-gost/x v0.32.0` (`xrouting`, `xhop`), SQLite stats store, React + Ant Design console with Vitest.

**Spec:** `docs/superpowers/specs/2026-11-19-subagent-tiered-routing-design.md` (Phase 2 sections 4–10).

## Global Constraints

- `github.com/go-gost/x v0.32.0`; **no x change, no sibling-repository change, no `go.mod` bump, no local `replace`**.
- No config schema change beyond `RouteBinding.When`. `yaml.KnownFields(true)` stays; an unknown `when` value is rejected with the entrypoint name and binding index.
- `when` accepts only `""` (absent = any request) and `"subagent"`. There is no `lead` value (Decision 2).
- An entrypoint must keep at least one binding without `when`, or validation fails: lead traffic would otherwise have no binding and x would answer 502. Draft entrypoints (zero bindings) are exempt.
- Existing configs are a strict no-op: with no `when` anywhere, every compiled matcher is `nil` and behavior is byte-identical.
- The class tier sits **above** the probe tier in `bindingOrder` and `probePriorityOrder`; both must stay in agreement, and a test asserts it.
- Marker health (`GET /api/subagent/health`) is **read-only observation**: it never changes a routing decision.
- Raw header values never reach a request row, a log, or an API payload (Phase 1 rule, unchanged).
- Code, comments, docs and commit messages are English. Console source strings are Chinese, translated in `web/src/locales/en-US.ts`.
- Run every Go command with `GOWORK=off` from `cuttle/`. Run the web gate (`npm test`, `npx tsc --noEmit`, `npm run build`) before claiming a web task done.
- Commit after each completed task, with that task's Implementation record in the same commit. Never push.

## Review Focus

- **A config with `when: subagent` and no unconditional binding must be rejected.** Otherwise every lead request reaches zero eligible bindings and x answers 502 — the operator's main session breaks the moment they save the config. Task 1 test `TestValidateServability`.
- **A draft entrypoint (zero bindings) must stay valid.** An existing hand-written config with `routes: []` must not start failing validation on upgrade. Task 1 test `TestValidateDraftExempt`.
- **A config with no `when` at all must compile to `nil` matchers and behave identically.** The whole fleet depends on this being a no-op. Task 2 test `TestBuildNoWhenIsNilMatcher`.
- **An unknown `when` value must be rejected, never ignored.** `when: lead` or a typo silently routing by class would be a silent behavior change. Task 1 test `TestValidateUnknownWhen`.
- **Health `bindings[]` order and `active` must agree with the data plane after the class tier.** A console that highlights a binding the gateway would not use is worse than no console. Task 3 test `TestOrderAgreement`.

---

### Task 1: `when` config field and validation

**Files:**
- Modify: `cuttle/internal/config/config.go` (`RouteBinding`, `expandEnv`, `applyDefaults`, `Validate`)
- Test: `cuttle/internal/config/config_test.go`

**Interfaces:**
- Produces: `RouteBinding.When string` with `yaml:"when,omitempty" json:"when,omitempty"`.
- Produces: `const BindingWhenSubagent = "subagent"` (exported, so the gateway and the admin layer name it once).
- Produces: validation rules — unknown `when` rejected; at least one binding without `when` required unless the entrypoint has zero bindings.

- [ ] **Step 1: Write the failing validation tests**

```go
func TestValidateUnknownWhen(t *testing.T) {
	cfg := validConfigWithBindings(t, []config.RouteBinding{
		{Route: "r_0000000001", When: "lead"},
		{Route: "r_0000000002"},
	})
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `when must be "" or "subagent"`) {
		t.Fatalf("err = %v, want a when rejection", err)
	}
	if !strings.Contains(err.Error(), `routes[0]`) {
		t.Fatalf("err = %v, want the binding index", err)
	}
}

func TestValidateServability(t *testing.T) {
	cfg := validConfigWithBindings(t, []config.RouteBinding{
		{Route: "r_0000000001", When: "subagent"},
	})
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one binding without when") {
		t.Fatalf("err = %v, want a servability rejection", err)
	}
}

func TestValidateDraftExempt(t *testing.T) {
	cfg := validConfigWithBindings(t, nil)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("draft entrypoint must stay valid, got %v", err)
	}
}

func TestValidateWhenAccepted(t *testing.T) {
	cfg := validConfigWithBindings(t, []config.RouteBinding{
		{Route: "r_0000000001", When: "subagent"},
		{Route: "r_0000000002"},
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
```

`validConfigWithBindings` is a small local helper building a minimal valid config (one provider, two nodes, two routes, one entrypoint with the given bindings) in the style of the existing config tests; add it once at the bottom of the test file if a similar helper does not already exist.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/config -run 'TestValidateUnknownWhen|TestValidateServability|TestValidateDraftExempt|TestValidateWhenAccepted' -count=1`
Expected: FAIL with `unknown field When in struct literal of type config.RouteBinding`.

- [ ] **Step 3: Implement the field and the rules**

Add to `RouteBinding`:

```go
// When limits the binding to one traffic class. Empty means any request.
// The only non-empty value is BindingWhenSubagent: subagent traffic then
// prefers this binding, and every other request uses the bindings without it.
When string `yaml:"when,omitempty" json:"when,omitempty"`
```

Add `const BindingWhenSubagent = "subagent"` beside it.

In `applyDefaults`, trim `b.When` next to `b.Route`.

In `Validate`, inside the existing per-binding loop, after the route-reference and duplicate checks:

```go
if b.When != "" && b.When != BindingWhenSubagent {
    return fmt.Errorf(`%s: when must be "" or %q`, scope, BindingWhenSubagent)
}
```

After the per-binding loop and before the end of the entrypoint block, add the servability rule: when `len(e.Routes) > 0`, require at least one binding whose `When` is empty, else

```go
return fmt.Errorf("entrypoint %q: at least one binding without when is required", name)
```

`expandEnv` must not touch `When` (it is an enum, not a reference).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/config -count=1`
Expected: PASS (including the existing schema tests).

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/config/config.go internal/config/config_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(config): add the subagent-only route binding"
```

**Implementation record — Task 1**

- Date/status: 2026-11-19, DONE. `when` is in the schema, validated, and both failure modes are pinned.
- Repository/commit: `cuttle` (`/root/code/go-gost/cuttle/.worktrees/feat-phase2`, branch `feat/subagent-routing-phase2`), implementation commit `969b607e`; this record follows it.
- Files changed: `internal/config/config.go` (`RouteBinding.When`, `BindingWhenSubagent`, trim in `applyDefaults`, the `when` enum check and the servability check in `Validate`); `internal/config/config_test.go` (`bindingsFixture` helper plus `TestValidateUnknownWhen`, `TestValidateServability`, `TestValidateDraftExempt`, `TestValidateWhenAccepted`).
- Tests/commands and results: RED — `GOWORK=off go test ./internal/config -run 'TestValidateUnknownWhen|TestValidateServability|TestValidateDraftExempt|TestValidateWhenAccepted' -count=1` → FAIL (`unknown field When in struct literal of type RouteBinding`). GREEN — same command → all four PASS. Package gate — `GOWORK=off go test ./internal/config -count=1` → `ok ... 0.723s`; `gofmt -l internal/config` → no output.
- Review findings and fixes: the plan sketched `cfg.Validate()` but this repository exposes `config.Validate(cfg)` as a function; the tests use the real signature. The servability check runs after the per-binding loop so a draft (`len(e.Routes) == 0`) is exempt, and an entrypoint whose bindings are all conditioned names itself in the error.
- Deferred items/limitations: no `when: lead` value by design; `expandEnv` deliberately does not touch `When` (it is an enum, not a reference).

---

### Task 2: Matcher generation and the class tier in `bindingOrder`

**Files:**
- Modify: `cuttle/internal/agent/agent.go` (rule generator)
- Modify: `cuttle/internal/gateway/build.go` (`buildBindingsHop`, `bindingOrder`)
- Test: `cuttle/internal/agent/agent_test.go`, `cuttle/internal/gateway/build_test.go`

**Interfaces:**
- Consumes: `agent.Classify`, the `markers` table, `config.BindingWhenSubagent`.
- Produces: `agent.SubagentRule() string` — the x routing rule text, built from the same table.
- Produces: `bindingOrder` sorts class-conditioned bindings before unconditional ones, keeping the existing probed-before-probe-less rule inside each tier.

- [ ] **Step 1: Write the failing rule-generator test**

```go
func TestSubagentRuleMatchesEveryMarker(t *testing.T) {
	m, err := xrouting.NewMatcher(agent.SubagentRule())
	if err != nil {
		t.Fatalf("rule does not parse: %v", err)
	}
	for _, h := range []string{"x-claude-code-agent-id", "x-parent-session-id", "x-openai-subagent"} {
		req := &routing.Request{Header: http.Header{}}
		req.Header.Set(h, "v")
		if !m.Match(req) {
			t.Fatalf("rule missed %q", h)
		}
	}
	if m.Match(&routing.Request{Header: http.Header{}}) {
		t.Fatal("rule matched a marker-less request")
	}
	// An empty-valued marker must match: x tests map-key presence.
	req := &routing.Request{Header: http.Header{"X-Openai-Subagent": {""}}}
	if !m.Match(req) {
		t.Fatal("rule missed an empty-valued marker")
	}
}
```

- [ ] **Step 2: Write the failing order test**

```go
func TestBindingOrderClassTier(t *testing.T) {
	bindings := []gwconfig.RouteBinding{
		{Route: "a"},                                  // unconditional, probe-less
		{Route: "b", When: "subagent", Probe: &gwconfig.Probe{Command: "x"}}, // conditioned, probed
		{Route: "c", When: "subagent"},                 // conditioned, probe-less
		{Route: "d", Probe: &gwconfig.Probe{Command: "y"}}, // unconditional, probed
	}
	order, _ := bindingOrder(bindings)
	// conditioned first (probed then probe-less), then unconditional (probed then probe-less)
	if want := []int{1, 2, 3, 0}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/agent ./internal/gateway -run 'TestSubagentRule|TestBindingOrderClassTier' -count=1`
Expected: FAIL (`undefined: agent.SubagentRule`; order mismatch).

- [ ] **Step 4: Implement**

`internal/agent/agent.go`: add

```go
// SubagentRule returns the x routing rule that matches any subagent marker.
// It is generated from the same table Classify uses, so the dispatch decision
// and the recorded class cannot drift.
func SubagentRule() string
```

building `Header("<name>")` clauses joined with ` || ` in table order.

`internal/gateway/build.go`, in `buildBindingsHop`: for each binding, when `strings.TrimSpace(b.When) == gwconfig.BindingWhenSubagent`, compile `xrouting.NewMatcher(agent.SubagentRule())` **once per build** (cache it in a local, since the matcher is immutable) and pass it to `xhop.NewHopEntry(h, matcher, nil)`; otherwise pass `nil` as today.

`bindingOrder`: sort by class first (conditioned before unconditional), then by the existing probed-before-probe-less rule, then configuration order within each of the four groups. Keep the `unreachable` semantics: within the **unconditional** tier only, probe-less bindings after the first are unreachable, exactly as today; conditioned probe-less bindings are unreachable only within their own class tier, and the build keeps one warning per unreachable binding.

- [ ] **Step 5: Run the tests and the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/agent ./internal/gateway -count=1`
Expected: PASS. Then `GOWORK=off go build ./... && GOWORK=off go vet ./...` → clean.

- [ ] **Step 6: Write the no-op regression test**

```go
func TestBuildNoWhenIsNilMatcher(t *testing.T) {
	// Build a hop group from a config with no `when` anywhere, then assert
	// every inner HopEntry carries a nil matcher — the strict no-op guarantee.
}
```

Assert it through the compiled group's entries (an exported accessor or an in-package test that inspects `entries`). This test protects the fleet during rollout.

- [ ] **Step 7: Write the selection test**

Using two `httptest` upstreams and the existing gateway fixture style: a request with `x-openai-subagent` reaches the conditioned upstream; a request without it reaches the unconditional upstream; a dead probe on the conditioned binding falls back to the unconditional one. Name it `TestSelectionByClass`.

- [ ] **Step 8: Run the tests and commit**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway ./internal/agent -count=1`
Expected: PASS.

```bash
git -C /root/code/go-gost/cuttle add internal/agent/agent.go internal/agent/agent_test.go internal/gateway/build.go internal/gateway/build_test.go
git -C /root/code/go-gost/cuttle commit -m "feat(gateway): route subagent traffic by the marker table"
```

**Implementation record — Task 2**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 3: Health order agreement and the `when` field

**Files:**
- Modify: `cuttle/internal/gateway/bindingprobe.go` (`probePriorityOrder`, snapshot)
- Modify: `cuttle/internal/health/health.go` (`Binding.When`, `ActiveBinding` doc)
- Test: `cuttle/internal/gateway/bindingprobe_test.go`, `cuttle/internal/health` (types compile only)

**Interfaces:**
- Consumes: `config.BindingWhenSubagent`.
- Produces: `health.Binding.When string` with `json:"when,omitempty"`.
- Produces: `probePriorityOrder` applies the same class tier as `bindingOrder`.

- [ ] **Step 1: Write the failing agreement test**

```go
func TestOrderAgreement(t *testing.T) {
	// For a table of binding lists (mixed when/probe), assert that
	// bindingOrder(bindings) and probePriorityOrder(probes, hasProbe) produce
	// the same index sequence. This is the guard against the console and the
	// data plane disagreeing after the class tier.
}
```

Derive the probe slice from the same bindings (one `bindingProbe` per binding, `command` empty when the binding has no probe) so the two functions see the same input shape.

- [ ] **Step 2: Write the failing health-field test**

```go
func TestBindingHealthCarriesWhen(t *testing.T) {
	// Build an entrypoint with one `when: subagent` binding and one
	// unconditional binding; assert the snapshot's bindings[] carry
	// When="subagent" and When="" respectively, and that Active names the
	// first eligible unconditional binding.
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run 'TestOrderAgreement|TestBindingHealthCarriesWhen' -count=1`
Expected: FAIL (order differs; `When` field missing).

- [ ] **Step 4: Implement**

`bindingprobe.go`: `probePriorityOrder` takes the binding class into account. Since a `bindingProbe` does not know its binding's `when`, add the field to `bindingProbe` when the set is built (in `buildHopGroup`/the probe-set constructor) and sort by class first, mirroring `bindingOrder`.

`bindingprobe.go` snapshot: fill `health.Binding.When` from the probe's binding.

`health.go`: add `When string \`json:"when,omitempty"\`` to `Binding`, and extend the type doc to say a conditioned binding is not a candidate for `active` (which stays lead-scoped).

- [ ] **Step 5: Run the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -count=1 && GOWORK=off go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/gateway/bindingprobe.go internal/gateway/bindingprobe_test.go internal/health/health.go
git -C /root/code/go-gost/cuttle commit -m "feat(gateway): keep entrypoint health in the data-plane class order"
```

**Implementation record — Task 3**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Admin API round-trip for `when`

**Files:**
- Modify: `cuttle/internal/admin/handlers.go`, `cuttle/internal/admin/handlers_events.go` (payload passthrough)
- Test: `cuttle/internal/admin/entrypoint_test.go`, `cuttle/internal/admin/handlers_test.go`

**Interfaces:**
- Consumes: `config.RouteBinding.When`.
- Produces: `GET /api/entrypoints`, `PUT /api/entrypoints`, `PUT /api/config` and the `entrypointHealth` snapshot carry `when` verbatim.

- [ ] **Step 1: Write the failing round-trip test**

```go
func TestEntrypointWhenRoundTrip(t *testing.T) {
	// PUT /api/entrypoints with one `when: subagent` binding and one plain
	// binding; GET returns both with the same `when` values; a second PUT
	// that drops the field leaves both bindings unconditional.
}
```

Also assert `PUT /api/config` preserves `when` (the adopt-or-mint path must not drop a field it does not know).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -run TestEntrypointWhenRoundTrip -count=1`
Expected: FAIL (`when` absent from the response).

- [ ] **Step 3: Implement**

The field rides the config struct; if the admin layer builds its own binding payload, add `when` there. Check `handlers_events.go`'s entrypoint health snapshot uses `health.Binding`, which already carries `When` after Task 3.

- [ ] **Step 4: Run the package gate and commit**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

```bash
git -C /root/code/go-gost/cuttle add internal/admin internal/config
git -C /root/code/go-gost/cuttle commit -m "feat(admin): round-trip the binding traffic class"
```

**Implementation record — Task 4**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: Per-client marker health (`GET /api/subagent/health`)

**Files:**
- Create: `cuttle/internal/agent/ua.go`, `cuttle/internal/agent/ua_test.go`
- Create: `cuttle/internal/stats/subagenthealth.go`, `cuttle/internal/stats/subagenthealth_test.go`
- Create: `cuttle/internal/admin/handlers_subagent.go`, `cuttle/internal/admin/subagent_test.go`
- Modify: `cuttle/internal/admin/server.go` (route registration)
- Modify: `cuttle/internal/agent/agent.go` (expose the known sources with their client prefix)

**Interfaces:**
- Produces: `agent.ClientOf(userAgent string) string` — `"claude-code"`, `"opencode"`, `"codex"`, or `""`.
- Produces: `stats.Store.SubagentHealth(ctx, from, to time.Time) ([]stats.SubagentHealthRow, error)`.
- Produces: `stats.SubagentHealthRow{Source, Client string; Requests, Marked int64}`.
- Produces: `GET /api/subagent/health?from=&to=` → `[{"source","client","requests","marked","markedPct","status"}]`, `status ∈ {ok, absent, regressed}`.

- [ ] **Step 1: Write the failing user-agent test**

```go
func TestClientOf(t *testing.T) {
	cases := map[string]string{
		"claude-cli/2.1.328 (external, cli)": "claude-code",
		"opencode/latest/2.3.2/cli":          "opencode",
		"codex_cli_rs/0.167.1":               "codex",
		"Python-urllib/3.14":                 "",
		"":                                   "",
	}
	for ua, want := range cases {
		if got := agent.ClientOf(ua); got != want {
			t.Fatalf("ClientOf(%q) = %q, want %q", ua, got, want)
		}
	}
}
```

- [ ] **Step 2: Write the failing store test**

```go
func TestStoreSubagentHealth(t *testing.T) {
	// Insert: 3 opencode rows with source opencode, 1 opencode row with no
	// source, 2 claude-cli rows with source claude_code, 1 Python-urllib row
	// with no source. Assert: opencode {requests 4, marked 3}; claude-code
	// {2,2}; a client with no traffic is absent from the result (the admin
	// layer adds `absent` rows from the marker table); Python-urllib has no
	// client label so it contributes no row.
}
```

- [ ] **Step 3: Write the failing API test**

```go
func TestSubagentHealthStatus(t *testing.T) {
	// Seed rows so that one agent client has traffic+markers (ok), one has
	// traffic and zero markers (regressed), and one has no traffic (absent).
	// Assert the JSON status of each and that `markedPct` is 0 for regressed.
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/agent ./internal/stats ./internal/admin -run 'TestClientOf|TestStoreSubagentHealth|TestSubagentHealthStatus' -count=1`
Expected: FAIL (`undefined: agent.ClientOf`, etc.).

- [ ] **Step 5: Implement**

`internal/agent/ua.go`: a small ordered table of `{source, uaPrefix}` (`claude_cli→claude-cli`, `opencode→opencode`, `codex→codex`) and `ClientOf` doing a case-insensitive prefix match on the trimmed UA.

`internal/stats/subagenthealth.go`: one `SELECT` over the window grouping by `subagent_source` and by the UA-derived client. `SQLite has no Go function`, so either (a) select `subagent_source, user_agent, count(*)` grouped by both and fold the client label in Go, or (b) group by source only and by UA prefix with `LIKE` clauses. Prefer (a): one query, folding in Go keeps the mapping in `internal/agent` as the single source of truth.

`internal/admin/handlers_subagent.go`: parse the range with the existing `parseRange`, call the store, then union with the marker table so a client with zero traffic appears as `absent`; compute `status` and `markedPct`; return `[]` when empty.

**Explicitly**: this handler must not touch the hop group or the config. It is observation only.

- [ ] **Step 6: Run the package gate**

Run: `cd cuttle && GOWORK=off go test ./internal/agent ./internal/stats ./internal/admin -count=1 && GOWORK=off go build ./... && GOWORK=off go vet ./...`
Expected: PASS.

- [ ] **Step 7: Write the routing-independence test**

```go
func TestMarkerRegressionDoesNotChangeRouting(t *testing.T) {
	// Build the hop group for a config with `when: subagent`, record the
	// compiled matcher/entry shape, then call the marker-health path (which
	// must not mutate anything) and assert the shape is unchanged.
}
```

- [ ] **Step 8: Commit**

```bash
git -C /root/code/go-gost/cuttle add internal/agent internal/stats internal/admin
git -C /root/code/go-gost/cuttle commit -m "feat(admin): report per-client subagent marker health"
```

**Implementation record — Task 5**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: Console — class select and marker-health lines

**Files:**
- Modify: `cuttle/web/src/api/types.ts`, `cuttle/web/src/api/client.ts`
- Modify: `cuttle/web/src/pages/Entrypoints.tsx`, `cuttle/web/src/pages/Topology.tsx` (edge panel)
- Modify: `cuttle/web/src/locales/en-US.ts`
- Test: `cuttle/web/src/pages/Entrypoints.test.tsx`, `cuttle/web/src/pages/Entrypoints.dom.test.tsx`, `cuttle/web/src/api/client.test.ts`, `cuttle/web/src/i18n.test.ts`

**Interfaces:**
- Consumes: `RouteBinding.when`, `BindingHealth.when`, `GET /api/subagent/health`.
- Produces: a class select (不限 / 子代理) per binding row, a `when` badge on the topology edge label, and one marker-health line per client on the Entrypoints page.

- [ ] **Step 1: Write the failing tests**

- `client.test.ts`: `getSubagentHealth` sends `from`/`to` and returns the rows unchanged.
- `Entrypoints.test.tsx`: the binding editor state carries `when` through a reorder and a delete (pure helper, same style as the existing route-order tests).
- `Entrypoints.dom.test.tsx`: selecting 子代理 on a binding row and saving sends `when: 'subagent'` in the `PUT /api/entrypoints` body; the marker-health section renders a `regressed` row in the amber state and an `absent` row without it.
- `i18n.test.ts`: `子代理`, `不限`, `标记健康`, `未见到标记` translate.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle/web && npm test -- --run src/api/client.test.ts src/pages/Entrypoints.test.tsx src/pages/Entrypoints.dom.test.tsx src/i18n.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

`types.ts`: `RouteBinding.when?: '' | 'subagent'`; `BindingHealth.when?: string`; `SubagentHealthRow { source, client, requests, marked, markedPct, status }`.
`client.ts`: `getSubagentHealth(q)`.
`Entrypoints.tsx`: a per-row class select beside the route select, preserved through reorder/delete like `probe` is today; a marker-health block below the list with one line per client, `regressed` in amber, wording that the marker is expected only for agent clients.
`Topology.tsx`: the `entrypoint → route` edge panel gains the same select; the monitor edge label marks a conditioned binding.
`locales/en-US.ts`: both sides for every new string.

- [ ] **Step 4: Run the web gate**

Run: `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS, no type errors, bundle builds.

- [ ] **Step 5: Commit**

```bash
git -C /root/code/go-gost/cuttle add web/src
git -C /root/code/go-gost/cuttle commit -m "feat(web): edit the binding traffic class and show marker health"
```

**Implementation record — Task 6**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 7: Docs, `example.yaml`, and end-to-end verification

**Files:**
- Modify: `cuttle/README.md`, `cuttle/example.yaml`, `cuttle/AGENTS.md`
- Test: manual (recorded below)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Amend the routing invariants in `AGENTS.md`**

Replace the sentence that says only the request body's `model` field is matched, with the three-part statement: entrypoint precedence is the body `model` glob; binding choice consults the fixed cuttle-owned subagent marker table in addition to probe eligibility; paths still only select the upstream wire API. Also state the class-tier order and the servability rule.

- [ ] **Step 2: Document the feature in `README.md`**

A `when: subagent` section: the field, the semantics (conditioned bindings first, unconditional always serves, degradation on probe failure only), **what the mechanism is and what it is not** — the two routes are ordinary routes and the operator chooses what each carries (equal-price isolation, tiered model, tiered capacity) — the servability requirement, the cache caveat (route by class and phase shape, never per turn), and the marker-health endpoint including its false-positive behavior for non-agent clients.

- [ ] **Step 3: Add a commented two-class entrypoint to `example.yaml`**

Keeping the file loadable: it must still pass `config.Load` with the current schema.

- [ ] **Step 4: Verify end-to-end with the equal-price isolation scenario**

The acceptance scenario from the spec: two routes over the **same** model and provider, one bound with `when: subagent` and one unconditional. Drive a real subagent request (opencode child session) and a lead request; confirm each lands on its own route (check the recorded `route` on the request row), that `GET /api/entrypoints/health` reports the conditioned binding with `when: subagent` and `active` naming the unconditional one, and that `GET /api/subagent/health` reports `ok` for opencode. Record the actual route ids and request ids.

- [ ] **Step 5: Run both full gates**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Run: `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS both.

- [ ] **Step 6: Commit**

```bash
git -C /root/code/go-gost/cuttle add README.md example.yaml AGENTS.md
git -C /root/code/go-gost/cuttle commit -m "docs: document subagent route bindings and marker health"
```

**Implementation record — Task 7**

- Date/status:
- Repository/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

## Deferred

- `when: lead` or any second class value (Decision 2 keeps the single value).
- Per-agent matching (tightening `when` to a marker value).
- Phase inference from body signals and friction-driven escalation.
- Per-class rollups, charts, alerts and graph edges.
- A class-scoped `active` in the health payload.
- Filtering Sessions by `subagent_source`.
- Authenticating the marker headers.
