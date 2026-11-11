# Optional Entrypoint→Route and Node→Provider Connections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an entrypoint exist without a route and a node exist without a provider, as drafts that compile into the data plane, fail their traffic as unrouted, and can be wired up later from the topology canvas.

**Architecture:** Relax `config.Validate` for the two references; the data plane compiles an unconnected entrypoint against a shared zero-node hop (keeping its model globs in declaration order so nothing falls through) and skips providerless nodes when compiling a route. The console marks drafts, exposes `node→provider` as a draggable direction, and lets both draft-capable edges be disconnected.

**Tech Stack:** Go 1.27 (cuttle module, `GOWORK=off`), React + TypeScript + Vite + Ant Design + React Flow + Vitest (web console).

**Spec:** `docs/superpowers/specs/2026-11-11-optional-connections-design.md`

## Global Constraints

- Run every Go command in `cuttle/` with `GOWORK=off` (the parent workspace has a `go.work` that would otherwise mask the tagged modules).
- Keep `yaml.KnownFields(true)` and the `Load` order (decode → migrate → expand → defaults → validate) untouched.
- Entrypoint declaration order stays the only precedence rule: a draft entrypoint that matches must fail its request, never fall through.
- Names are display labels; every association stays an id. No name-keyed lookup may be introduced.
- `example.yaml` must keep loading unchanged; do not add required fields.
- Admin UI source strings are Chinese and every new one needs an `en-US` entry in `web/src/locales/en-US.ts`.
- Web gate per task: `cd web && npx vitest run <files>` then `npx tsc --noEmit`. Full gates before the last task's commit: `npm test`, `npm run build` (writes ignored `internal/admin/dist`), `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1`.
- Commit after each task (conventional message) with that task's Implementation record in this file; never push.
- Do not weaken existing assertions to make a change pass.

## Review Focus

1. A draft entrypoint declared before working entrypoints must not leak its models upstream — pinned by `TestDraftEntrypointDoesNotFallThrough` (Task 2).
2. `count_tokens` owned by a draft entrypoint must fail, not be answered by a local estimate — pinned by `TestDraftEntrypointCountTokensFails` and `TestBuildCountTokensPolicyDraftEntrypoint` (Task 2).
3. A route whose only node has no provider must answer a 5xx (502) instead of failing the reload — pinned by `TestDraftNodeRouteFails` (Task 2).
4. A hand-written YAML node with no provider but a pinned `key` (or a non-auto `api`) must be rejected with the node named — pinned by `TestValidateProviderlessNodeRejectsKey` / `...RejectsAPI` (Task 1).
5. Disconnecting a node's provider in the console must clear the pinned key so reconnecting elsewhere cannot leave a key that belongs to another provider — pinned by `connectNodeProvider` / `disconnectNodeProvider` tests (Task 4).

---

### Task 1: Accept drafts in config validation and the admin API

**Files:**
- Modify: `internal/config/config.go` (`Entrypoint` doc comment, `Node` doc comment, the node loop in `Validate`, the entrypoint loop in `Validate`)
- Test: `internal/config/config_test.go`
- Test: `internal/admin/draft_test.go` (new)

**Interfaces:**
- Consumes: nothing new.
- Produces: the validation contract every later task relies on — `entrypoint.route` may be `""`; `node.provider` may be `""`, in which case `node.model` may also be `""` and `node.key` must be `""` and `node.api` must be `""`.

- [ ] **Step 1: Write the failing config tests**

Add to `internal/config/config_test.go` (next to `TestValidateEntrypoints`):

```go
func TestValidateAllowsDraftEntrypoint(t *testing.T) {
	c := topologyFixture()
	c.Entrypoints[0].Route = ""
	if err := Validate(c); err != nil {
		t.Fatalf("an entrypoint without a route should validate as a draft: %v", err)
	}
}

func TestValidateAllowsProviderlessNode(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Model = ""
	if err := Validate(c); err != nil {
		t.Fatalf("a node without a provider should validate as a draft: %v", err)
	}
}

func TestValidateDraftNodeKeepsModelHint(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	if err := Validate(c); err != nil {
		t.Fatalf("a providerless node may keep its model text: %v", err)
	}
}

func TestValidateProviderlessNodeRejectsKey(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Key = fixtureID(identity.ProviderKey, "key")
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "node") || !strings.Contains(err.Error(), "key") {
		t.Fatalf("err = %v, want the node rejected for a pinned key without a provider", err)
	}
}

func TestValidateProviderlessNodeRejectsAPI(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Model = ""
	c.Nodes[0].API = "chat"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "node") {
		t.Fatalf("err = %v, want the node rejected for a pinned api without a provider", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/config -run 'TestValidateAllowsDraft|TestValidateAllowsProviderless|TestValidateDraftNode|TestValidateProviderless' -count=1`
Expected: FAIL — `entrypoint "..." : route is required`, `node "...": provider is required`.

