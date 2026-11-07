# Gateway Keys and Per-Key Usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add named gateway keys for data-plane authentication and per-key usage reporting.

**Architecture:** A new auth handler sits between the public hello probe and the count_tokens/forward handlers. It validates credentials, places the key name in the request context, and the stats recorder persists it alongside hourly key-dimension rollups. A new admin CRUD API and web page manage keys and show 24h/7d/30d usage.

**Tech Stack:** Go, SQLite (modernc.org/sqlite), x handler chain, React, Ant Design, Vite, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-07-gateway-keys-design.md`

## Global Constraints

- Work in `/root/code/go-gost/llm-gw`; it is its own Git repository. Do not edit `../core`, `../x` or `../llm-api-converter`.
- Run every Go command with `GOWORK=off`.
- Empty `gateway.keys` means authentication off. Any entry means authentication on, including a list where every entry is disabled (all requests then get 401).
- Accepted credential headers: `Authorization: Bearer <value>`, `x-api-key`, `x-goog-api-key`, `api-key`. Any candidate may match. Accepting a request must not depend on the first header being valid.
- Raw gateway keys must never appear in logs, stats, captures, `GET /api/config`, `GET /api/gateway-keys`, or the web bundle. The only raw-key responses are `POST /api/gateway-keys` and `POST /api/gateway-keys/{name}/rotate`.
- Key values are stored plaintext in YAML like provider keys; config writes stay atomic and `0600`.
- Stats schema changes are additive. Old databases keep working; no destructive migration and no backfill.
- UI source strings are Chinese and must be added to `web/src/locales/en-US.ts`.
- Commit after each task, including its Implementation record. Never push.
- Spec section references in this plan use the headings of `2026-11-07-gateway-keys-design.md`.

## Review Focus

- **Claude Code sends more than one credential header.** A valid key in `x-api-key` must not be rejected because `Authorization` carries another value. Test: Task 3 `TestGatewayAuthAnyCandidateMayMatch`.
- **Large bodies and `Expect: 100-continue`.** Authentication must read only the request head and must not lose or reorder body bytes. Test: Task 3 `TestGatewayAuthReplaysLargeBody`.
- **Old SQLite databases.** Opening a pre-change database must add `requests.gateway_key`, the index and `key_rollups` without touching old rows. Test: Task 2 `TestOpenMigratesGatewayKey`.
- **Admin read-modify-write of the whole config.** `PUT /api/config` without `gateway.keys` must preserve existing keys; only an explicit `[]` clears them. Test: Task 4 `TestPutConfigPreservesGatewayKeys`.
- **Deletion and rotation.** A rotated key must stop working immediately, while a deleted key's historical usage remains visible. Tests: Task 3 `TestGatewayAuthReloadRotates`, Task 4 `TestGatewayKeyUsageMergesConfiguredAndDeleted`.

---

### Task 1: Gateway key configuration

**Files:**
- Modify: `internal/config/config.go` (`Gateway`, new `GatewayKey`, `expandEnv`, `applyDefaults`, `Validate`)
- Modify: `internal/config/config_test.go`
- Modify: `example.yaml`

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```go
  type GatewayKey struct {
      Name    string `yaml:"name" json:"name"`
      Key     string `yaml:"key" json:"key,omitempty"`
      Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
  }
  // Gateway gains: Keys []GatewayKey `yaml:"keys,omitempty" json:"keys,omitempty"`
  ```

- [x] **Step 1: Write failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestLoadGatewayKeys(t *testing.T) {
    t.Setenv("GW_KEY", "sk-gw-secret")
    c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
  keys:
    - name: " claude-code "
      key: "${GW_KEY}"
    - name: disabled
      key: sk-gw-disabled
      enabled: false
`))
    // assert: err == nil; first name trimmed to "claude-code";
    // first key == "sk-gw-secret"; first Enabled == nil;
    // second Enabled != nil && *second.Enabled == false.
}

