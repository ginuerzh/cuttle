# Topology Layout Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store topology node positions in the server's SQLite database and share them across admin browsers, while keeping `localStorage` as a backup and fallback.

**Architecture:** The stats store gains a `topology_positions` table and load/replace methods. The admin API exposes the full position map under `/api/topology/positions`. The web topology reads the server map first, seeds it from the local backup when empty, and writes every layout change to both stores through a serialized save queue.

**Tech Stack:** Go 1.26, SQLite (modernc), React 18, React Flow, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-05-topology-layout-persistence-design.md`

## Global Constraints

- The server layout is one shared map for all admins; it is not per user.
- `localStorage` key stays `llm-gw.positions` and remains the backup/fallback.
- Server positions win when the server map is non-empty.
- When the server map is empty, a non-empty local map seeds the server once.
- Server read/write failures must not block local topology use.
- No YAML/config schema changes.
- Last full map written wins; no conflict resolution.
- Keep the position format `node ID -> {x, y}`.
- Commit after each completed task with its Implementation record.

## Review Focus

- Server empty and local backup present must seed the server, not discard the layout.
- Server read failure must keep rendering the local layout and show one warning.
- Rapid drag/reset saves must reach the server in call order; the last map must win.
- Reset must submit the freshly computed layout once, never leave the server empty after a successful reset.
- A non-empty server map must override a different local map on load.
- Invalid payloads (empty ID, oversized map) must return 400 without touching stored positions.
- Missing stats store must return 503, not panic.

---

### Task 1: SQLite storage and admin API

**Files:**
- Modify: `internal/stats/store.go` (schema in `migrate`, methods near the session methods)
- Modify: `internal/stats/store_test.go`
- Create: `internal/admin/handlers_topology.go`
- Modify: `internal/admin/server.go` (route registration)
- Create: `internal/admin/topology_test.go`

**Interfaces:**
- Produces:

```go
type Position struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