- [ ] **Step 3: Relax validation in `internal/config/config.go`**

In the node loop of `Validate` (the `provider := strings.TrimSpace(n.Provider)` block):

- keep the existing "unknown provider id" (`nameRefError`) check for a non-empty provider;
- add, for an empty provider, `fmt.Errorf("node %q: key requires a provider", name)` when `strings.TrimSpace(n.Key) != ""`, and `fmt.Errorf("node %q: api requires a provider", name)` when `n.API != ""`;
- move the `model is required` error behind `provider != ""` so a providerless node may have an empty model (a non-empty model is kept as a hint and never validated).
- keep the existing `api` enum check for names, and keep `weight >= 0` unconditional.

In the entrypoint loop, delete the `route is required` error and keep the `routeIDs[route]` lookup only for a non-empty route (a dangling non-empty id must still fail with `nameRefError`).

Update the `Entrypoint` and `Node` struct doc comments: the route/provider is optional and an empty reference is a draft that serves no traffic.

- [ ] **Step 4: Run the config tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/config -count=1`
Expected: PASS, including the unchanged `TestValidateEntrypoints` "unknown route" case and `TestValidateNodeRequiresModel`.

- [ ] **Step 5: Write the failing admin API test**

Create `internal/admin/draft_test.go`:

```go
func TestDraftNodeAndEntrypointAreAccepted(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	route := routeID(t, client, base, "fast")

	// A node with no provider and no model is a draft.
	if resp := doJSON(t, client, http.MethodPost, base+"/api/nodes", `{"name":"draft-node"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create draft node = %d, want 201", resp.StatusCode)
	}

	// An entrypoint with no route is a draft, in declaration order.
	body := `[{"name":"draft-entry","match":["mystery*"]},{"name":"fast","match":["gpt-4.5*"],"route":"` + route + `"}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put draft entrypoint = %d, want 204", resp.StatusCode)
	}

	// GET /api/config must show the drafts and round-trip through PUT unchanged.
	cfgResp := doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	cfgBody, err := io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, client, http.MethodPut, base+"/api/config", string(cfgBody), csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put config with drafts = %d, want 204", resp.StatusCode)
	}

	cfgResp = doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	var view struct {
		Entrypoints []config.Entrypoint `json:"entrypoints"`
		Nodes       []config.Node       `json:"nodes"`
	}
	if err := json.NewDecoder(cfgResp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	var draftNode *config.Node
	for i := range view.Nodes {
		if view.Nodes[i].Name == "draft-node" {
			draftNode = &view.Nodes[i]
		}
	}
	if draftNode == nil || draftNode.Provider != "" || draftNode.Model != "" {
		t.Fatalf("draft node = %+v, want an empty provider and model", draftNode)
	}
	if len(view.Entrypoints) == 0 || view.Entrypoints[0].Name != "draft-entry" || view.Entrypoints[0].Route != "" {
		t.Fatalf("entrypoints = %+v, want an unconnected draft first", view.Entrypoints)
	}
}
```

- [ ] **Step 6: Verify the admin test fails before Task 1's config change**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -run TestDraftNodeAndEntrypointAreAccepted -count=1`
Expected: FAIL with `create draft node = 400` (or `put draft entrypoint = 400`).

If Task 1's config change is already applied, temporarily revert `Validate`'s two relaxations with `git stash push internal/config/config.go` around this step, then `git stash pop`.

- [ ] **Step 7: Run the admin test to verify it passes**

Run: `cd cuttle && GOWORK=off go test ./internal/admin -run TestDraftNodeAndEntrypointAreAccepted -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/admin/draft_test.go
git commit -m "feat(config): accept an entrypoint without a route and a node without a provider"
```

**Implementation record (Task 1)** — fill in after the task is complete:
- Date/status: 2026-11-11 complete
- Repository + commit: cuttle `c2011f70` — `feat(config): accept an entrypoint without a route and a node without a provider`
- Files changed: `internal/config/config.go` (`Entrypoint`/`Node` doc comments, node loop: empty provider → reject `key`/`api`, require `model` only with a provider; entrypoint loop: empty route is a draft, dangling non-empty route still rejected), `internal/config/config_test.go` (5 new cases), `internal/admin/draft_test.go` (new; draft node + entrypoint create, `GET /api/config` → `PUT /api/config` round-trip).
- Tests/commands and results: `GOWORK=off go test ./internal/config -run 'TestValidateAllowsDraft|TestValidateAllowsProviderless|TestValidateDraftNode|TestValidateProviderless'` RED (4 failed: `route is required` / `provider is required`) then GREEN; the `api` case was strengthened from `node` to `node`+`api` after it passed against the old error. `GOWORK=off go test ./internal/admin -run TestDraftNodeAndEntrypointAreAccepted` RED with `internal/config/config.go` stashed (`create draft node = 400`) then GREEN. Whole packages: `GOWORK=off go test ./internal/config ./internal/admin -count=1` → ok / ok (15.4s).
- Review findings and fixes: the first `TestValidateProviderlessNodeRejectsAPI` assertion only matched `node`, so it passed against the old `provider is required` error; tightened to require `api` in the message before implementing.
- Deferred items/limitations: `node.model` on a providerless node is kept unvalidated as a hint (spec's settled call); the console clears it instead.

---

### Task 2: Compile drafts in the data plane

**Files:**
- Modify: `internal/gateway/build.go` (`buildHopGroup`, `buildRoutePlan`)
- Modify: `internal/gateway/count_tokens_policy.go` (`buildCountTokensPolicy`)
- Test: `internal/gateway/draft_test.go` (new)
- Test: `internal/gateway/count_tokens_policy_test.go`

**Interfaces:**
- Consumes: Task 1's validation contract.
- Produces: `const draftPlanKey = ""` in `build.go` — the `routePlans` key of the shared draft plan; `func buildDraftPlan(log logger.Logger) (*routePlan, error)` returning `hops: map[provider.API]hop.Hop{"": <zero-node hop>}` and `countTokensEstimate: false`.

- [ ] **Step 1: Write the failing gateway tests**

Create `internal/gateway/draft_test.go` (`package gateway`):

```go
func waitRow(t *testing.T, gw *Gateway) stats.Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := gw.Stats().Query(context.Background(),
			stats.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			return rows[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no request row recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDraftEntrypointDoesNotFallThrough(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []gwconfig.Entrypoint{
		{ID: "e_0000000003", Name: "draft", Match: []string{"*"}},
		{ID: "e_0000000004", Name: "fallback", Match: []string{"*"}, Route: testRouteID},
	}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 502", code)
	}
	if hit.path != "" {
		t.Fatalf("a draft entrypoint leaked to the later entrypoint's upstream: %q", hit.path)
	}
	row := waitRow(t, gw)
	if row.Route != "" || row.Status != http.StatusServiceUnavailable {
		t.Fatalf("row route/status = %q/%d, want empty/502 (unrouted)", row.Route, row.Status)
	}
}

func TestDraftNodeRouteFails(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Nodes[0].Provider = ""
	cfg.Nodes[0].Model = ""
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 502", code)
	}
	if hit.path != "" {
		t.Fatalf("a providerless node reached an upstream: %q", hit.path)
	}
}

func TestDraftEntrypointCountTokensFails(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Entrypoints[0].Route = ""
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages/count_tokens", `{"model":"anything","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("count_tokens status = %d, want 502 instead of a local estimate", code)
	}
	if hit.path != "" {
		t.Fatalf("count_tokens reached an upstream: %q", hit.path)
	}
}

func TestReloadAcceptsDraftConfig(t *testing.T) {
	up, _ := captureUpstream(t)
	gw := startGateway(t, testConfig(up.URL))

	draft := testConfig(up.URL)
	draft.Entrypoints = append(draft.Entrypoints, gwconfig.Entrypoint{
		ID: "e_0000000003", Name: "draft", Match: []string{"mystery*"},
	})
	draft.Nodes[0].Provider = ""
	draft.Nodes[0].Model = ""
	if err := gw.Reload(draft); err != nil {
		t.Fatalf("reload with drafts = %v, want success", err)
	}
}
```

Add to `internal/gateway/count_tokens_policy_test.go`:

```go
func TestBuildCountTokensPolicyDraftEntrypoint(t *testing.T) {
	cfg := &gwconfig.Config{
		Entrypoints: []gwconfig.Entrypoint{
			{ID: "e_0000000001", Name: "draft", Match: []string{"*"}},
			{ID: "e_0000000002", Name: "rest", Match: []string{"*"}, Route: "r_0000000002"},
		},
		Routes: []gwconfig.Route{{ID: "r_0000000002", Name: "r"}},
	}
	plans := map[string]*routePlan{"r_0000000002": {countTokensEstimate: true}}
	p, err := buildCountTokensPolicy(cfg, plans)
	if err != nil {
		t.Fatal(err)
	}
	if p.estimateFor("gpt-4") {
		t.Fatal("a draft entrypoint must pass through so the request fails, not estimate locally")
	}
}
```

Imports needed: `context`, `net/http`, `path/filepath`, `testing`, `time`, plus `gwconfig`, `stats`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run 'TestDraft|TestReloadAcceptsDraft|TestBuildCountTokensPolicyDraft' -count=1`
Expected: FAIL — `New` rejects the config with `unknown route ""` / `node "...": unknown provider ""`, or the requests return 200.

- [ ] **Step 3: Implement the draft compile path in `internal/gateway/build.go`**

- `buildRoutePlan`: after `cfg.Node(ref)` succeeds, `if strings.TrimSpace(n.Provider) == "" { log.Warnf("route %s node %s: no provider, skipped", r.Name, n.Name); continue }`. Leave the `unknown provider` error for a non-empty dangling id.
- Add `const draftPlanKey = ""` and `buildDraftPlan` that calls `hop_parser.ParseHop(&xconfig.HopConfig{Name: "route/draft", Selector: &xconfig.SelectorConfig{Strategy: "round", MaxFails: 1, FailTimeout: 10 * time.Second}}, log)` with no nodes, and returns that hop under the empty API key with `countTokensEstimate: false`.
- `buildHopGroup`: replace the `unknown route` early return with a branch on an empty route — build the draft plan once (lazily, stored in `routePlans[draftPlanKey]`) and use it for that entrypoint; log one `log.Warnf("entrypoint %s: no route, compiled as a draft", e.Name)` per draft entrypoint; a non-empty route that does not resolve still returns `fmt.Errorf("entrypoint %q: unknown route %q", ...)`. Only the entrypoint's fallback entry is appended for a draft (the loop already skips protocol variants absent from `plan.hops`).
- Update the function comment: the draft plan keeps the matcher order and fails at select time.

In `internal/gateway/count_tokens_policy.go`, resolve the plan key first: `key := ""`; if `cfg.Route(e.Route)` resolves, `key = r.ID`; otherwise a non-empty `e.Route` is the existing `unknown route` error. Then `plan, ok := plans[key]`, erroring when the key is absent.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -run 'TestDraft|TestReloadAcceptsDraft|TestBuildCountTokensPolicy|TestRouteWithOnlyDisabledNodes|TestEmptyEntrypoints' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the whole gateway package**

Run: `cd cuttle && GOWORK=off go test ./internal/gateway -count=1`
Expected: PASS (no regression in `TestUnmatchedModelIsUnrouted`, reload, retry, count-tokens or stats tests).

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/build.go internal/gateway/count_tokens_policy.go internal/gateway/draft_test.go internal/gateway/count_tokens_policy_test.go
git commit -m "feat(gateway): compile draft entrypoints and nodes as unrouted hops"
```

**Implementation record (Task 2)** — fill in after the task is complete:
- Date/status: 2026-11-11 complete
- Repository + commit: cuttle `6edbb29c` — `feat(gateway): compile draft entrypoints and nodes as unrouted hops`
- Files changed: `internal/gateway/build.go` (`draftPlanKey`, `buildDraftPlan` with a zero-node hop, `buildHopGroup` draft branch + warn, `buildRoutePlan` skips a providerless node), `internal/gateway/count_tokens_policy.go` (empty route → `estimate: false`, `strings` import), `internal/gateway/draft_test.go` (4 tests + `waitRow`), `internal/gateway/count_tokens_policy_test.go` (draft precedence case); the spec and this plan now say 502 instead of 503.
- Tests/commands and results: RED `go test ./internal/gateway -run 'TestDraft|TestReloadAcceptsDraft|TestBuildCountTokensPolicyDraft'` → 5 failures (`entrypoint "draft": unknown route ""`, `node "up": unknown provider ""`); after implementing, 3 failures remained on the status code (`status = 502, want 503`) → GREEN once the tests and docs pin 502; `go test ./internal/gateway -count=1` ok (10.0s); `go test ./internal/... -count=1` all ok.
- Review findings and fixes: x answers `502` (not 503) for a hop with no nodes (`errNodeNotAvailable`), so the draft failure status is 502; the tests and both documents were corrected rather than bending x. Direction (`match` still claims the model in declaration order, no fallthrough, unrouted row) confirmed by `TestDraftEntrypointDoesNotFallThrough`.
- Deferred items/limitations: a draft entrypoint's `count_tokens` is pass-through by construction (policy entry without `estimate`), so any future local-estimate feature must keep that branch.

---

### Task 3: Draw drafts on the topology

**Files:**
- Modify: `web/src/graph/build.ts`
- Modify: `web/src/graph/nodes.tsx`
- Modify: `web/src/pages/Topology.tsx` (`EdgeData` preserves the draft dash)
- Modify: `web/src/api/types.ts` (`Node.provider` and `Entrypoint.route` doc comments)
- Modify: `web/src/locales/en-US.ts`
- Test: `web/src/graph/build.test.ts`
- Test: `web/src/graph/nodes.test.tsx`

**Interfaces:**
- Consumes: nothing from earlier web tasks.
- Produces: `GraphNodeData.unconnected?: boolean`; `EdgeData.draft?: boolean` in both `graph/build.ts` and `pages/Topology.tsx`; `DraftBadge({ show }: { show?: boolean })` exported from `graph/nodes.tsx`; source string `未连接` translated as `Not connected`; node sub-label `未连接` for a providerless node and entrypoint sub-label `… → 未连接` for a routeless entrypoint.

- [ ] **Step 1: Write the failing graph tests**

Append to `web/src/graph/build.test.ts`:

```ts
it('marks a providerless node as a draft without a provider edge', () => {
  const draft = {
    ...cfg,
    nodes: [...cfg.nodes, { id: 'n_0000000003', name: 'draft', provider: '', model: '' }],
  };
  const { nodes, edges } = buildGraph(draft);
  const node = nodes.find((n) => n.id === 'node:n_0000000003');
  expect(node?.data.unconnected).toBe(true);
  expect(node?.data.sub).toContain('未连接');
  expect(edges.some((e) => e.id.startsWith('node:n_0000000003|'))).toBe(false);
});

it('marks a routeless entrypoint as a draft with a dashed amber unrouted edge', () => {
  const draft = {
    ...cfg,
    entrypoints: [...cfg.entrypoints, { id: 'e_0000000003', name: 'draft', match: ['mystery*'], route: '' }],
  };
  const { nodes, edges } = buildGraph(draft);
  expect(nodes.find((n) => n.id === 'entrypoint:e_0000000003')?.data.unconnected).toBe(true);
  expect(nodes.find((n) => n.id === 'entrypoint:e_0000000003')?.data.sub).toContain('未连接');
  const edge = edges.find((e) => e.id === 'entrypoint:e_0000000003->unrouted');
  expect(edge?.data?.color).toBe('#d97706');
  expect(edge?.data?.draft).toBe(true);
});
```

Append to `web/src/graph/nodes.test.tsx` (same SSR + `ReactFlowProvider` pattern as the `EntrypointNode` test; call `setActiveLang('zh-CN')` in the test):

```tsx
describe('DraftBadge', () => {
  it('marks an unconnected entrypoint and node', () => {
    setActiveLang('zh-CN');
    const entryProps = {
      id: 'entrypoint:e_0000000001',
      type: 'entrypoint',
      data: { kind: 'entrypoint', label: 'draft', unconnected: true },
      selected: false,
      isConnectable: false,
    } as unknown as NodeProps<GraphNode>;
    const entry = renderToStaticMarkup(
      <ReactFlowProvider>
        <EntrypointNode {...entryProps} />
      </ReactFlowProvider>,
    );
    expect(entry).toContain('data-status="draft"');
    expect(entry).toContain('未连接');

    const nodeProps = {
      id: 'node:n_0000000001',
      type: 'node',
      data: {
        kind: 'node',
        label: 'draft',
        unconnected: true,
        node: { id: 'n_0000000001', name: 'draft', provider: '', model: '' },
      },
      selected: false,
      isConnectable: false,
    } as unknown as NodeProps<GraphNode>;
    const node = renderToStaticMarkup(
      <ReactFlowProvider>
        <BindingNode {...nodeProps} />
      </ReactFlowProvider>,
    );
    expect(node).toContain('data-status="draft"');
    expect(node).toContain('未连接');
  });
});
```

- [ ] **Step 2: Run the web tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/graph/build.test.ts src/graph/nodes.test.tsx`
Expected: FAIL — `data.unconnected` is `undefined`, the sub-label has no `未连接`, and `data-status="draft"` is not rendered.

- [ ] **Step 3: Implement the graph markings**

- `web/src/graph/build.ts`: add `unconnected?: boolean` to `GraphNodeData` and `draft?: boolean` to `EdgeData`; import `translate as t` from `../i18n`.
  - node branch: `const p = providerById.get(n.provider)`, `sub: [p?.name ?? n.provider ?? '', n.model].filter(Boolean).join(' · ') || t('未连接')`, with `unconnected: !n.provider`; keep pushing the node→provider edge only when `p` exists.
  - entrypoint branch: `sub: \`${(e.match ?? []).join(', ')} → ${routeById.get(e.route)?.name ?? (e.route || t('未连接'))}\``, `unconnected: !e.route`; when the route is missing, push the edge to `unrouted` with `{ color: '#d97706', draft: true }` and `dashed` style.
  - `edge(...)`: add a trailing `dashed = false` parameter that sets `strokeDasharray: '6 4'`.
- `web/src/pages/Topology.tsx`: add `draft?: boolean` to the local `EdgeData` and make `decorateEdge` keep the dash when the edge is a draft: `const dashed = dead || data?.draft === true;` then `strokeDasharray: dashed ? '6 4' : undefined`.
- `web/src/graph/nodes.tsx`: export `DraftBadge` (absolutely positioned top-right, `data-status="draft"`, colour `#d97706`, text `t('未连接')`, same geometry as `NodeMoodBadge`); render `<DraftBadge show={data.unconnected} />` in `EntrypointNode` and `BindingNode` right after `NodeStats`.
- `web/src/api/types.ts`: the `Node.provider` comment becomes "Provider id this node binds; empty while drafting (the node never serves traffic)" and `Entrypoint.route` becomes "Route id this entrypoint points at; empty while drafting".
- `web/src/locales/en-US.ts`: add `'未连接': 'Not connected'`.

- [ ] **Step 4: Run the web tests to verify they pass**

Run: `cd cuttle/web && npx vitest run src/graph/build.test.ts src/graph/nodes.test.tsx src/pages/Topology.test.tsx`
Expected: PASS.

- [ ] **Step 5: Type-check**

Run: `cd cuttle/web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/graph/build.ts web/src/graph/build.test.ts web/src/graph/nodes.tsx web/src/graph/nodes.test.tsx web/src/pages/Topology.tsx web/src/api/types.ts web/src/locales/en-US.ts
git commit -m "feat(web): mark unconnected entrypoints and nodes as drafts"
```

**Implementation record (Task 3)** — fill in after the task is complete:
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Connect and disconnect drafts on the edit canvas

**Files:**
- Create: `web/src/pages/drafts.ts`
- Test: `web/src/pages/drafts.test.ts` (new)
- Modify: `web/src/pages/Edit.tsx` (`connect`, `EdgeForm`, `NodeForm`, `EntrypointForm`, the node and entrypoint create modals)
- Modify: `web/src/locales/en-US.ts`
- Test: `web/src/pages/Edit.test.tsx`
- Test: `web/src/pages/Edit.dom.test.tsx`

**Interfaces:**
- Consumes: Task 3's `unconnected` marking (display only) and Task 1's validation contract.
- Produces: `connectNodeProvider(node: Node, provider: Provider): Node`, `disconnectNodeProvider(node: Node): Node`, `disconnectEntrypointRoute(e: Entrypoint): Entrypoint` in `web/src/pages/drafts.ts`; `EdgeForm` exported from `Edit.tsx`; new source strings `未连接（草稿）` → `Not connected (draft)` and `未连接` → `Not connected`.

- [ ] **Step 1: Write the failing helper tests**

Create `web/src/pages/drafts.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { connectNodeProvider, disconnectEntrypointRoute, disconnectNodeProvider } from './drafts';
import type { Entrypoint, Node, Provider } from '../api/types';

const provider: Provider = {
  id: 'p_0000000001',
  name: 'p',
  type: 'generic',
  chat: 'https://a.example/v1',
  keys: [{ id: 'pk_0000000001', name: 'k', keyPreview: 'sk-...a', enabled: true }],
};

const draft: Node = { id: 'n_0000000001', name: 'draft', provider: '', model: '', key: '' };

describe('connectNodeProvider', () => {
  it('binds the provider and drops a key that does not belong to it', () => {
    const pinned: Node = { ...draft, key: 'pk_0000000002' };
    expect(connectNodeProvider(pinned, provider).provider).toBe('p_0000000001');
    expect(connectNodeProvider(pinned, provider).key).toBe('');
  });

  it('keeps a key the new provider owns and keeps the model hint', () => {
    const kept: Node = { ...draft, key: 'pk_0000000001', model: 'm1' };
    const next = connectNodeProvider(kept, provider);
    expect(next.key).toBe('pk_0000000001');
    expect(next.model).toBe('m1');
  });
});

describe('disconnectNodeProvider', () => {
  it('clears provider, model, api and key and keeps policy fields', () => {
    const node: Node = { id: 'n_0000000001', name: 'n', provider: 'p_0000000001', model: 'm1', api: 'chat', key: 'pk_0000000001', backup: true, weight: 3 };
    const next = disconnectNodeProvider(node);
    expect(next).toMatchObject({ id: 'n_0000000001', name: 'n', provider: '', model: '', key: '', backup: true, weight: 3 });
    expect(next.api).toBeUndefined();
  });
});

describe('disconnectEntrypointRoute', () => {
  it('clears the route and keeps match order', () => {
    const e: Entrypoint = { id: 'e_0000000001', name: 'e', match: ['a*', 'b*'], route: 'r_0000000001' };
    expect(disconnectEntrypointRoute(e)).toMatchObject({ id: 'e_0000000001', match: ['a*', 'b*'], route: '' });
  });
});
```

- [ ] **Step 2: Run the helper tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/pages/drafts.test.ts`
Expected: FAIL — the module `./drafts` does not exist.

- [ ] **Step 3: Implement `web/src/pages/drafts.ts`**

Three pure functions, no React, no API calls:
- `connectNodeProvider(node, provider)` → `{ ...node, provider: provider.id, key: provider.keys?.some((k) => k.id === node.key) ? node.key : '' }`.
- `disconnectNodeProvider(node)` → `{ ...node, provider: '', model: '', key: '', api: undefined }`.
- `disconnectEntrypointRoute(e)` → `{ ...e, route: '' }`.

- [ ] **Step 4: Run the helper tests to verify they pass**

Run: `cd cuttle/web && npx vitest run src/pages/drafts.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing canvas and form tests**

In `web/src/pages/Edit.test.tsx` add:

```tsx
describe('draft editing', () => {
  it('offers a clearable route select and no required marker for an entrypoint', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(
      <EntrypointForm entrypoint={{ id: 'e_0000000001', name: 'draft', match: ['*'], route: '' }} index={0} config={config} onSaved={async () => undefined} />,
    );
    expect(html).toContain('未连接（草稿）');
  });

  it('offers a clearable provider select for a node', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const node: Node = { id: 'n_0000000001', name: 'draft', provider: '', model: '' };
    const html = renderToStaticMarkup(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('未连接（草稿）');
  });
});

describe('EdgeForm', () => {
  const config = {
    providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', keys: [] }],
    nodes: [{ id: 'n_0000000001', name: 'n', provider: 'p_0000000001', model: 'm1' }],
    routes: [{ id: 'r_0000000001', name: 'r', nodes: [] }],
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], route: 'r_0000000001' }],
  } as unknown as ConfigResponse;

  it('can disconnect an entrypoint from its route', () => {
    const html = renderToStaticMarkup(<EdgeForm id="entrypoint:e_0000000001->route:r_0000000001" config={config} onSaved={async () => undefined} />);
    expect(html).toContain('断开');
  });

  it('can disconnect a node from its provider', () => {
    const html = renderToStaticMarkup(<EdgeForm id="node:n_0000000001|provider:p_0000000001" config={config} onSaved={async () => undefined} />);
    expect(html).toContain('断开');
  });
});
```

In `web/src/pages/Edit.dom.test.tsx`, extend the `../api/client` mock with the functions `Edit` touches (`updateNode` already mocked; add `putEntrypoints: vi.fn(async () => undefined)`), and mock the canvas so a test can fire a connection:

```tsx
vi.mock('./Topology', () => ({
  Topology: (props: { onConnect?: (c: { source: string; target: string }) => void }) =>
    props.onConnect ? (
      <button
        type="button"
        onClick={() => props.onConnect!({ source: 'node:n_0000000001', target: 'provider:p_0000000001' })}
      >
        connect-draft
      </button>
    ) : null,
}));
```

Then:

```tsx
it('connects a node to a provider by dragging', async () => {
  const config = {
    gateway: { listen: ':8787' },
    admin: { listen: '', sessionTTL: '' },
    providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', chat: 'https://a/v1', keys: [] }],
    nodes: [{ id: 'n_0000000001', name: 'draft', provider: '', model: '' }],
    routes: [{ id: 'r_0000000001', name: 'r', nodes: [] }],
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], route: 'r_0000000001' }],
  } as unknown as ConfigResponse;
  const { container } = render(<Edit config={config} onReload={async () => undefined} />);
  await act(async () => {
    buttonByText(container, 'connect-draft').click();
  });
  expect(updateNodeMock).toHaveBeenCalledWith('n_0000000001', expect.objectContaining({ provider: 'p_0000000001' }));
});
```

- [ ] **Step 6: Run the canvas and form tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/pages/Edit.test.tsx src/pages/Edit.dom.test.tsx`
Expected: FAIL — `EdgeForm` is not exported, the placeholder is absent, and `node→provider` reports `Unsupported connection direction`.

- [ ] **Step 7: Implement the edit-canvas behavior in `web/src/pages/Edit.tsx`**

- `connect`: keep the `route→node` and `entrypoint→route` branches; add `skind === 'node' && tkind === 'provider'` → look up the node and provider by id, `updateNode(node.id, connectNodeProvider(node, provider))`. Unknown kinds still throw `tr('不支持的连接方向')`.
- `EdgeForm`: export it. In the `entrypoint:` branch render the existing explanation plus a danger `断开` button that calls `putEntrypoints(config.entrypoints.map((e) => (e.id === entrypointID ? disconnectEntrypointRoute(e) : e)))` (parse `entrypointID` from the edge id before `->`), then `message.success(tr('已断开'))` and `onSaved()`. In the `node:…|provider:…` branch render the existing explanation plus a `断开` button that calls `updateNode(nodeID, disconnectNodeProvider(node))`.
- `NodeForm` and the node create modal: make the provider `Form.Item` non-required with `<Select allowClear placeholder={tr('未连接（草稿）')} …>`; when `onValuesChange` sees the provider cleared, clear `model`, `api` and `key` fields; make the model field's `required` rule depend on a chosen provider; clear `providerId` handling so `useProviderModels(undefined)` is a no-op (already the case).
- `EntrypointForm` and the entrypoint create modal: make the route `Form.Item` non-required with `allowClear` and placeholder `tr('未连接（草稿）')`; submit `route: v.route ?? ''`.
- Add `'未连接（草稿）': 'Not connected (draft)'` to `web/src/locales/en-US.ts` (the `未连接` key already exists from Task 3).

- [ ] **Step 8: Run the canvas and form tests to verify they pass**

Run: `cd cuttle/web && npx vitest run src/pages/Edit.test.tsx src/pages/Edit.dom.test.tsx src/pages/drafts.test.ts`
Expected: PASS.

- [ ] **Step 9: Type-check**

Run: `cd cuttle/web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 10: Commit**

```bash
git add web/src/pages/drafts.ts web/src/pages/drafts.test.ts web/src/pages/Edit.tsx web/src/pages/Edit.test.tsx web/src/pages/Edit.dom.test.tsx web/src/locales/en-US.ts
git commit -m "feat(web): connect and disconnect drafts on the edit canvas"
```

**Implementation record (Task 4)** — fill in after the task is complete:
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: Show `未连接` on the list pages

**Files:**
- Modify: `web/src/pages/Entrypoints.tsx`
- Modify: `web/src/pages/Nodes.tsx`
- Test: `web/src/pages/Nodes.test.tsx`
- Test: `web/src/pages/Entrypoints.test.tsx` (new)

**Interfaces:**
- Consumes: `t('未连接')` from Task 3.
- Produces: list rows that read `… → 未连接` (entrypoint) and `未连接 · model` (node) instead of an empty leading segment.

- [ ] **Step 1: Write the failing list tests**

Create `web/src/pages/Entrypoints.test.tsx` following the SSR pattern in `Nodes.test.tsx`:

```tsx
it('labels an unconnected entrypoint', () => {
  const html = renderToStaticMarkup(
    <Entrypoints config={{ providers: [], nodes: [], routes: [], entrypoints: [{ id: 'e_0000000001', name: 'draft', match: ['*'], route: '' }] } as unknown as ConfigResponse} />,
  );
  expect(html).toContain('未连接');
});
```

Add to `web/src/pages/Nodes.test.tsx`:

```tsx
it('labels a node without a provider', () => {
  const html = renderToStaticMarkup(
    <Nodes config={{ providers: [], nodes: [{ id: 'n_0000000001', name: 'draft', provider: '', model: '' }], routes: [], entrypoints: [] } as unknown as ConfigResponse} />,
  );
  expect(html).toContain('未连接');
});
```

- [ ] **Step 2: Run the list tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/pages/Nodes.test.tsx src/pages/Entrypoints.test.tsx`
Expected: FAIL — neither page renders `未连接`.

- [ ] **Step 3: Implement the list labels**

- `Entrypoints.tsx`: `subtitle={\`${(e.match ?? []).join(', ')} → ${entityLabel(e.route, routeNames) || t('未连接')}\`}`.
- `Nodes.tsx`: `subtitle={[providerName(n.provider) || t('未连接'), n.model].filter(Boolean).join(' · ')}`.

- [ ] **Step 4: Run the list tests to verify they pass**

Run: `cd cuttle/web && npx vitest run src/pages/Nodes.test.tsx src/pages/Entrypoints.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Entrypoints.tsx web/src/pages/Entrypoints.test.tsx web/src/pages/Nodes.tsx web/src/pages/Nodes.test.tsx
git commit -m "feat(web): label unconnected entrypoints and nodes on the list pages"
```

**Implementation record (Task 5)** — fill in after the task is complete:
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: Document drafts and run the full gates

**Files:**
- Modify: `README.md` (routing section)
- Modify: `AGENTS.md` (routing invariants; gotcha for the `*` draft blackhole)
- Test: `internal/config/config_test.go` (existing `example.yaml` load test must stay green)

**Interfaces:**
- Consumes: everything above.
- Produces: documentation only; no code.

- [ ] **Step 1: Update `AGENTS.md` routing invariants**

- Entrypoint bullet: "a display name, a non-empty list of client-model globs and an **optional** route id; an empty route is a draft that still claims its models in declaration order and fails every matching request as unrouted (502). `*` is an explicit catch-all."
- Node bullet: "A node binds **at most** one provider and one provider model: `provider` is optional, and a providerless node is a draft skipped at compile time (with a warning); a node with a provider still requires a model, and `key` requires a provider."
- Gotcha: "A draft entrypoint with `*` declared before working entrypoints blackholes every model by design (no fallthrough). The console tags it `未连接`; fix its route or delete it."

- [ ] **Step 2: Update the README routing section**

Add one paragraph after the routing diagram: an entrypoint without a route and a node without a provider are drafts — they are saved, shared and shown on the topology, serve no traffic, never fall through to a later entrypoint, and can be wired up by dragging `entrypoint→route` / `node→provider` in the edit canvas; the `*` caveat is called out. Update the console page table so the Entrypoints and Nodes rows mention drafts.

- [ ] **Step 3: Rebuild the web bundle and run the full gates**

```bash
cd cuttle/web && npm run build
cd .. && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit
```

Expected: every command green; `npm run build` rewrites the ignored `internal/admin/dist`.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md
git commit -m "docs: describe draft entrypoints and nodes"
```

**Implementation record (Task 6)** — fill in after the task is complete:
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

## Final verification

- [ ] `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
- [ ] `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
- [ ] Manual check against the spec: create a provider, a node without a provider, a route, an entrypoint without a route; confirm the topology tags both `未连接`, a request to the draft entrypoint answers 502 and the Sessions page shows it without a route; then drag `node→provider` and `entrypoint→route` and confirm real traffic flows.
- [ ] Confirm `example.yaml` still loads: `cd cuttle && GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