func TestValidateGatewayKeys(t *testing.T) {
    // table: duplicate name, empty name, "/" in name, name longer than 64,
    // empty key, key shorter than 8, key longer than 512, duplicate key.
    // For each: cfg := &Config{Gateway: Gateway{Keys: keys}}; ApplyDefaults(cfg);
    // err := Validate(cfg); assert err != nil and the message names the
    // offending key or field. Assert no error message contains a key value.
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestLoadGatewayKeys|TestValidateGatewayKeys' -count=1`
Expected: FAIL because `Gateway.Keys` and `GatewayKey` do not exist.

- [x] **Step 3: Implement the schema, normalization and validation**

- Add `Keys []GatewayKey` to `Gateway` and the struct above near `Gateway`.
- In `expandEnv`, expand `Name` and `Key` for every entry.
- In `applyDefaults`, for every entry: `strings.TrimSpace` on `Name` and `Key`; leave `Enabled` untouched.
- In `Validate`, before the providers loop, call `validateGatewayKeys(c.Gateway.Keys)` and return its error.
- `validateGatewayKeys` rules: name required, ≤64 chars, no `/`, no control characters, unique; key required, 8–512 characters, unique. Error text names the index and field but never the value (`gateway.keys[0]: key is too short`, `gateway.keys[1]: duplicate name "x"`). Duplicate key errors say `duplicate key value` without the value.
- Add a commented block to `example.yaml` under `gateway`:
  ```yaml
  # Data-plane access keys. Empty or absent keeps the gateway open.
  # keys:
  #   - name: claude-code
  #     key: ${CLAUDE_GATEWAY_KEY}
  #   - name: disabled-example
  #     key: sk-gw-example
  #     enabled: false
  ```
- `TestExampleConfigLoads` must keep passing, proving the commented block does not change the default.

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/config -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go example.yaml
git commit -m "feat(config): add gateway keys"
```

**Implementation record**
- Date/status: 2026-11-07, complete.
- Repository + commit: llm-gw @ see commit (this commit, `feat(config): add gateway keys`)
- Files changed: `internal/config/config.go` (new `GatewayKey`, `Gateway.Keys`, env expansion, trimming and `validateGatewayKeys`), `internal/config/config_test.go` (load/normalize/validate coverage), `example.yaml` (commented keys block), plus this plan and `docs/superpowers/specs/2026-11-07-gateway-keys-design.md`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/config -run 'TestLoadGatewayKeys|TestValidateGatewayKeys' -count=1` failed because `Gateway.Keys`/`GatewayKey` did not exist; GREEN focused run passed all cases; `GOWORK=off go test ./internal/config -count=1`, `GOWORK=off go build ./...` and `GOWORK=off go vet ./internal/config` passed.
- Review findings and fixes: validation errors name only the entry index and field, never a key value; an empty or absent list stays open and is not collapsed to nil so later admin PUT semantics can distinguish absent from `[]`; `Enabled` is untouched by defaults.
- Deferred items/limitations: authentication, stats attribution and admin/API/UI management belong to later tasks; this task changes no runtime behavior.

---

### Task 2: Stats storage, context and key usage

**Files:**
- Create: `internal/stats/gatewaykey.go`
- Create: `internal/stats/gatewaykey_test.go`
- Modify: `internal/stats/store.go` (`Request`, `Filter`, `KeyUsage`, `migrate`, `Insert`, `Query`, `Get`, `scanRequests`, `Cleanup`)
- Modify: `internal/stats/recorder.go`
- Modify: `internal/stats/store_test.go`
- Modify: `internal/stats/recorder_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:
  ```go
  type KeyUsage struct {
      Key          string `json:"key"`
      Requests     int64  `json:"requests"`
      Errors       int64  `json:"errors"`
      InputTokens  int64  `json:"inputTokens"`
      OutputTokens int64  `json:"outputTokens"`
      CachedTokens int64  `json:"cachedTokens"`
  }
  func WithGatewayKey(ctx context.Context, name string) context.Context
  func GatewayKeyFromContext(ctx context.Context) string
  func (s *Store) KeyUsage(ctx context.Context, from, to time.Time) ([]KeyUsage, error)
  // Request gains: GatewayKey string `json:"gatewayKey,omitempty"`
  // Filter gains:  GatewayKey string
  ```

- [x] **Step 1: Write failing stats tests**

Add to `internal/stats/gatewaykey_test.go`:

```go
func TestGatewayKeyContext(t *testing.T) {
    ctx := WithGatewayKey(context.Background(), "claude-code")
    // assert GatewayKeyFromContext(ctx) == "claude-code"
    // assert GatewayKeyFromContext(context.Background()) == ""
}
```

Add to `internal/stats/store_test.go`:

```go
func TestInsertRecordsGatewayKeyAndRollups(t *testing.T) {
    // s := newStore(t); now := time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC)
    // Insert a: GatewayKey "alpha", Status 200, InputTokens 10, OutputTokens 2
    // Insert b: GatewayKey "alpha", Status 500
    // Insert c: GatewayKey ""
    // Query with Filter{GatewayKey:"alpha"} => 2 rows, both GatewayKey "alpha"
    // KeyUsage(now.Truncate(time.Hour), now.Add(time.Hour)) => one "alpha" row:
    //   Requests 2, Errors 1, InputTokens 10, OutputTokens 2
}

func TestOpenMigratesGatewayKey(t *testing.T) {
    // Create a temp DB with the pre-change requests/rollups schema (same shape
    // as TestOpenMigratesMonotonicSeq), insert one old row, close.
    // Open it; assert PRAGMA table_info(requests) contains gateway_key, the
    // key_rollups table exists, and Insert with GatewayKey "alpha" succeeds.
}

func TestCleanupRemovesOldKeyRollups(t *testing.T) {
    // Insert one row with TS now-48h and GatewayKey "old".
    // Cleanup(ctx, now, now.Add(-24*time.Hour), now).
    // KeyUsage over the last 7 days returns no "old" row.
}
```

Add to `internal/stats/recorder_test.go`:

```go
func TestRecorderRecordsGatewayKey(t *testing.T) {
    // Build the same recorder body used by TestRecorderCopiesRewriteFields,
    // call Record(WithGatewayKey(context.Background(), "alpha"), body),
    // then assert the stored row's GatewayKey == "alpha".
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'GatewayKey|KeyRollups|MigratesGatewayKey' -count=1`
Expected: FAIL because the context helpers, fields and table do not exist.

- [x] **Step 3: Implement**

- `gatewaykey.go`: unexported context key type; the two functions above.
- `Request`: add `GatewayKey` after `Node`.
- `Filter`: add `GatewayKey` after `Node`; `where()` adds `gateway_key = ?`.
- `migrate`:
  - `CREATE INDEX IF NOT EXISTS idx_requests_gateway_key_ts ON requests(gateway_key, ts)`.
  - `CREATE TABLE IF NOT EXISTS key_rollups (...)` using the spec's columns and `PRIMARY KEY (bucket, gateway_key)`.
  - `ensureColumns("requests", ... "gateway_key": "TEXT NOT NULL DEFAULT ''")`.
- `Insert`:
  - Add `gateway_key` to the INSERT column list after `node` and one `?`.
  - Pass `r.GatewayKey` after `r.Node`.
  - After the existing `rollups` upsert and only when `r.GatewayKey != ""`, upsert `key_rollups` with the same bucket and counters, `ON CONFLICT(bucket, gateway_key) DO UPDATE`.
- `Query`/`Get`: add `gateway_key` to the SELECT after `node`; `scanRequests` scans it after `&r.Node`.
- `KeyUsage`: aggregate `key_rollups` with the same bucket bounds as `ProviderUsage`, `GROUP BY gateway_key ORDER BY gateway_key`; return a non-nil slice.
- `Cleanup`: `DELETE FROM key_rollups WHERE bucket < ?` with the rollups cutoff, before returning.
- `recorder.go`: set `req.GatewayKey = GatewayKeyFromContext(ctx)` before the facts merge.

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/stats
git commit -m "feat(stats): record gateway key and key usage"
```

**Implementation record**
- Date/status: 2026-11-07, complete.
- Repository + commit: llm-gw @ see commit (this commit, `feat(stats): record gateway key and key usage`)
- Files changed: `internal/stats/gatewaykey.go` (new context carrier), `internal/stats/store.go` (`Request.GatewayKey`, `Filter.GatewayKey`, `KeyUsage`, `key_rollups` schema/index, insert, query/scan, cleanup), `internal/stats/recorder.go` (reads the key name from the context), `internal/stats/gatewaykey_test.go`, `internal/stats/store_test.go`, `internal/stats/recorder_test.go`, plus this plan.
- Tests/commands and results: RED `GOWORK=off go test ./internal/stats -run 'GatewayKey|KeyRollups|MigratesGatewayKey' -count=1` failed to build because the context helpers, fields and table did not exist; GREEN focused run passed; `GOWORK=off go test ./internal/stats -count=1`, `GOWORK=off go build ./...`, `GOWORK=off go vet ./internal/stats` and `GOWORK=off go test ./... -count=1` passed.
- Review findings and fixes: the `requests.gateway_key` index is created after `ensureColumns` so opening a pre-change database does not fail on the missing column; `key_rollups` is written only for non-empty key names in the same transaction as `rollups`; cleanup reuses the rollups cutoff; `KeyUsage` returns an empty non-nil slice.
- Deferred items/limitations: Task 3 places the key name in the context and Task 4 exposes `KeyUsage` and the request filter through the admin API; existing rows are not backfilled and open-mode traffic has no key rollup.

---

### Task 3: Data-plane authentication handler

**Files:**
- Create: `internal/gateway/auth.go`
- Create: `internal/gateway/auth_test.go`
- Modify: `internal/gateway/gateway.go` (`Gateway` struct, `New`, `apply`)

**Interfaces:**
- Consumes: `config.GatewayKey` from Task 1; `stats.WithGatewayKey` from Task 2.
- Produces:
  ```go
  type authPolicy struct { required bool; entries []authEntry }
  type authEntry struct { name string; digest [32]byte; enabled bool }
  func newAuthPolicy(keys []gwconfig.GatewayKey) *authPolicy
  func newGatewayAuthHandler(next handler.Handler, policy *atomic.Pointer[authPolicy], log logger.Logger) *gatewayAuthHandler
  // Gateway gains: auth atomic.Pointer[authPolicy]
  ```

- [x] **Step 1: Write failing gateway auth tests**

Add to `internal/gateway/auth_test.go` (same package `gateway`):

```go
func TestGatewayAuthOpenWhenNoKeys(t *testing.T)
// testConfig without keys; POST /v1/chat/completions => 200; upstream hit == 1.

func TestGatewayAuthAcceptsSupportedHeaders(t *testing.T)
// cfg.Gateway.Keys = [{Name:"client", Key:"gateway-secret"}]
// table of headers:
//   Authorization: Bearer gateway-secret
//   Authorization: bearer gateway-secret
//   x-api-key: gateway-secret
//   x-goog-api-key: gateway-secret
//   api-key: gateway-secret
// each => 200 and upstream hit.

func TestGatewayAuthAnyCandidateMayMatch(t *testing.T)
// Authorization: Bearer wrong + x-api-key: gateway-secret => 200.

func TestGatewayAuthRejectsMissingInvalidAndDisabled(t *testing.T)
// missing => 401, body contains "invalid gateway key",
//   WWW-Authenticate contains "Bearer", upstream hits 0.
// wrong => same.
// key "off" configured with Enabled false => same.

func TestAuthPolicyReasons(t *testing.T)
// newAuthPolicy with first, second and disabled entries.
// no candidates => ("", "missing", false)
// wrong candidate => ("", "invalid", false)
// disabled candidate => ("", "disabled", false)
// second candidate => ("second", "", true)
// both first and second candidates => ("first", "", true)
// wrong + second => ("second", "", true)

func TestGatewayHelloStaysPublic(t *testing.T)
// GET /api/hello without a key while keys are configured => 200.

func TestGatewayCountTokensRequiresKey(t *testing.T)
// POST /v1/messages/count_tokens with {"model":"claude-3"}:
// no key => 401; valid key => status is not 401.

func TestGatewayAuthRejectsMalformedHead(t *testing.T)
// raw TCP write "not http\r\n\r\n" while keys are configured;
// response status 400; upstream hits 0.

func TestGatewayAuthKeyNotForwarded(t *testing.T)
// upstream captures all headers; valid gateway key "gateway-secret";
// no upstream header value contains "gateway-secret".

func TestGatewayAuthReplaysLargeBody(t *testing.T)
// 1 MiB JSON body, Content-Type application/json, valid key;
// upstream reads the body and reports len == 1 MiB; response 200.

func TestGatewayAuthReloadRotates(t *testing.T)
// start with key A: A => 200.
// cfg2 := testConfig(...); cfg2.Gateway.Keys = [{Name:"client", Key:"B"}]
// gw.Reload(cfg2): A => 401, B => 200.
```

Also add a stats attribution test:

```go
func TestGatewayAuthRecordsGatewayKey(t *testing.T)
// cfg.Stats.Path = temp file; cfg.Gateway.Keys = [{Name:"alpha", Key:"secret"}]
// send one valid request, wait for the stats row (reuse waitRow),
// assert row.GatewayKey == "alpha".
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'GatewayAuth|AuthPolicy|HelloStaysPublic' -count=1`
Expected: FAIL because the auth handler and policy do not exist.

- [x] **Step 3: Implement `auth.go`**

- Constants: header limit 1 MiB, header read deadline 15s.
- `newAuthPolicy`: `required = len(keys) > 0`; one entry per key with `sha256.Sum256([]byte(key))` and `enabled = key.Enabled == nil || *key.Enabled`. Skip entries with an empty key; they cannot be produced by validation.
- `hasEnabled()` helper.
- Handler chain behavior:
  - If `policy == nil` or `!required`, call `next.Handle(ctx, conn, opts...)` immediately.
  - Set read deadline, read raw bytes through the blank line with a `bufio.Reader` (`ReadBytes('\n')` in a loop, cap at 1 MiB), clear the deadline.
  - Parse the request line for method and target. Parse the header block with `textproto.NewReader(...).ReadMIMEHeader()`.
  - If the head is malformed, write a 400 response and return.
  - Collect candidates: every `Authorization` value whose scheme is `Bearer` case-insensitively (trim the rest), plus every `x-api-key`, `x-goog-api-key` and `api-key` value. Ignore empty values.
  - `authenticate`: hash every candidate once; compare each entry digest against every candidate digest with `subtle.ConstantTimeCompare`, accumulating a match. Record the first enabled matching entry name in configuration order and whether any disabled entry matched. Do not return early. Result: enabled match → name; else disabled match → `disabled`; else candidates empty → `missing`; else `invalid`.
  - On success: `next.Handle(stats.WithGatewayKey(ctx, name), &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(head), br)}, opts...)`.
  - On failure: log `gateway auth: <method> <target> from <remote>: <reason>` at warn level (no credential), then write a 401 with `Content-Type: application/json`, `WWW-Authenticate: Bearer realm="llm-gw"`, `Connection: close`, body `{"error":{"message":"invalid gateway key","type":"authentication_error"}}`.
  - Defer `conn.Close()` like the neighboring wrappers.
- `gateway.go`:
  - Add `auth atomic.Pointer[authPolicy]`.
  - In `New`, wrap `ct` with the auth handler, then wrap that with hello:
    `h := newHelloHandler(newGatewayAuthHandler(ct, &g.auth, log), log)`.
  - In `apply`, after `buildHopGroup` succeeds:
    ```go
    ap := newAuthPolicy(cfg.Gateway.Keys)
    g.auth.Store(ap)
    if ap.required && !ap.hasEnabled() {
        g.log.Warnf("gateway keys: all configured keys are disabled; all data-plane requests will be rejected")
    }
    ```
    Store the policy next to `g.countTokens.Store(policy)`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/gateway -run 'GatewayAuth|AuthPolicy|HelloStaysPublic' -count=1`
Expected: PASS.

Run the full package: `GOWORK=off go test ./internal/gateway -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/gateway
git commit -m "feat(gateway): require configured gateway keys"
```

**Implementation record**
- Date/status: 2026-11-07, complete (initial implementation plus connection-scoping fix).
- Repository + commit: llm-gw @ `35dcc29a` (`feat(gateway): require configured gateway keys`) and this fix commit (`fix(gateway): scope authentication to one request per connection`).
- Files changed: `internal/gateway/auth.go` (new `authPolicy`/`authEntry`, `newAuthPolicy`, `gatewayAuthHandler`, raw head read/replay, single-request body framing and chunked pass-through, candidate collection, constant-time authentication, 400/401 responses), `internal/gateway/auth_test.go` (new coverage for open mode, accepted headers, any-candidate matching, missing/invalid/disabled rejection, reasons, hello exemption, count_tokens, malformed head, invalid body framing, header leakage, large-body replay, reload rotation, reload to empty keys, all-disabled keys, exact `WWW-Authenticate`, query stripping, and raw-TCP same-connection rejection for Content-Length, chunked and no-body requests), `internal/gateway/gateway.go` (`Gateway.auth`, hello → auth → count_tokens chain, policy store and all-disabled warning in `apply`), `internal/gateway/stats_test.go` (`TestGatewayAuthRecordsGatewayKey`), plus this plan.
- Tests/commands and results: initial RED `GOWORK=off go test ./internal/gateway -run 'GatewayAuth|AuthPolicy|HelloStaysPublic' -count=1` failed to build with `undefined: newAuthPolicy`; initial GREEN focused run passed every case. Fix RED `GOWORK=off go test ./internal/gateway -run 'TestGatewayAuthSecondRequestOnSameConnectionIsRejected|TestGatewayAuthSecondChunkedRequestOnSameConnectionIsRejected|TestGatewayAuthNoBodyFirstRequestClosesConnection|TestGatewayAuthRejectsInvalidBodyFraming|TestGatewayAuthAllDisabledRejectsMissingCredential|TestGatewayAuthReloadToEmptyKeysTurnsAuthOff' -count=1` failed: each same-connection test got a second 200 and the framing tests got `unexpected EOF` instead of 400. After the fix, `GOWORK=off go test ./internal/gateway -run 'GatewayAuth|AuthPolicy|HelloStaysPublic' -count=1` and `GOWORK=off go test ./internal/gateway -count=1` passed; `GOWORK=off go build ./...`, `GOWORK=off go vet ./...` and `GOWORK=off go test ./... -count=1` passed.
- Review findings and fixes: a fresh review found that authentication was connection-scoped. The x keep-alive loop processed a second request on an already-authenticated socket without re-entering the auth handler, even though the first response advertised `Connection: close`; a raw-socket check got two 200 responses and two upstream hits sharing one request ID. Fix: after a successful authentication the handler passes a connection whose `Read` yields exactly the first request and then EOF. `Content-Length` bodies are bounded with `io.LimitReader`, chunked bodies are streamed through a pass-through reader that emits the chunk-size lines (extensions included), data, CRLF separators and trailers up to the terminal chunk, and a bodyless request yields only the head. `Transfer-Encoding` that is not chunked (or has chunked in a non-final position) and invalid, negative or duplicate `Content-Length` values return 400. Open mode remains a pure pass-through. The warn log records only the target path so a query credential cannot leak. Earlier decisions retained: the exact-byte replay test uses an unmapped path so the chat rewriter does not normalize the body; Go's client strips `Connection` from `Response.Header`, so `resp.Close` is asserted; every digest is compared with every candidate without early exit; empty-key entries are skipped defensively; credentials are absent from logs and upstream headers.
- Deferred items/limitations: admin key CRUD/usage API, web page and documentation belong to Tasks 4–7; 401 requests are logged only and not recorded in stats (by design); no per-IP rate limiting in v1. The single-request wrapper makes `http.keepalive: false` effective for HTTP requests, but protocol upgrades (for example WebSocket/101) cannot continue past the first request when keys are configured; the design does not cover upgrades, and a proper per-request auth loop belongs in x.

---

### Task 4: Admin key API, usage endpoint and config behavior

**Files:**
- Create: `internal/admin/handlers_keys.go`
- Create: `internal/admin/handlers_keys_test.go`
- Modify: `internal/admin/server.go` (routes)
- Modify: `internal/admin/handlers.go` (`configView`, `handleGetConfig`, `handlePutConfig`)
- Modify: `internal/admin/handlers_stats.go` (`handleRequests` key filter)

**Interfaces:**
- Consumes: `config.GatewayKey`, `stats.KeyUsage`, `Store.KeyUsage`, `Filter.GatewayKey`.
- Produces:
  ```go
  // GET  /api/gateway-keys
  // POST /api/gateway-keys                 -> 201 {"name","key"}
  // PUT  /api/gateway-keys/{name}          -> 204
  // POST /api/gateway-keys/{name}/rotate   -> 200 {"name","key"}
  // DELETE /api/gateway-keys/{name}        -> 204
  // GET  /api/gateway-key-usage?from=&to=
  func generateGatewayKey() (string, error)
  ```

- [x] **Step 1: Write failing admin tests**

Add `internal/admin/handlers_keys_test.go` using `newTestAdmin`, `login` and `doJSON`:

```go
func TestGatewayKeyCRUD(t *testing.T)
// create {"name":"claude-code"} => 201; decode {name,key};
// key has prefix "sk-gw-" and len > 40.
// GET /api/gateway-keys => one item, keyPreview != raw, enabled true.
// GET /api/config body does not contain the raw key; gateway.keysSet == 1,
// gateway.authRequired == true.
// PUT {"enabled":false} => 204; list shows enabled false.
// POST /rotate => 200 with a different key; GET still masks.
// DELETE => 204; list is [].

func TestGatewayKeyCreateRejectsDuplicateName(t *testing.T)
// second create with the same name => 400.

func TestGatewayKeyCreateGeneratesUniqueKeys(t *testing.T)
// create two names; returned keys differ.

func TestPutConfigPreservesGatewayKeys(t *testing.T)
// seed one key through POST; read the current config through GET /api/config
// (no keys field), then PUT that body back;
// assert the key is still present and the raw value unchanged.
// Then PUT a body with "gateway":{"keys":[]} and assert the list is empty.
// Then PUT a body with a non-empty gateway.keys list containing key
// "replacement-secret" and assert it replaces the set, while GET /api/config
// still does not echo the raw value.

func TestRequestsFilterByGatewayKey(t *testing.T)
// insert one row with GatewayKey "alpha" and one with "beta";
// GET /api/requests?key=alpha => only the alpha row, gatewayKey "alpha".

func TestGatewayKeyUsageMergesConfiguredAndDeleted(t *testing.T)
// insert stats rows with GatewayKey "alpha" (2 requests, 1 error, 30/6 tokens)
// and "old" (1 request, 10/2 tokens); configure only "alpha";
// GET /api/gateway-key-usage with a one-hour window:
// keys[0] name alpha configured true, counters match;
// keys[1] name old configured false, counters match;
// totals requests 3, errors 1, input 40, output 8.

func TestGatewayKeyUsageStatsDisabled(t *testing.T)
// newTestAdmin, then s.stats = nil; GET => 503.
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'GatewayKey|PutConfigPreserves' -count=1`
Expected: FAIL because the routes and handlers do not exist.

- [x] **Step 3: Implement**

- `handlers_keys.go`:
  - `gwKeyEnabled(k)` returns `k.Enabled == nil || *k.Enabled`.
  - `generateGatewayKey`: 32 bytes from `crypto/rand`, `base64.RawURLEncoding`, prefix `sk-gw-`.
  - `gatewayKeyView{Name, KeyPreview, Enabled}`; list in config order with `preview(k.Key)`.
  - Create input `{Name string, Enabled *bool}`. Trim name. Generate a unique key (regenerate up to 8 times if it collides with any configured key). Append through `mutate`. Return `201` with `{name,key}` only after the mutation succeeds.
  - Update input `{Name string, Enabled *bool}`. Reject a mismatched name and a nil `enabled`. Replace only the `Enabled` pointer. Return 204.
  - Rotate: generate a unique key excluding the target, replace `Key`, return 200 `{name,key}`.
  - Delete: remove the entry; unknown name is a 400 with `gateway key "<name>" not found`; return 204.
  - Usage: if `s.stats == nil`, 503 `stats disabled`. `from, to := parseRange(r)`. Snapshot keys under `s.mu`. Call `s.stats.KeyUsage`. Build a map, emit configured keys in config order (preview and enabled from config), then append unconfigured names sorted alphabetically with `enabled:false`, then totals. Always return `keys: []` when empty. Response fields: `from`, `to`, `totals`, `keys`; each key row has `name`, `configured`, `enabled`, optional `keyPreview`, and the five counters.
- `server.go`: register the six routes inside the authenticated `api` mux.
- `handlers.go`:
  - `configView.Gateway` gains `KeysSet int` and `AuthRequired bool`; set both in `handleGetConfig`.
  - `handlePutConfig`: before `*dst = c`, preserve keys when absent:
    ```go
    if c.Gateway.Keys == nil {
        c.Gateway.Keys = dst.Gateway.Keys
    }
    ```
  - Do not add keys to any other view.
- `handlers_stats.go`: in `handleRequests`, set `GatewayKey: q.Get("key")` in the `stats.Filter` literal.

- [x] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/admin -run 'GatewayKey|PutConfigPreserves' -count=1`
Expected: PASS.

Run the full package: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): manage gateway keys and usage"
```

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit.
- Files changed: `internal/admin/handlers_keys.go` (new), `internal/admin/handlers_keys_test.go` (new), `internal/admin/server.go` (six authenticated routes), `internal/admin/handlers.go` (`configView.Gateway.KeysSet`/`AuthRequired`, `handlePutConfig` key preservation), `internal/admin/handlers_stats.go` (`key` request filter).
- Tests/commands and results: RED `GOWORK=off go test ./internal/admin -run 'GatewayKey|PutConfigPreserves' -count=1` failed with 404 for every new route/handler (log: `.superpowers/sdd/2026-11-07-gateway-keys/task-4-red.log`). GREEN same command passed; full `GOWORK=off go test ./internal/admin -count=1` passed (9.5s); `GOWORK=off go build ./...`, `GOWORK=off go vet ./internal/admin` and full `GOWORK=off go test ./... -count=1` passed.
- Review findings and fixes: self-review only (controller review follows). Confirmed raw keys never appear in list/config/log paths, empty list serializes as `[]`, PUT config preserves absent keys but clears explicit `[]`, usage merges zero-usage configured keys and deleted historical keys, and stats-disabled returns 503 `stats disabled`. No fixes required.
- Deferred items/limitations: console page/renderers are Task 5 and docs are Task 7; `POST /api/gateway-keys` deliberately returns a one-time secret body (ruled exception to the empty-201 invariant); PUT ignores key values by design; open-mode traffic has no per-key usage rows (stats behavior from Task 2); no backfill of pre-change usage.

---

### Task 5: Web API client, navigation and Gateway keys page

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/nav.ts`
- Modify: `web/src/App.tsx`
- Create: `web/src/pages/GatewayKeys.tsx`
- Create: `web/src/pages/GatewayKeys.test.tsx`
- Modify: `web/src/api/client.test.ts`
- Modify: `web/src/nav.test.ts`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: Task 4 HTTP API.
- Produces:
  ```ts
  export interface GatewayKey { name: string; keyPreview: string; enabled: boolean }
  export interface KeyUsageCounts {
    requests: number; errors: number; inputTokens: number;
    outputTokens: number; cachedTokens: number;
  }
  export interface GatewayKeyUsage extends KeyUsageCounts {
    name: string; configured: boolean; enabled: boolean; keyPreview?: string;
  }
  export interface GatewayKeyUsageResponse {
    from: string; to: string; totals: KeyUsageCounts; keys: GatewayKeyUsage[];
  }
  export function listGatewayKeys(): Promise<GatewayKey[]>
  export function createGatewayKey(input: { name: string; enabled?: boolean }): Promise<{ name: string; key: string }>
  export function updateGatewayKey(name: string, input: { enabled: boolean }): Promise<void>
  export function rotateGatewayKey(name: string): Promise<{ name: string; key: string }>
  export function deleteGatewayKey(name: string): Promise<void>
  export function getGatewayKeyUsage(q: { from: Date; to: Date }): Promise<GatewayKeyUsageResponse>
  export function GatewayKeysPage(): JSX.Element
  ```

- [x] **Step 1: Write failing web tests**

Add to `web/src/api/client.test.ts`:

```ts
it('creates a gateway key', async () => {
  // stub fetch returning 201 {"name":"claude-code","key":"sk-gw-abc"}
  // assert createGatewayKey({name:'claude-code'}) resolves to the body
  // and the request is POST /api/gateway-keys with JSON.
});

it('loads gateway key usage', async () => {
  // stub fetch returning {"from":"...","to":"...","totals":{...},"keys":[]}
  // assert getGatewayKeyUsage({from,to}) resolves with keys [].
});

it('filters requests by gateway key', async () => {
  // stub fetch returning []; call getRequests({..., gatewayKey:'alpha'});
  // assert the URL contains key=alpha.
});
```

Add `web/src/pages/GatewayKeys.test.tsx` covering exported pure components:

```tsx
it('renders a key card with mask and usage', () => {
  // renderToStaticMarkup(<GatewayKeyCard item={{...}} usage={{...}} />)
  // contains the name, masked preview, request count and token counts.
});

it('classifies the empty and all-disabled states', () => {
  // gatewayKeyState([]) === 'none'
  // gatewayKeyState([{enabled:false}]) === 'all-disabled'
  // gatewayKeyState([{enabled:true}]) === 'ok'
});
it('shows the one-time secret and warning', () => {
  // renderToStaticMarkup(<GatewaySecretContent secret={{name:'k',key:'sk-gw-raw'}} />)
  // contains 'sk-gw-raw' and '该密钥只显示一次，请立即复制保存。'
});
```

Update `web/src/nav.test.ts`:

```ts
expect(NAV_ORDER).toEqual([
  'topology', 'providers', 'nodes', 'routes', 'entrypoints',
  'gateway-keys', 'requests', 'usage', 'settings',
]);
expect(NAV_LABELS['gateway-keys']).toBe('网关密钥');
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test -- --run api/client.test.ts pages/GatewayKeys.test.tsx nav.test.ts`
Expected: FAIL because the types, functions, page and nav entry do not exist.

- [x] **Step 3: Implement**

- `types.ts`: add the four types; add `gatewayKey?: string` to `RequestRow` (needed by Task 6, harmless now).
- `client.ts`: add the six functions. `getGatewayKeyUsage` normalizes `keys` and `totals` (`keys ?? []`, zero totals if absent). `RequestQuery` gains `gatewayKey?: string`; `getRequests` sets `key` when present.
- `nav.ts`: add `'gateway-keys'` to `Page`, `NAV_ORDER` after `'entrypoints'`, and `NAV_LABELS` entry `网关密钥`.
- `App.tsx`: import `KeyOutlined`; add `'gateway-keys': <KeyOutlined />` to `NAV_ICONS`; render `<GatewayKeysPage />` in the page switch before `requests`.
- `GatewayKeys.tsx`:
  - Export `gatewayKeyState(keys: Pick<GatewayKey, 'enabled'>[]): 'none' | 'all-disabled' | 'ok'`.
  - Export `GatewayKeyCard({ item, usage }: { item: GatewayKey; usage?: GatewayKeyUsage })` using `ListCard`: title `item.name`; subtitle shows `item.keyPreview` in code style plus a usage line `会话 N · 错误 N · 输入 N · 输出 N · 缓存 N`; tags contain an Ant Design `Switch` with `aria-label={t('启用 {name}', {name: item.name})}`; actions are Sessions, Rotate, Delete buttons. The parent owns the callbacks.
  - Export `GatewaySecretContent({ secret }: { secret: { name: string; key: string } })`: an Ant Design warning `该密钥只显示一次，请立即复制保存。` and `Typography.Paragraph copyable={{ text: secret.key }} code` with the raw key. Export `GatewaySecretDialog` as a `Modal` wrapper titled `密钥已生成`; tests render `GatewaySecretContent` directly so Modal portals cannot hide it in SSR.
  - The page loads `listGatewayKeys()` and `getGatewayKeyUsage({from,to})` on mount and whenever the range changes. If usage fails, keep the key list and show an `Alert type="warning"` with the error.
  - Header: title `网关密钥`, range selector (`24 小时`, `7 天`, `30 天`), a Refresh button, and an Add button.
  - Header alerts: `none` → info `未配置网关密钥，数据面当前不鉴权`; `all-disabled` → warning `所有网关密钥均已禁用，所有数据面请求都会被拒绝`.
  - Add modal: name input, enabled switch, submit calls `createGatewayKey`; on success close the modal, open the secret dialog with the response, reload both lists.
  - Switch change calls `updateGatewayKey(name, {enabled})` then reloads.
  - Rotate uses `Popconfirm` (`轮换 {name} 的密钥?`) then `rotateGatewayKey`, then opens the secret dialog and reloads.
  - Delete uses `Popconfirm` (`删除网关密钥 {name}?`) then `deleteGatewayKey` and reload.
  - Sessions opens a drawer; load `getRequests({from: now-24h, to: now, gatewayKey: name, limit: 100})` and render simple `ListCard` rows with model, timestamp, status and tokens. Do not import `SessionCard` from `App.tsx` (circular import).
- `en-US.ts` additions:
  ```ts
  '网关密钥': 'Gateway keys',
  '添加网关密钥': 'Add gateway key',
  '未配置网关密钥，数据面当前不鉴权': 'No gateway keys are configured; the data plane is currently unauthenticated.',
  '所有网关密钥均已禁用，所有数据面请求都会被拒绝': 'All gateway keys are disabled; every data-plane request will be rejected.',
  '密钥已生成': 'Key generated',
  '该密钥只显示一次，请立即复制保存。': 'This key is shown only once. Copy it now.',
  '轮换': 'Rotate',
  '轮换 {name} 的密钥?': 'Rotate the key for {name}?',
  '删除网关密钥 {name}?': 'Delete gateway key {name}?',
  '启用 {name}': 'Enable {name}',
  '密钥': 'Key',
  '请求密钥': 'Request key',
  ```

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test -- --run api/client.test.ts pages/GatewayKeys.test.tsx nav.test.ts`
Expected: PASS.

Run typecheck: `cd web && npx tsc --noEmit`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): add gateway keys page"
```

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit (`feat(web): add gateway keys page`).
- Files changed: `web/src/api/types.ts` (`GatewayKey`, `KeyUsageCounts`, `GatewayKeyUsage`, `GatewayKeyUsageResponse`, `RequestRow.gatewayKey`), `web/src/api/client.ts` (six gateway-key calls, `getGatewayKeyUsage` normalization, `RequestQuery.gatewayKey` mapped to `key`), `web/src/api/client.test.ts` (create/usage/request-filter tests), `web/src/nav.ts` (`gateway-keys` page, order and label), `web/src/nav.test.ts`, `web/src/App.tsx` (`KeyOutlined` icon and page switch), `web/src/pages/GatewayKeys.tsx` (new page, key card, state classifier, Sessions drawer, add/rotate/delete flows and one-time secret dialog), `web/src/pages/GatewayKeys.test.tsx`, `web/src/locales/en-US.ts`, plus this plan.
- Tests/commands and results: RED `cd web && npm test -- --run api/client.test.ts pages/GatewayKeys.test.tsx nav.test.ts` — 3 files failed, 6 tests failed, `GatewayKeys` module missing and the new client/nav symbols absent (log: `.superpowers/sdd/2026-11-07-gateway-keys/task-5-red.log`). GREEN same focused command — 3 files / 18 tests passed. Full `cd web && npm test` — 24 files / 174 tests passed. `npx tsc --noEmit` passed with no output. `npm run build` passed; a grep of `internal/admin/dist` for the fake test secrets found none.
- Review findings and fixes: self-review only (controller review follows). Deleted keys with retained usage are rendered as read-only historical `ListCard`s (`已删除` tag, no switch/actions), matching the design; usage failures are isolated from the key list via `Promise.allSettled` and shown as a warning alert; list failures show an error alert and do not mislabel the page as unconfigured; the secret dialog only exists while its response is held in component state, and the page never fetches raw keys. Sessions rows are rendered locally with `ListCard`, avoiding the `App.tsx` import cycle. No fixes were required after review.
- Deferred items/limitations: Task 6 adds the Sessions page `网关密钥` filter and detail row using the `RequestRow.gatewayKey`/`RequestQuery.gatewayKey` fields added here; UI behavior was verified by SSR markup, Vitest and TypeScript only (no browser visual regression tests); the usage totals object is typed and normalized but not rendered as a separate summary because the page shows per-key counters.

---

### Task 6: Sessions page key filter and detail

**Files:**
- Modify: `web/src/pages/Requests.tsx`
- Modify: `web/src/pages/Requests.test.ts`

**Interfaces:**
- Consumes: `RequestRow.gatewayKey`, `RequestQuery.gatewayKey` and `getRequests` from Task 5.
- Produces: `gatewayKeyLabel(row: RequestRow): string` (returns `key:<name>` or `''`).

- [x] **Step 1: Write the failing test**

Add to `web/src/pages/Requests.test.ts`:

```ts
it('labels the gateway key only when present', () => {
  expect(gatewayKeyLabel(base)).toBe('');
  expect(gatewayKeyLabel({ ...base, gatewayKey: 'claude-code' })).toBe('key:claude-code');
});
```

- [x] **Step 2: Run the test to verify it fails**

Run: `cd web && npm test -- --run pages/Requests.test.ts`
Expected: FAIL because `gatewayKeyLabel` does not exist.

- [x] **Step 3: Implement**

- Export `gatewayKeyLabel(row)` as above.
- Add `const [gatewayKey, setGatewayKey] = useState('')`.
- Pass `gatewayKey: gatewayKey.trim() || undefined` to `getRequests`; add `gatewayKey` to the `load` dependency list.
- Add an `Input` with placeholder `t('请求密钥')` beside the provider/route filters.
- Append `gatewayKeyLabel(r)` to the `ListCard` subtitle when non-empty.
- Add a detail row `<Descriptions.Item label={t('请求密钥')}>{selected.gatewayKey || '—'}</Descriptions.Item>` after `route`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test -- --run pages/Requests.test.ts`
Expected: PASS.

Run typecheck: `cd web && npx tsc --noEmit`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add web/src/pages/Requests.tsx web/src/pages/Requests.test.ts
git commit -m "feat(web): filter sessions by gateway key"
```

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit (`feat(web): filter sessions by gateway key`).
- Files changed: `web/src/pages/Requests.tsx` (`gatewayKeyLabel` export, gateway-key filter state/input/query dependency, subtitle label, detail row), `web/src/pages/Requests.test.ts` (label test), plus this plan.
- Tests/commands and results: RED `cd web && npm test -- --run pages/Requests.test.ts` — 1 test failed with `gatewayKeyLabel is not a function` (log: `.superpowers/sdd/2026-11-07-gateway-keys/task-6-red.log`). GREEN same focused command — 4 tests passed. Full `cd web && npm test` — 24 files / 175 tests passed. `npx tsc --noEmit` passed with no output. `npm run build` passed.
- Review findings and fixes: self-review only (controller review follows). The filter trims input and maps through the existing `RequestQuery.gatewayKey`/`getRequests` path without duplicating URL construction; `gatewayKeyLabel` returns an empty string when the key is absent so the subtitle has no stray separator; the detail row follows `route` and falls back to `—`. No fixes were required.
- Deferred items/limitations: UI behavior was verified by Vitest, TypeScript and the production build only (no browser visual regression tests); the filter matches the exact key name, consistent with the admin query parameter, and is not a prefix search.

---

### Task 7: Documentation and full verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `example.yaml` (only if Task 1 left the block incomplete)

**Interfaces:**
- Consumes: all prior tasks.
- Produces: documentation matching the shipped behavior; no code interfaces.

- [x] **Step 1: Update README**

- `### gateway` table: add `keys` with default "empty (data plane open)" and a one-line description.
- Add a `### Gateway keys` subsection after `### gateway` covering:
  - the YAML example and `enabled`;
  - accepted headers and the any-candidate rule;
  - empty list = open, any entry = required, all-disabled = every request 401;
  - `/api/hello` is public; `count_tokens` is protected; query-string keys are not supported;
  - 401 shape and the warning log;
  - admin create/rotate return the secret once; later reads are masked;
  - rotation and deletion take effect on the next request;
  - per-key usage source and the no-backfill limitation.
- Admin API route list: add the six gateway-key routes and `GET /api/gateway-key-usage`.
- Requests section: document `key=<name>` and the `gatewayKey` response field.
- Stats section: document `key_rollups`, retention, and that old rows have no key.
- Security notes: gateway keys are plaintext in YAML like provider keys; protect the file; only masked previews reach the browser.

- [x] **Step 2: Update AGENTS.md**

- Configuration invariants: empty keys = open; any entry = auth on; duplicates, short keys and duplicate values rejected; names ≤64, keys 8–512; admin writes do not expand `$`.
- Admin API invariants: CRUD routes; create/rotate are the only raw-key responses; `GET /api/config` exposes only `keysSet`/`authRequired`; `PUT /api/config` preserves nil keys and clears on `[]`.
- Stats invariants: `requests.gateway_key`, `key_rollups`, key filter, cleanup shares rollup retention, no backfill, 401s are not recorded.
- Gateway invariants: chain is hello → auth → count_tokens → forward; any supported header may authenticate; all keys compared constant-time; disabled matches only affect the log reason.
- Web invariants: `网关密钥` page; one-time secret dialog; key filter on Sessions.

- [x] **Step 3: Run the full Go gate**

Run:

```bash
cd /root/code/go-gost/llm-gw
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: all succeed.

- [x] **Step 4: Run the full web gate and rebuild the embedded bundle**

Run:

```bash
cd /root/code/go-gost/llm-gw/web
npm test
npx tsc --noEmit
npm run build
```

Then rebuild the Go binary against the fresh bundle:

```bash
cd /root/code/go-gost/llm-gw
GOWORK=off go build -o llm-gw .
```

Expected: all succeed. Note in the final report that there are no browser visual regression tests; UI behavior is covered by TypeScript, Vitest and HTTP checks.

- [x] **Step 5: Commit**

```bash
git add README.md AGENTS.md example.yaml
git commit -m "docs: document gateway keys"
```

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit (`docs: document gateway keys`).
- Files changed: `README.md` (gateway `keys` table row, new `Gateway keys` subsection, console page table row, gateway-key admin routes and request `key`/`gatewayKey` fields, `key_rollups` retention/backfill notes, security notes, project layout), `AGENTS.md` (architecture row, new Gateway invariants section, configuration/admin/stats/web invariants), `docs/superpowers/plans/2026-11-07-gateway-keys.md` (this record). `example.yaml`: verified the Task 1 commented keys block is present and complete; unchanged.
- Tests/commands and results: from `/tmp/opencode/llm-gw-gateway-keys`: `GOWORK=off go build ./...` passed; `GOWORK=off go vet ./...` passed with no output; `GOWORK=off go test ./... -count=1` passed for every package. From `web/`: `npm test` — 24 files / 175 tests passed; `npx tsc --noEmit` passed with no output; `npm run build` passed and rebuilt `internal/admin/dist` (Vite emitted only the pre-existing >500 kB chunk warning). Back in the repository root: `GOWORK=off go build -o llm-gw .` passed and `./llm-gw version` printed `dev`.
- Review findings and fixes: self-review only (controller review follows). Checked the documentation against the shipped code: route list matches `server.go`; create/rotate response bodies and status codes match `handlers_keys.go`; `GET /api/config` `keysSet`/`authRequired` and `PUT` nil-preserve/`[]`-clear behavior match `handlers.go`; 401 status, headers, body and warning fields match `auth.go`; retention, key filter and no-backfill behavior match `store.go`; one-request-per-connection and all-disabled warning match the implementation. No code changes were required; drafting fixes were limited to wording and line wrapping.
- Deferred items/limitations: no browser visual regression tests; UI behavior is covered by TypeScript, Vitest and HTTP checks only, and this task changed no UI code. The gates ran from the isolated worktree rather than the plan's hardcoded `/root/code/go-gost/llm-gw` path, per the ledger ruling. The pre-existing unrelated change to `web/node_modules/.vite/vitest/results.json` was not included in the commit.

---

## Final review fix wave

Date: 2026-11-07
Status: complete

The final whole-branch review returned "With fixes" and recorded one Critical and
three Important findings in
`.superpowers/sdd/2026-11-07-gateway-keys/final-review-findings.md`. All four,
plus the listed minor test additions, are fixed in one wave with no scope
expansion.

### Findings and fixes

1. **Critical — unbounded head-line allocation.** `readGatewayAuthHead` used
   `bufio.Reader.ReadBytes('\n')`, which accumulates every `ErrBufferFull`
   fragment before the 1 MiB check runs. It now reads fragments with
   `ReadSlice('\n')`, checks `len(head)+len(line)` against
   `gatewayAuthHeaderLimit` before each append, and continues on
   `bufio.ErrBufferFull`. A single unterminated line can no longer allocate
   more than the limit plus one 4 KiB `bufio` buffer. The 15s read deadline is
   unchanged.
2. **Important — log target could leak userinfo and control characters.**
   `gatewayAuthLogTarget` now keeps the `"*"` case, parses with `url.Parse`,
   returns the path re-escaped from the decoded `u.Path` so lower-case control
   escapes such as `%0a` become visible `%0A`, returns `/` when the path is
   empty (absolute-form userinfo targets), and never returns the raw target,
   userinfo or query. `sanitizeGatewayAuthMethod` returns `-` for any method
   containing bytes outside printable ASCII `0x21..0x7e`; both warn calls use
   it.
3. **Important — config read-modify-write wiped provider keys and gateway
   fields.** `configView.Gateway` now exposes `readTimeout` and `userAgent`, and
   `handleGetConfig` fills them. `handlePutConfig` preserves each existing
   provider's `keys` when the decoded field is nil, keyed by provider name; an
   explicit `[]` still clears and a new provider stays keyless. Gateway key and
   admin password preservation are unchanged. README and AGENTS now document
   that PUT is a full replacement for exposed fields, write-only values are
   preserved when absent, and non-exposed provider fields are not
   round-trippable. `web/src/api/types.ts` mirrors the expanded gateway view.
4. **Important — `$` and control characters in key names/values.**
   `validateGatewayKeys` now rejects `$` in names and values with the exact
   messages `gateway.keys[%d]: name must not contain '$'` and
   `gateway.keys[%d]: key must not contain '$'`, and rejects control characters
   in values like it already did for names. Errors still never echo a value.
   README and AGENTS document the environment-expansion restriction.
5. **Minor test additions.** `TestInsertRecordsGatewayKeyAndRollups` now writes
   and asserts `CachedTokens`; config tests cover the exact 8/512 boundaries,
   whitespace trimming, a `$` and control characters in both names and values,
   and environment expansion of a key name; admin tests cover provider-key and
   custom gateway-field round-trips and a rejected `${MISSING}` gateway key.

### Files changed

- `internal/gateway/auth.go`, `internal/gateway/auth_test.go`
- `internal/config/config.go`, `internal/config/config_test.go`
- `internal/admin/handlers.go`, `internal/admin/handlers_keys_test.go`
- `internal/stats/store_test.go`
- `web/src/api/types.ts`
- `README.md`, `AGENTS.md`
- `docs/superpowers/plans/2026-11-07-gateway-keys.md` (this section)

### Verification gates

Run from `/tmp/opencode/llm-gw-gateway-keys`:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit && npm run build
cd .. && GOWORK=off go build -o llm-gw .
```

All passed. `go test` passed for every package; `npm test` passed 24 files /
175 tests; `npx tsc --noEmit` produced no output; `npm run build` rebuilt
`internal/admin/dist` with only the pre-existing >500 kB chunk warning; the
final `./llm-gw version` printed `dev`.

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit (`fix: harden gateway key auth, validation and config round-trip`).
- Files changed: `internal/gateway/auth.go` (fragment-capped head reader, safe log target, method sanitizer), `internal/gateway/auth_test.go` (oversized-line reader test, expanded log-target table, method table), `internal/config/config.go` (`$` and control-character rejection), `internal/config/config_test.go` (name env expansion, trimmed key, `$`/control cases, 8/512 boundaries), `internal/admin/handlers.go` (gateway view fields, provider-key preservation), `internal/admin/handlers_keys_test.go` (provider key plus gateway field round-trip, rejected `${MISSING}`), `internal/stats/store_test.go` (cached-token assertion), `web/src/api/types.ts` (`ConfigResponse.gateway`), `README.md` and `AGENTS.md` (restrictions and PUT contract), plus this plan section.
- Tests/commands and results: focused `GOWORK=off go test ./internal/gateway -run 'TestGatewayAuthLogTargetStripsQuery|TestSanitizeGatewayAuthMethod|TestReadGatewayAuthHeadRejectsOversizedLine' -count=1` passed; `./internal/config -run 'TestLoadGatewayKeys|TestValidateGatewayKeys'` passed; `./internal/admin -run 'TestPutConfig'` passed; `./internal/stats -run TestInsertRecordsGatewayKeyAndRollups` passed. Full gates listed above all passed.
- Review findings and fixes: self-review after the fix wave. Verified the head reader's worst case reads at most `gatewayAuthHeaderLimit + 4096` bytes; the log target returns only a canonically escaped path or `/`; the method sanitizer rejects space, DEL, newline and non-ASCII; `handlePutConfig` distinguishes nil from `[]` for provider keys and preserves keys only for existing names; validation messages never contain the offending value. No issues remained after the focused fixes (the first log-target version preserved lower-case `RawPath`; the test caught it and the path is now re-escaped from the decoded value).
- Deferred items/limitations: the optional handler-level oversized-head test was not added (the unit test pins the allocation bound); the last-key deletion warning, provider `userAgent`/`headers` view exposure and the x keepalive root fix stay out of scope; no browser visual regression tests. The unrelated `web/node_modules/.vite/vitest/results.json` change was not committed. Nothing was pushed.

### Follow-up: long header-line regression

Date: 2026-11-07
Status: complete

The re-review found that the fragment-capped `readGatewayAuthHead` above tested
each `bufio.ErrBufferFull` fragment for blankness instead of the whole logical
line. For a header line whose content length is 4095/4096 modulo 4096, the
final fragment is exactly `\r\n` or `\n`; the reader stopped there with a
truncated head, `textproto.ReadMIMEHeader` then returned `EOF`, and `Handle`
answered 400 for an otherwise valid request. It failed closed, but it regressed
the previous `bufio.Reader.ReadBytes` behavior.

`readGatewayAuthHead` now accumulates each logical line in a local `line`
buffer across fragments, checks the 1 MiB cap before every append, then appends
the complete line to `head` and only then calls `isBlankGatewayAuthLine`.
Oversized lines still return `errGatewayAuthHeadTooLarge`, and short lines,
malformed heads and the cap are unchanged. `TestReadGatewayAuthHeadLongLine`
covers header line content lengths 4095, 4096, 8191 and 8192, each followed by
a normal header and the blank terminator; it fails on the previous
implementation.

**Implementation record**
- Date/status: 2026-11-07 — complete.
- Repository + commit: `llm-gw` (worktree `/tmp/opencode/llm-gw-gateway-keys`) — see commit (`fix(gateway): accumulate fragments before testing head blank line`).
- Files changed: `internal/gateway/auth.go` (per-line fragment accumulation), `internal/gateway/auth_test.go` (`TestReadGatewayAuthHeadLongLine`), `docs/superpowers/plans/2026-11-07-gateway-keys.md` (this note).
- Tests/commands and results: `GOWORK=off go test ./internal/gateway -run 'GatewayAuth|ReadGatewayAuthHead' -count=1` passed; `GOWORK=off go test ./internal/gateway -count=1` passed; `GOWORK=off go build ./...` and `GOWORK=off go vet ./...` passed. The new test failed on the previous reader for all four lengths (truncated head, following header missing) and passes with the fix.
- Review findings and fixes: the test initially used the long header value length as the target and did not fail on the old reader; it was corrected to make the header line's total content length (including the `X-Long: ` prefix) match 4095/4096/8191/8192 so the terminating CRLF is a lone fragment, which reproduces the regression.
- Deferred items/limitations: none. Nothing was pushed.

