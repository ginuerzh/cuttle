# Topology layout persistence

Date: 2026-11-05
Status: design proposed

## Intent

Topology node positions currently live only in browser `localStorage`
(`llm-gw.positions`). Every browser therefore has its own layout. Make the
layout server-shared so every admin session sees the same graph, while keeping
`localStorage` as a local backup and offline fallback.

## Goals

- Persist the topology position map on the server and share it across browsers.
- Keep the existing position format and key semantics: node ID -> `{x, y}`.
- Keep writing `localStorage` on every change as a backup.
- Seed the server from an existing local layout when the server has none.
- Fall back to `localStorage` when the server cannot be read or written.
- Preserve current behavior: saved positions survive config changes, new nodes
  get a dagre position, reset recomputes the layout.

## Non-goals

- Per-user layouts. The layout is one shared workspace state.
- Persisting viewport, zoom, node selection or edge selection.
- Conflict resolution between simultaneous editors. The last full map written
  wins.
- Adding a new config file or YAML field.
- Replacing the stats database or changing request statistics.

## Storage

Add a table to the existing stats SQLite database:

```sql
CREATE TABLE IF NOT EXISTS topology_positions (
    node_id TEXT PRIMARY KEY,
    x REAL NOT NULL,
    y REAL NOT NULL
);
```

Positions are UI state, but the stats database is already the admin server's
durable store (it also holds persistent sessions) and is mounted through the
existing `/data` volume. A separate file would add another path and permission
problem without a real benefit.

`internal/stats` gains:

```go
type Position struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

func (s *Store) LoadTopologyPositions(ctx context.Context) (map[string]Position, error)
func (s *Store) SaveTopologyPositions(ctx context.Context, positions map[string]Position) error
```

Save replaces the whole map in one transaction (`DELETE` then `INSERT`). An
empty map clears the layout.

## Admin API

- `GET /api/topology/positions` -> `{"positions":{"node-id":{"x":1,"y":2}}}`
- `PUT /api/topology/positions` -> same body, replace all, `204 No Content`

Both require a session like every other `/api/` endpoint. Validation rejects an
empty node ID, non-finite coordinates, and unreasonably large maps (for example
more than 10,000 entries). If the stats store is unavailable, return `503`.

## Web behavior

`Topology.tsx`:

1. Initialize state synchronously from `localStorage` so the page can render
   immediately.
2. Fetch `GET /api/topology/positions`.
3. If the server has positions, they are authoritative: replace local state and
   refresh the local backup.
4. If the server is empty and the local backup is not, seed the server with the
   local layout once.
5. If the server request fails, keep using the local backup and show one
   warning; the topology remains usable.
6. After the initial server read completes, the existing merge effect fills
   positions for new nodes and writes the merged map to both stores.
7. Drag stop, first layout, and reset write to `localStorage` immediately and
   then PUT the full current map. Reset computes the dagre layout explicitly so
   it performs one PUT with the new map instead of clearing and refilling.
8. Failed server writes keep the local backup and show a keyed warning, so a
   slow or offline server does not block local use.

The API client gains `getTopologyPositions` and `putTopologyPositions`; the wire
type is `Record<string, {x: number; y: number}>`.

## Testing

- `internal/stats`: table creation, round-trip, full replacement, clearing.
- `internal/admin`: empty GET, PUT then GET, auth required, invalid map
  rejected, `503` when no store is configured.
- Web: API client normalization; position merge behavior remains covered;
  TypeScript and Vitest gates.
- Manual: run a local instance, save via one browser/session cookie, read the
  same layout with a second session, and confirm the SQLite rows.

## Acceptance criteria

- Two browsers with separate storage load the same topology positions after one
  of them saves a layout.
- Restarting the gateway keeps the shared layout.
- `localStorage` still contains the latest layout and is used when the server
  read fails.
- Reset updates the server and both local copies.
- Existing Go and web gates pass.