func (s *Store) LoadTopologyPositions(ctx context.Context) (map[string]Position, error)
func (s *Store) SaveTopologyPositions(ctx context.Context, positions map[string]Position) error
```

- Produces API routes:
  - `GET /api/topology/positions` -> `200 {"positions":{"entrypoint:fast":{"x":10,"y":20}}}`
  - `PUT /api/topology/positions` with the same body -> `204 No Content`
- Consumes: existing session auth middleware and `stats.Store` held by `admin.Server`.

- [ ] **Step 1: Write the failing stats test**

Append to `internal/stats/store_test.go`:

```go
func TestTopologyPositions(t *testing.T) {
    s := newStore(t)
    ctx := context.Background()

    empty, err := s.LoadTopologyPositions(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if len(empty) != 0 {
        t.Fatalf("initial positions = %+v, want empty", empty)
    }

    first := map[string]Position{
        "entrypoint:fast": {X: 10, Y: 20},
        "node:deepseek":   {X: 30, Y: 40},
    }
    if err := s.SaveTopologyPositions(ctx, first); err != nil {
        t.Fatal(err)
    }
    got, err := s.LoadTopologyPositions(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if !reflect.DeepEqual(got, first) {
        t.Fatalf("positions = %+v, want %+v", got, first)
    }

    second := map[string]Position{"route:fast": {X: -5.5, Y: 99}}
    if err := s.SaveTopologyPositions(ctx, second); err != nil {
        t.Fatal(err)
    }
    got, err = s.LoadTopologyPositions(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if !reflect.DeepEqual(got, second) {
        t.Fatalf("replaced positions = %+v, want %+v", got, second)
    }

    if err := s.SaveTopologyPositions(ctx, nil); err != nil {
        t.Fatal(err)
    }
    got, err = s.LoadTopologyPositions(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 0 {
        t.Fatalf("cleared positions = %+v, want empty", got)
    }
}
```

Add `reflect` to the test imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/stats -run TestTopologyPositions -count=1`

Expected: FAIL with `s.LoadTopologyPositions undefined`.

- [ ] **Step 3: Implement the store**

Add to the `migrate` statement list in `internal/stats/store.go`:

```sql
CREATE TABLE IF NOT EXISTS topology_positions (
    node_id TEXT PRIMARY KEY,
    x REAL NOT NULL,
    y REAL NOT NULL
)
```

Add near the session methods:

```go
// Position is one topology node position in graph coordinates.
type Position struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

// LoadTopologyPositions returns the shared topology position map.
func (s *Store) LoadTopologyPositions(ctx context.Context) (map[string]Position, error) {
    rows, err := s.db.QueryContext(ctx, `SELECT node_id, x, y FROM topology_positions`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    out := make(map[string]Position)
    for rows.Next() {
        var id string
        var p Position
        if err := rows.Scan(&id, &p.X, &p.Y); err != nil {
            return nil, err
        }
        out[id] = p
    }
    return out, rows.Err()
}

// SaveTopologyPositions replaces the whole shared topology position map.
func (s *Store) SaveTopologyPositions(ctx context.Context, positions map[string]Position) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if _, err := tx.ExecContext(ctx, `DELETE FROM topology_positions`); err != nil {
        return err
    }
    if len(positions) > 0 {
        stmt, err := tx.PrepareContext(ctx,
            `INSERT INTO topology_positions(node_id, x, y) VALUES(?, ?, ?)`)
        if err != nil {
            return err
        }
        defer stmt.Close()
        for id, p := range positions {
            if _, err := stmt.ExecContext(ctx, id, p.X, p.Y); err != nil {
                return err
            }
        }
    }
    return tx.Commit()
}
```

- [ ] **Step 4: Run the stats test to verify it passes**

Run: `GOWORK=off go test ./internal/stats -run TestTopologyPositions -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing admin API tests**

Create `internal/admin/topology_test.go`:

```go
package admin

import (
    "encoding/json"
    "fmt"
    "net/http"
    "reflect"
    "testing"

    "github.com/go-gost/llm-gw/internal/stats"
)

func TestTopologyPositionsAPI(t *testing.T) {
    srv, _, _, ts := newTestAdmin(t)
    client, csrf := login(t, ts.URL)

    resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("get status = %d", resp.StatusCode)
    }
    var got struct {
        Positions map[string]stats.Position `json:"positions"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
        t.Fatal(err)
    }
    if len(got.Positions) != 0 {
        t.Fatalf("initial positions = %+v", got.Positions)
    }

    want := map[string]stats.Position{
        "entrypoint:fast": {X: 12, Y: 34},
        "route:fast":      {X: 56, Y: 78},
    }
    body, _ := json.Marshal(map[string]any{"positions": want})
    resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions", string(body), csrf)
    if resp.StatusCode != http.StatusNoContent {
        t.Fatalf("put status = %d", resp.StatusCode)
    }

    resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("get status = %d", resp.StatusCode)
    }
    got.Positions = nil
    if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
        t.Fatal(err)
    }
    if !reflect.DeepEqual(got.Positions, want) {
        t.Fatalf("positions = %+v, want %+v", got.Positions, want)
    }

    resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions",
        `{"positions":{}}`, csrf)
    if resp.StatusCode != http.StatusNoContent {
        t.Fatalf("clear status = %d", resp.StatusCode)
    }
    resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
    got.Positions = nil
    if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
        t.Fatal(err)
    }
    if len(got.Positions) != 0 {
        t.Fatalf("cleared positions = %+v", got.Positions)
    }

    resp = doJSON(t, &http.Client{}, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
    if resp.StatusCode != http.StatusUnauthorized {
        t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
    }

    resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions",
        `{"positions":{"":{"x":1,"y":2}}}`, csrf)
    if resp.StatusCode != http.StatusBadRequest {
        t.Fatalf("empty id status = %d, want 400", resp.StatusCode)
    }

    oversized := make(map[string]stats.Position, maxTopologyPositions+1)
    for i := 0; i <= maxTopologyPositions; i++ {
        oversized[fmt.Sprintf("node-%d", i)] = stats.Position{}
    }
    body, _ = json.Marshal(map[string]any{"positions": oversized})
    resp = doJSON(t, client, http.MethodPut, ts.URL+"/api/topology/positions", string(body), csrf)
    if resp.StatusCode != http.StatusBadRequest {
        t.Fatalf("oversized map status = %d, want 400", resp.StatusCode)
    }

    srv.stats = nil
    resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/topology/positions", "", "")
    if resp.StatusCode != http.StatusServiceUnavailable {
        t.Fatalf("missing store status = %d, want 503", resp.StatusCode)
    }
}
```

- [ ] **Step 6: Run the admin test to verify it fails**

Run: `GOWORK=off go test ./internal/admin -run TestTopologyPositionsAPI -count=1`

Expected: FAIL with `404` for the new route (or a compile error if the handler is not present yet).

- [ ] **Step 7: Implement the admin handlers**

Create `internal/admin/handlers_topology.go`:

```go
package admin

import (
    "encoding/json"
    "fmt"
    "math"
    "net/http"
    "strings"

    "github.com/go-gost/llm-gw/internal/stats"
)

const maxTopologyPositions = 10000

type topologyPositionsView struct {
    Positions map[string]stats.Position `json:"positions"`
}

func (s *Server) handleGetTopologyPositions(w http.ResponseWriter, r *http.Request) {
    if s.stats == nil {
        writeError(w, http.StatusServiceUnavailable, "stats store is not available")
        return
    }
    positions, err := s.stats.LoadTopologyPositions(r.Context())
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    if positions == nil {
        positions = map[string]stats.Position{}
    }
    writeJSON(w, http.StatusOK, topologyPositionsView{Positions: positions})
}

func (s *Server) handlePutTopologyPositions(w http.ResponseWriter, r *http.Request) {
    if s.stats == nil {
        writeError(w, http.StatusServiceUnavailable, "stats store is not available")
        return
    }
    var in topologyPositionsView
    dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
    if err := dec.Decode(&in); err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    if err := validateTopologyPositions(in.Positions); err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    if err := s.stats.SaveTopologyPositions(r.Context(), in.Positions); err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func validateTopologyPositions(positions map[string]stats.Position) error {
    if len(positions) > maxTopologyPositions {
        return fmt.Errorf("too many positions: %d", len(positions))
    }
    for id, p := range positions {
        if strings.TrimSpace(id) == "" {
            return fmt.Errorf("node id is required")
        }
        if len(id) > 256 {
            return fmt.Errorf("node id is too long")
        }
        if math.IsNaN(p.X) || math.IsInf(p.X, 0) || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
            return fmt.Errorf("position for %q is not finite", id)
        }
    }
    return nil
}
```

Register in `internal/admin/server.go` after the stats routes:

```go
api.HandleFunc("GET /api/topology/positions", s.handleGetTopologyPositions)
api.HandleFunc("PUT /api/topology/positions", s.handlePutTopologyPositions)
```

- [ ] **Step 8: Run the admin test and the stats test**

Run:

```bash
GOWORK=off go test ./internal/admin -run TestTopologyPositionsAPI -count=1
GOWORK=off go test ./internal/stats -run TestTopologyPositions -count=1
```

Expected: both PASS.

- [ ] **Step 9: Run the full Go gate**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 10: Fill the Implementation record and commit**

Fill the record below, then:

```bash
git add internal/stats/store.go internal/stats/store_test.go \
  internal/admin/handlers_topology.go internal/admin/server.go internal/admin/topology_test.go \
  docs/superpowers/specs/2026-11-05-topology-layout-persistence-design.md \
  docs/superpowers/plans/2026-11-05-topology-layout-persistence.md
git commit -m "feat(admin): persist topology positions in sqlite"
```

**Implementation record**
- Date/status: 2026-11-05 / complete
- Repo/commit: llm-gw / 8d4a2548 (code, spec and plan); this record commit is the follow-up
- Files changed: `internal/stats/store.go`, `internal/stats/store_test.go`, `internal/admin/handlers_topology.go`, `internal/admin/server.go`, `internal/admin/topology_test.go`, `docs/superpowers/specs/2026-11-05-topology-layout-persistence-design.md`, `docs/superpowers/plans/2026-11-05-topology-layout-persistence.md`
- Tests/commands and results: `GOWORK=off go test ./internal/stats -run TestTopologyPositions -count=1` PASS; `GOWORK=off go test ./internal/admin -run TestTopologyPositionsAPI -count=1` PASS; `GOWORK=off go build ./...` PASS; `GOWORK=off go vet ./...` PASS; `GOWORK=off go test ./... -count=1` PASS (root, admin, config, gateway, provider, stats).
- Review findings and fixes: The table is created by `migrate`, so existing databases gain it without destructive migration. Save is a single transaction and clears the map when empty. The handler validates empty/long IDs and the 10,000-entry cap, returns 503 when the admin server has no store, and relies on the existing session/CSRF middleware for auth. JSON decoding already rejects NaN/Infinity; the finite check remains defensive. No interface conflict with the web task: `stats.Position` and the `{"positions": ...}` envelope are used directly.
- Deferred items/limitations: Stored IDs are not pruned against the current graph; stale IDs are harmless and are dropped when the client next resets or saves a pruned map. Concurrent editors still use last-write-wins.

---

### Task 2: Web server sync with local backup

**Files:**
- Modify: `web/src/graph/positions.ts`
- Modify: `web/src/graph/positions.test.ts`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`
- Modify: `web/src/pages/Topology.tsx`
- Modify: `web/src/locales/en-US.ts`
- Modify: `README.md`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: `GET/PUT /api/topology/positions` from Task 1.
- Produces in `types.ts`:

```ts
export interface TopologyPosition { x: number; y: number }
```

- Produces in `client.ts`:

```ts
export function getTopologyPositions(): Promise<Record<string, TopologyPosition>>
export function putTopologyPositions(positions: Record<string, TopologyPosition>): Promise<void>
```

- Produces in `graph/positions.ts`:

```ts
export type PositionMap = Record<string, XYPosition>;

export function pickInitialPositions(
  server: PositionMap,
  local: PositionMap,
): { positions: PositionMap; seed: PositionMap | null };

export function createPositionSaver(
  save: (positions: PositionMap) => Promise<void>,
): (positions: PositionMap) => Promise<void>;
```

- [ ] **Step 1: Write the failing position helper tests**

Append to `web/src/graph/positions.test.ts`:

```ts
import { createPositionSaver, pickInitialPositions } from './positions';

describe('pickInitialPositions', () => {
  it('uses a non-empty server map and ignores the local map', () => {
    const server = { a: { x: 1, y: 2 } };
    const local = { a: { x: 9, y: 9 } };
    expect(pickInitialPositions(server, local)).toEqual({ positions: server, seed: null });
  });

  it('uses the local map as a seed when the server is empty', () => {
    const local = { a: { x: 3, y: 4 } };
    expect(pickInitialPositions({}, local)).toEqual({ positions: local, seed: local });
  });

  it('starts empty when both stores are empty', () => {
    expect(pickInitialPositions({}, {})).toEqual({ positions: {}, seed: null });
  });
});

describe('createPositionSaver', () => {
  it('runs saves in call order', async () => {
    const calls: string[] = [];
    const save = async (p: Record<string, { x: number; y: number }>) => {
      calls.push(Object.keys(p)[0]);
    };
    const saver = createPositionSaver(save);
    await Promise.all([saver({ a: { x: 1, y: 1 } }), saver({ b: { x: 2, y: 2 } })]);
    expect(calls).toEqual(['a', 'b']);
  });
});
```

Keep the existing `mergePositions` tests.

- [ ] **Step 2: Run the web test to verify it fails**

Run: `cd web && npm test -- --run src/graph/positions.test.ts`

Expected: FAIL because `pickInitialPositions` and `createPositionSaver` are not exported.

- [ ] **Step 3: Implement the position helpers**

Add to `web/src/graph/positions.ts`:

```ts
export type PositionMap = Record<string, XYPosition>;

/** Server positions win; an empty server is seeded from the local backup. */
export function pickInitialPositions(
  server: PositionMap,
  local: PositionMap,
): { positions: PositionMap; seed: PositionMap | null } {
  if (Object.keys(server).length > 0) {
    return { positions: server, seed: null };
  }
  if (Object.keys(local).length > 0) {
    return { positions: local, seed: local };
  }
  return { positions: {}, seed: null };
}

/** Serialize full-map saves so the last caller wins on the server. */
export function createPositionSaver(
  save: (positions: PositionMap) => Promise<void>,
): (positions: PositionMap) => Promise<void> {
  let chain: Promise<void> = Promise.resolve();
  return (positions) => {
    const next = chain.then(() => save(positions));
    chain = next.catch(() => {});
    return next;
  };
}
```

- [ ] **Step 4: Run the position tests to verify they pass**

Run: `cd web && npm test -- --run src/graph/positions.test.ts`

Expected: PASS.

- [ ] **Step 5: Add the API type, client functions and client test**

Add to `web/src/api/types.ts`:

```ts
export interface TopologyPosition {
  x: number;
  y: number;
}
```

Add to `web/src/api/client.ts`:

```ts
export function getTopologyPositions(): Promise<Record<string, TopologyPosition>> {
  return request<{ positions?: Record<string, TopologyPosition> }>(
    'GET',
    '/api/topology/positions',
  ).then((data) => data.positions ?? {});
}

export function putTopologyPositions(
  positions: Record<string, TopologyPosition>,
): Promise<void> {
  return request<void>('PUT', '/api/topology/positions', { positions });
}
```

Add `TopologyPosition` to the type import line in `client.ts`.

Add to `web/src/api/client.test.ts`:

```ts
import { getTopologyPositions } from './client';

it('normalizes topology positions', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ positions: { a: { x: 1, y: 2 } } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
  await expect(getTopologyPositions()).resolves.toEqual({ a: { x: 1, y: 2 } });
});
```

- [ ] **Step 6: Run the client test**

Run: `cd web && npm test -- --run src/api/client.test.ts`

Expected: PASS.

- [ ] **Step 7: Rewrite Topology position loading and saving**

In `web/src/pages/Topology.tsx`:

- Import `message` from `antd`, the API functions, and the new helpers.
- Replace the local-only load/save helpers with:

```ts
const POSITIONS_KEY = 'llm-gw.positions';

function loadLocalPositions(): PositionMap {
  try {
    const raw = localStorage.getItem(POSITIONS_KEY);
    return raw ? (JSON.parse(raw) as PositionMap) : {};
  } catch {
    return {};
  }
}

function saveLocalPositions(p: PositionMap) {
  try {
    localStorage.setItem(POSITIONS_KEY, JSON.stringify(p));
  } catch {
    /* ignore quota/private mode */
  }
}
```

- Initialize `positions` from `loadLocalPositions` and add `serverReady`.
- Create a stable serialized saver with `useRef(createPositionSaver(putTopologyPositions))`.
- Add `persist` with `useCallback`: write local first, then call the saver and show a keyed warning on failure.
- Add a mount effect that calls `getTopologyPositions()`:
  - on success, call `pickInitialPositions(server, loadLocalPositions())`, set positions, refresh the local backup, and PUT `seed` when non-nil (a failed seed shows the sync warning);
  - on failure, show a keyed warning;
  - always set `serverReady` when the component is still mounted.
- Gate the existing merge effect on `serverReady` and call `persist(next)` instead of `savePositions`.
- Update drag stop to compute the next map, `setPositions`, then `persist` outside the state updater.
- Replace reset with:

```ts
const resetLayout = () => {
  const raw = buildGraph(config);
  const laid = layoutGraph(raw.nodes, raw.edges);
  const next: PositionMap = {};
  for (const n of laid) next[n.id] = n.position;
  setPositions(next);
  persist(next);
};
```

- Add i18n strings to `web/src/locales/en-US.ts`:

```ts
'无法读取服务端拓扑布局，已使用本地备份': 'Could not load the server topology layout; using the local backup',
'拓扑布局已保存到本地，但同步服务端失败': 'Topology layout saved locally but could not sync to the server',
```

- [ ] **Step 8: Run the web gate**

```bash
cd web
npm test -- --run
npx tsc --noEmit
npm run build
```

Expected: all PASS.

- [ ] **Step 9: Update documentation**

In `README.md`:

- Add `GET|PUT /api/topology/positions` to the Admin API list.
- In the topology paragraph, state that node positions are stored in
  `stats.sqlite` and shared by all admins, with `localStorage` as a local backup.

In `AGENTS.md`:

- Update the web invariant to say positions are persisted server-side in the
  `topology_positions` table and mirrored to `localStorage` as a fallback.
- Add the topology endpoints to the admin API invariants if the endpoint list is
  described there.

- [ ] **Step 10: Run the final gates**

```bash
GOWORK=off go test ./... -count=1
cd web && npm test -- --run && npx tsc --noEmit && npm run build
```

Expected: all PASS.

- [ ] **Step 11: Fill the Implementation record and commit**

Fill the record below, then:

```bash
git add web/src/graph/positions.ts web/src/graph/positions.test.ts \
  web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts \
  web/src/pages/Topology.tsx web/src/locales/en-US.ts \
  README.md AGENTS.md docs/superpowers/plans/2026-11-05-topology-layout-persistence.md
git commit -m "feat(web): sync topology layout through the server"
```

**Implementation record**
- Date/status: 2026-11-05 / complete
- Repo/commit: llm-gw / 26cd6c5c (web and docs); this record commit is the follow-up
- Files changed: `web/src/graph/positions.ts`, `web/src/graph/positions.test.ts`, `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/api/client.test.ts`, `web/src/pages/Topology.tsx`, `web/src/locales/en-US.ts`, `README.md`, `AGENTS.md`, `docs/superpowers/plans/2026-11-05-topology-layout-persistence.md`
- Tests/commands and results: `cd web && npm test -- --run` PASS (9 files, 81 tests); `npx tsc --noEmit` PASS; `npm run build` PASS; `GOWORK=off go test ./... -count=1` PASS. Manual: logged in with two cookie jars, PUT the layout with session A, GET returned the same map with session B; restarted the process and GET still returned it from `stats.sqlite`.
- Review findings and fixes: Server positions replace the local map; an empty server seeds from the local backup once; server failures keep the local layout and show keyed warnings. Saves go through `createPositionSaver`, so PUTs run in call order. Reset computes the dagre layout and persists it in one call instead of clearing and refilling. The mount effect is gated by `serverReady` so a partial local map is not written before the server response. `positions.test.ts` pins server-wins, local-seed and save-order behavior; `client.test.ts` pins the response envelope normalization.
- Deferred items/limitations: no DOM-level test for the mount effect (decision logic is extracted and tested); simultaneous editors are last-write-wins; stale IDs are retained until a reset or a save from a pruned map.

## Self-Review

- Spec coverage: SQLite table, load/replace methods, admin GET/PUT with auth and
  validation, server-first load, local seeding, local fallback, serialized saves,
  single-PUT reset, docs and tests all map to Task 1 or Task 2.
- Type consistency: `stats.Position`, `TopologyPosition`, `PositionMap` and the
  JSON envelope `{"positions": ...}` are used consistently.
- Review Focus tests: server empty/local seed (`pickInitialPositions`), server
  wins (`pickInitialPositions`), rapid saves (`createPositionSaver`), reset
  (single `persist` call in implementation), invalid payload and missing store
  (admin test), local fallback (mount effect behavior; verified by web gate and
  manual second-session check).
- Limitation: the React mount effect is not covered by a DOM-level unit test;
  its decision logic is extracted into `pickInitialPositions` and tested.

## Final review fix pass (2026-11-05)

- Important: a successful `GET` could apply a stale snapshot after a drag (or after a failed `PUT`), snapping the graph back and losing the newer local layout on the next load. Fixed with a persistent `llm-gw.positions.pending` marker, a `mutated` guard for the in-flight read, and a latest-save version check that clears the marker only when the newest save succeeds. `pickInitialPositions` now accepts `pending`/`mutated` and returns `positions: null` when the in-memory map must win. Tests in `positions.test.ts` pin the pending and raced-read cases.
- Important (re-graded from the review's Minor by effect): a `PUT` with a missing or null `positions` field cleared the shared map. The request body now requires a non-nil `positions` field; `{}` and `{"positions":null}` return `400`. The admin test asserts the stored map is unchanged after both.
- Minor: invalid payload tests now confirm the stored map is untouched after empty-ID and oversized-map requests, and the missing-store `PUT` path is pinned with a `503`.
- Minor: after a failed server read the topology no longer performs an implicit write or shows a second warning; `serverReadOK` gates persistence from the merge effect while still filling in-memory dagre positions.
- Verification: `GOWORK=off go test ./... -count=1` PASS; `cd web && npm test -- --run` PASS (9 files, 83 tests); `npx tsc --noEmit` PASS; `npm run build` PASS. The fix commit is `d2e662cc`; the implementation record above remains the feature commit.
