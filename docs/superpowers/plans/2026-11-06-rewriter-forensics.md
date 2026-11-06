# Rewriter Forensics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add request IDs, structured rewriter error fields and bounded original/rewritten body capture so a client-visible rewriter defect can be traced to one request and compared before/after conversion.

**Architecture:** A response-header rewriter injects `X-LLM-GW-Request-ID` from the x session ID. The body rewriter wrapper records inbound protocol and error stage/direction/SSE phase into `stats.Facts`. A new `internal/capture.Manager` decides policy/on-demand capture and buffers bounded previews; `stats.Recorder` persists them to a `request_captures` table. The admin API exposes capture rules and request detail, and the Requests page shows the new fields and previews.

**Tech Stack:** Go 1.26, x body/header rewrite plugins, SQLite (modernc), React 18, Ant Design, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-06-rewriter-forensics-design.md`

## Global Constraints

- Do not modify `../x` or any sibling repository.
- Request ID covers node-forwarded responses only; gateway-generated 502/503 and request-side failures are a documented v1 gap.
- Capture is off by default; `stats.storeBody` is removed in favour of `stats.capture`.
- Capture modes: `off`, `errors`, `sample`; on-demand rules are time/limit bounded.
- `clientModel` rules use a case-insensitive glob; route rules are exact.
- Rule maxima: limit 1000, TTL 1 hour. Oldest matching rule wins; only the winner consumes its limit.
- SSE captures store first/last events and an event count, never the full stream.
- `maxBytes` applies per preview field; hashes cover the full logical rewritten body.
- Capture config is live on SIGHUP/admin save; in-memory rules survive reloads but not restarts.
- SQLite migrations are additive; old databases keep working.
- Commit after each completed task with its Implementation record.

## Review Focus

- A normal node response must carry a request ID equal to the stats row's `requestId`.
- A rewriter error must record direction/stage/inbound protocol/SSE phase and a structured log line.
- Capture off must create no rows and buffer no bodies.
- `errors` mode must persist only failed requests; successful requests must discard buffered capture.
- `sample` and on-demand rules must capture the intended subset and stop at limit/TTL.
- A failed rewrite must keep the original input and an empty rewritten preview.
- SSE capture must count events and keep first/last previews without storing the stream.
- Capture previews must be truncated per field and total memory must stay bounded.
- Existing databases and configs without `capture` must keep working.
- Gateway-generated 502/503 responses are documented as lacking the request ID.

---

### Task 1: Request ID header and structured rewriter errors

**Files:**
- Create: `internal/rewriter/header.go`
- Create: `internal/rewriter/header_test.go`
- Modify: `internal/rewriter/rewriter.go`
- Modify: `internal/stats/facts.go`
- Modify: `internal/stats/store.go`
- Modify: `internal/stats/store_test.go`
- Modify: `internal/stats/recorder.go`
- Modify: `internal/gateway/build.go`
- Modify: `internal/gateway/gateway.go`
- Create/modify gateway tests for the header

**Interfaces:**
- Produces:
  ```go
  const RequestIDHeaderName = "X-LLM-GW-Request-ID"

  type HeaderRewriter struct{}
  func (r *HeaderRewriter) Rewrite(ctx context.Context, b []byte, opts ...corerewriter.RewriteOption) ([]byte, error)

  type Options struct {
      Provider string
      Protocol string
      Route    string
      Node     string
      Sink     Sink
      Logger   logger.Logger
  }
  func New(eng *engine.Engine, opts Options) *Rewriter

  type Sink interface {
      Add(sid, provider, protocol string, res engine.Result)
      AddInboundProtocol(sid, protocol string)
      AddRewriteError(sid, stage, direction, ssePhase string, eventIndex int)
  }
  ```
- `stats.Request` gains `InboundProtocol`, `ErrorStage`, `ErrorDirection`, `SSEPhase`, `EventIndex`.
- Gateway registers the header rewriter under `llm-gw-request-id` and adds a response-header rewrite to every node.

- [ ] **Step 1: Write the failing header rewriter test**

Create `internal/rewriter/header_test.go`:

```go
package rewriter

import (
    "strings"
    "testing"

    corerewriter "github.com/go-gost/core/rewriter"
)

func TestHeaderRewriterAddsRequestID(t *testing.T) {
    in := []byte("Content-Type: application/json\r\n\r\n")
    md := map[string]any{"sid": "req-123", "direction": "response", "kind": "header"}
    out, err := (&HeaderRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(md))
    if err != nil {
        t.Fatal(err)
    }
    s := string(out)
    if !strings.Contains(s, "X-Llm-Gw-Request-Id: req-123") {
        t.Fatalf("header missing: %q", s)
    }
    if !strings.Contains(s, "Content-Type: application/json") {
        t.Fatalf("original header missing: %q", s)
    }
}

func TestHeaderRewriterFailsOpen(t *testing.T) {
    in := []byte("not a header block")
    out, err := (&HeaderRewriter{}).Rewrite(nil, in, corerewriter.MetadataRewriteOption(
        map[string]any{"sid": "req-123"}))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if string(out) != string(in) {
        t.Fatalf("output changed: %q", out)
    }
}
```

- [ ] **Step 2: Run the header test to verify it fails**

Run: `GOWORK=off go test ./internal/rewriter -run TestHeaderRewriter -count=1`

Expected: FAIL because `HeaderRewriter` is undefined.

- [ ] **Step 3: Implement the header rewriter and helper mapping**

Create `internal/rewriter/header.go` with:

- `RequestIDHeaderName = "X-LLM-GW-Request-ID"`.
- `HeaderRewriter.Rewrite` option parsing copied from `Rewriter.Rewrite`.
- Extract `sid` from metadata; return the input unchanged when empty.
- Parse the header block with `textproto.NewReader(...).ReadMIMEHeader()`.
- Set the request ID header, serialize with `http.Header.Write`, append `\r\n`, and return it.
- Any parse/write error returns the original bytes and `nil` (fail open).

Add to `internal/rewriter/rewriter.go`:

```go
func inboundProtocol(uri string) string
func rewriteStage(direction, ssePhase string) string
```

Mappings: `/v1/chat/completions` -> `openai-chat`, `/v1/responses` -> `openai-responses`, `/v1/messages` -> `anthropic`, `/v1beta/models/` -> `gemini`; request direction -> `request_rewrite`; response with an SSE phase -> `stream_rewrite`; other response -> `response_rewrite`.

- [ ] **Step 4: Run the header test**

Run: `GOWORK=off go test ./internal/rewriter -run TestHeaderRewriter -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing stats field test**

Add to `internal/stats/store_test.go`:

```go
func TestRequestRewriteFieldsRoundTrip(t *testing.T) {
    s := newStore(t)
    ctx := context.Background()
    want := Request{
        ID: "rid", SID: "rid", TS: time.Now().UTC(),
        InboundProtocol: "anthropic",
        ErrorStage:      "stream_rewrite",
        ErrorDirection:  "response",
        SSEPhase:        "end",
        EventIndex:      7,
    }
    if err := s.Insert(ctx, want); err != nil {
        t.Fatal(err)
    }
    got, err := s.Get(ctx, "rid")
    if err != nil || got == nil {
        t.Fatalf("get = %+v err=%v", got, err)
    }
    if got.InboundProtocol != want.InboundProtocol || got.ErrorStage != want.ErrorStage ||
        got.ErrorDirection != want.ErrorDirection || got.SSEPhase != want.SSEPhase ||
        got.EventIndex != want.EventIndex {
        t.Fatalf("fields = %+v", got)
    }
}
```

- [ ] **Step 6: Run the stats test to verify it fails**

Run: `GOWORK=off go test ./internal/stats -run TestRequestRewriteFieldsRoundTrip -count=1`

Expected: FAIL because the fields do not exist.

- [ ] **Step 7: Implement the fields through Facts, Request and SQLite**

- `stats.Fact`: add `InboundProtocol`, `ErrorStage`, `ErrorDirection`, `SSEPhase`, `EventIndex`.
- `Facts.SetInboundProtocol` / `Facts.SetRewriteError` update those fields with the SID lock.
- `stats.Request`: add the five JSON fields (`inboundProtocol`, `errorStage`, `errorDirection`, `ssePhase`, `eventIndex`).
- `ensureColumns("requests", ...)`: add `inbound_protocol`, `error_stage`, `error_direction`, `sse_phase` TEXT and `event_index` integer.
- Update `Insert`, `Query`, `Get` and `scanRequests` column lists/scans.
- `Recorder`: copy the Fact fields into `Request`.
- `gateway.build.go`: pass the route, node, provider, protocol, sink and logger to `rewriter.New`; use the new `Options` struct.
- `gateway.gateway.go`/`build.go`: register `HeaderRewriter` under `llm-gw-request-id`; add `RewriteResponseHeader: []xconfig.HTTPHeaderRewriteConfig{{Rewriter: "llm-gw-request-id"}}` to each node.
- `Rewriter.Rewrite`: set inbound protocol from URI; on error set stage/direction/SSE phase and log a structured error with `sid`, route, node, provider, protocol, direction, URI, SSE phase and event index.

- [ ] **Step 8: Add the gateway header test**

In a gateway test with a local upstream, send a chat request and assert the response header `X-LLM-GW-Request-ID` is non-empty. If practical, query the stats store and assert it equals the recorded `requestId`.

Run: `GOWORK=off go test ./internal/gateway -run 'Test.*RequestID' -count=1`

Expected: PASS.

- [ ] **Step 9: Run the focused and full Go tests**

```bash
GOWORK=off go test ./internal/rewriter ./internal/stats ./internal/gateway -count=1
GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1
```

Expected: all PASS.

- [ ] **Step 10: Fill the Implementation record and commit**

```bash
git add internal/rewriter internal/stats internal/gateway \
  docs/superpowers/specs/2026-11-06-rewriter-forensics-design.md \
  docs/superpowers/plans/2026-11-06-rewriter-forensics.md
git commit -m "feat(rewriter): add request IDs and structured errors"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / 6248fae9 (code, spec and plan); this record commit is the follow-up
- Files changed: `internal/rewriter/header.go`, `internal/rewriter/header_test.go`, `internal/rewriter/helpers_test.go`, `internal/rewriter/rewriter.go`, `internal/stats/facts.go`, `internal/stats/store.go`, `internal/stats/store_test.go`, `internal/stats/recorder_test.go`, `internal/stats/recorder.go`, `internal/gateway/build.go`, `internal/gateway/gateway_test.go`, spec and plan.
- Tests/commands and results: `GOWORK=off go test ./internal/rewriter ./internal/stats ./internal/gateway -count=1` PASS; `GOWORK=off go build ./...` PASS; `go vet ./...` PASS; `go test ./... -count=1` PASS. The new gateway test asserts a non-empty `X-LLM-GW-Request-ID` on a forwarded response; the recorder test pins inbound protocol/error stage/direction/SSE phase/index into a stats row.
- Review findings and fixes: The planned gateway test that forces a rewriter failure was not deterministic because `llm-api-converter` logs and passes through malformed payloads instead of returning errors. It was replaced with a focused `stats.Recorder` test that injects Facts and verifies the persisted fields; header behavior remains covered end to end. The header plugin fails open, and header rewrite registration happens before hop parsing. The request ID is deliberately limited to node-forwarded responses.
- Deferred items/limitations: Gateway-generated 502/503 responses and request-side failures still lack the request ID. Header values are not captured. Capture and UI come in later tasks.

---

### Task 2: Capture manager and policy config

**Files:**
- Create: `internal/capture/capture.go`
- Create: `internal/capture/capture_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `example.yaml`
- Modify: `internal/gateway/gateway.go` (create manager and update policy)

**Interfaces:**
- Produces:

```go
type Policy struct {
    Mode       string
    SampleRate float64
    MaxBytes   int
    Retain     time.Duration
}

type Rule struct {
    ID          string
    Route       string
    ClientModel string
    Reason      string
    Limit       int
    TTL         time.Duration
    Created     time.Time
    Expires     time.Time
    Used        int
}

type Capture struct {
    RequestOriginal    string
    RequestRewritten   string
    ResponseOriginal   string
    ResponseRewritten  string
    SSEFirstOriginal   string
    SSEFirstRewritten  string
    SSELastOriginal    string
    SSELastRewritten   string
    RequestSHA256      string
    ResponseSHA256     string
    SSIEvents          int
    Reason             string
    OnError            bool
    CreatedAt          time.Time
}

type Manager struct { /* unexported */ }

func NewManager(p Policy) *Manager
func (m *Manager) SetPolicy(p Policy)
func (m *Manager) AddRule(route, clientModel, reason string, limit int, ttl time.Duration) (Rule, error)
func (m *Manager) Rules() []Rule
func (m *Manager) RemoveRule(id string) bool
func (m *Manager) Start(sid, route, clientModel string) (reason string, ok bool)
func (m *Manager) Offer(sid, direction string, original, rewritten []byte, ssePhase string, eventIndex int)
func (m *Manager) Finish(sid string) (*Capture, bool)
```

- Config:
  ```go
  type Capture struct {
      Mode       string   `yaml:"mode" json:"mode"`
      SampleRate float64  `yaml:"sampleRate" json:"sampleRate"`
      MaxBytes   int      `yaml:"maxBytes" json:"maxBytes"`
      Retain     Duration `yaml:"retain" json:"retain"`
  }
  ```
  `Stats.Capture Capture`; remove `Stats.StoreBody`.

- [ ] **Step 1: Write the failing capture tests**

Cover:

```go
func TestPolicyModes(t *testing.T) // off, errors, sample=1, sample=0
func TestRuleMatchGlobLimitTTL(t *testing.T)
func TestOldestRuleWins(t *testing.T)
func TestSSEFirstLastCount(t *testing.T)
func TestPreviewsTruncated(t *testing.T)
func TestFailedRewriteKeepsOriginal(t *testing.T)
func TestFinishRemovesSession(t *testing.T)
```

Use a `Manager` with `SetPolicy`, fake SIDs and `time.Now` injection where necessary. Assert:
- `off` returns false; `errors` returns true with `OnError`; `sampleRate: 1` always true, `0` false.
- Route exact and client model glob (case-insensitive) match.
- Limit/TTL stop captures; oldest matching rule wins and only it increments.
- Three SSE events produce first/last previews and `SSIEvents == 3`.
- Each preview is at most `MaxBytes`.
- Rewritten nil keeps original and leaves rewritten empty.
- `Finish` returns the capture once.

- [ ] **Step 2: Run the capture tests to verify they fail**

Run: `GOWORK=off go test ./internal/capture -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the capture manager**

Implement policy/rule/session state with a mutex:

- `Start` checks active rules in creation order (expired/limit-exhausted rules are pruned), then `sample`, then `errors`, then `off`; store the decision per SID.
- `Offer` truncates previews with `maxBytes`, keeps first/last SSE previews and increments `SSIEvents` for non-empty response-phase input, and updates full-body SHA-256 hashes with `crypto/sha256`.
- `Finish` removes the session and returns a copy.
- Bounds: `MaxRuleLimit=1000`, `MaxRuleTTL=time.Hour`, max active sessions 1000 and a 64 MiB total-preview budget; refuse new captures beyond the bound.

- [ ] **Step 4: Run the capture tests**

Run: `GOWORK=off go test ./internal/capture -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestCaptureDefaultsAndValidation(t *testing.T) {
    c, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"))
    if err != nil { t.Fatal(err) }
    if c.Stats.Capture.Mode != "off" || c.Stats.Capture.MaxBytes != 32768 ||
        c.Stats.Capture.Retain != Duration(24*time.Hour) {
        t.Fatalf("capture defaults = %+v", c.Stats.Capture)
    }
    for _, body := range []string{
        "stats:\n  capture:\n    mode: always\n",
        "stats:\n  capture:\n    mode: sample\n    sampleRate: 2\n",
        "stats:\n  capture:\n    mode: errors\n    maxBytes: 0\n",
    } {
        if _, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"+body)); err == nil {
            t.Fatalf("expected error for %q", body)
        }
    }
}
```

- [ ] **Step 6: Run the config test to verify it fails**

Run: `GOWORK=off go test ./internal/config -run TestCapture -count=1`

Expected: FAIL because `Stats.Capture` does not exist.

- [ ] **Step 7: Implement the config and gateway policy wiring**

- Add `Capture` to `Stats`; remove `StoreBody`.
- Defaults: mode `off`, sampleRate `0.01`, maxBytes `32768`, retain `24h`.
- Validation: mode `off|errors|sample`; sampleRate 0..1; maxBytes 1024..1048576; retain > 0.
- `example.yaml`: replace `storeBody` with the `capture` block.
- `Gateway` creates `capture.NewManager(policy)` in `New` and calls `SetPolicy` in `apply`; expose `func (g *Gateway) Capture() *capture.Manager`.
- Map `config.Capture` to `capture.Policy` in the gateway.

- [ ] **Step 8: Run config and gateway tests**

```bash
GOWORK=off go test ./internal/config ./internal/gateway -count=1
```

Expected: PASS.

- [ ] **Step 9: Fill the Implementation record and commit**

```bash
git add internal/capture internal/config/config.go internal/config/config_test.go \
  internal/gateway/gateway.go example.yaml \
  docs/superpowers/plans/2026-11-06-rewriter-forensics.md
git commit -m "feat(capture): add policy and on-demand rules"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / 7337e034 (code, config, example and plan); this record commit is the follow-up
- Files changed: `internal/capture/capture.go`, `internal/capture/capture_test.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/gateway/gateway.go`, `example.yaml`, plan.
- Tests/commands and results: `GOWORK=off go test ./internal/capture ./internal/config ./internal/gateway -count=1` PASS; `go build ./...`, `go vet ./...`, `go test ./... -count=1` PASS.
- Review findings and fixes: The config validation is used directly by older tests without `ApplyDefaults`, so zero capture values are treated as “unset” while explicit out-of-range values fail. The capture rule reason stored on the session is `rule:<id>`; the user-provided reason stays on the active rule for the UI. `MaxRuleLimit=1000`, `MaxRuleTTL=1h`, 1000 sessions and a 64 MiB preview budget bound memory. `Stats.Capture` replaces the dead `storeBody` field; the gateway owns one manager and updates its policy on reload.
- Deferred items/limitations: The manager is not wired to the rewriter/recorder yet (Task 3). Rules are in-memory and lost on restart by design. Configs using `storeBody` now fail strict YAML and must migrate to `stats.capture`.

---

### Task 3: Capture persistence, recorder and admin API

**Files:**
- Modify: `internal/rewriter/rewriter.go` (call capture manager)
- Modify: `internal/gateway/build.go` (pass the manager)
- Modify: `internal/gateway/gateway.go` (recorder gets the manager)
- Modify: `internal/stats/store.go`
- Modify: `internal/stats/store_test.go`
- Modify: `internal/stats/recorder.go`
- Create: `internal/admin/handlers_capture.go`
- Create: `internal/admin/capture_test.go`
- Modify: `internal/admin/server.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_stats.go`

**Interfaces:**
- Capture persistence:
  ```go
  func (s *Store) PutCapture(ctx context.Context, requestID string, c *capture.Capture) error
  func (s *Store) GetCapture(ctx context.Context, requestID string) (*capture.Capture, error)
  ```
- `Request` gains `HasCapture bool` and `Capture *capture.Capture` (detail only).
- `Filter` gains `Captured bool`.
- Admin routes:
  - `GET /api/captures` -> `[]capture.Rule`
  - `POST /api/captures` -> `201` rule
  - `DELETE /api/captures/{id}` -> `204`
- `GET /api/requests` supports `captured=1`.
- `GET /api/requests/{id}` includes `capture` when present.

- [ ] **Step 1: Write the failing stats capture tests**

Add to `internal/stats/store_test.go`:

```go
func TestCaptureRoundTripAndCleanup(t *testing.T) {
    s := newStore(t)
    ctx := context.Background()
    req := Request{ID: "rid", SID: "rid", TS: time.Now().UTC()}
    if err := s.Insert(ctx, req); err != nil { t.Fatal(err) }
    cap := &capture.Capture{
        RequestOriginal: "in", RequestRewritten: "out",
        ResponseOriginal: "up", ResponseRewritten: "down",
        Reason: "rule:1", SSIEvents: 2, CreatedAt: time.Now().UTC(),
    }
    if err := s.PutCapture(ctx, "rid", cap); err != nil { t.Fatal(err) }
    got, err := s.GetCapture(ctx, "rid")
    if err != nil || got == nil || got.RequestRewritten != "out" || got.SSIEvents != 2 {
        t.Fatalf("capture = %+v err=%v", got, err)
    }
    row, err := s.Get(ctx, "rid")
    if err != nil || row == nil || !row.HasCapture || row.Capture == nil {
        t.Fatalf("request = %+v err=%v", row, err)
    }
    if _, err := s.db.ExecContext(ctx, `DELETE FROM requests`); err != nil { t.Fatal(err) }
    if err := s.Cleanup(ctx, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil { t.Fatal(err) }
    if got, _ := s.GetCapture(ctx, "rid"); got != nil { t.Fatalf("capture survived cleanup: %+v", got) }
}
```

- [ ] **Step 2: Run the stats test**

Run: `GOWORK=off go test ./internal/stats -run TestCaptureRoundTripAndCleanup -count=1`

Expected: FAIL because capture storage is missing.

- [ ] **Step 3: Implement capture storage**

- `request_captures` table from the spec.
- `PutCapture` (upsert), `GetCapture`, capture cleanup by `created_at`.
- Add `HasCapture` to Query via `EXISTS(...)` and to `Get`; attach `Capture` in `Get`.
- Add `Captured` to `Filter` and its SQL.
- Update scan helpers.
- `Recorder`: after `Take` and before/after request insert, call `mgr.Finish(sid)`; if `OnError` and the request is not failed, drop it; otherwise `PutCapture`.
- `stats.NewRecorder` gains the manager; gateway passes it.
- Rewriter: call `capture.Start` on request direction and `capture.Offer` after every rewrite attempt; pass route/client model.
- Gateway: pass the manager into `registerRewriters` and `stats.NewRecorder`.

- [ ] **Step 4: Run the stats tests**

Run: `GOWORK=off go test ./internal/stats -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing admin capture tests**

Cover:
- `GET /api/captures` empty array.
- `POST` a rule with route + glob, then GET.
- `DELETE` the rule.
- Invalid limit/TTL returns 400.
- Unauthenticated returns 401.
- Request detail returns `capture` and list returns `hasCapture` (reuse a real stats row/capture).

- [ ] **Step 6: Run the admin tests**

Run: `GOWORK=off go test ./internal/admin -run 'TestCapture' -count=1`

Expected: FAIL because the routes do not exist.

- [ ] **Step 7: Implement the admin API**

- Add `capture *capture.Manager` to `Server`, obtained from a `Capture() *capture.Manager` interface on the reloader.
- Register `GET/POST /api/captures`, `DELETE /api/captures/{id}`.
- Validate route/client model/limit/TTL/reason; `503` when the manager is nil.
- `handleRequests`: read `captured=1`.
- `handleRequest`: include `capture` from `Store.Get`.
- Add the new fields to the request JSON and UI-facing types in a later task.

- [ ] **Step 8: Run the admin and full Go gates**

```bash
GOWORK=off go test ./internal/admin ./internal/stats ./internal/gateway ./internal/rewriter -count=1
GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1
```

Expected: all PASS.

- [ ] **Step 9: Fill the Implementation record and commit**

```bash
git add internal/rewriter internal/gateway internal/stats internal/admin \
  docs/superpowers/plans/2026-11-06-rewriter-forensics.md
git commit -m "feat(capture): persist captures and expose admin API"
```

**Implementation record**
- Date/status:
- Repo/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Web UI and documentation

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`
- Modify: `web/src/pages/Requests.tsx`
- Modify: `web/src/locales/en-US.ts`
- Modify: `web/src/i18n.test.ts` if needed
- Modify: `README.md`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes Task 1-3 API fields and routes.
- Produces client functions:
  ```ts
  listCaptureRules(): Promise<CaptureRule[]>
  createCaptureRule(input): Promise<CaptureRule>
  deleteCaptureRule(id: string): Promise<void>
  ```
- `RequestRow` gains `hasCapture`, `inboundProtocol`, `errorStage`, `errorDirection`, `ssePhase`, `eventIndex`, and optional `capture`.

- [ ] **Step 1: Update types and client**

- Add `CaptureRule`, `RequestCapture` and the new `RequestRow` fields.
- Add `capturedOnly` to the requests query and `captured=1`.
- Add capture API functions.
- Add client tests for empty `hasCapture` and capture-rule calls.

- [ ] **Step 2: Update the Requests page**

- Red error tag when `r.status >= 400 || r.error`; show the error text in the subtitle.
- Detail drawer: request ID + copy button, inbound/upstream protocol, error stage/direction, SSE phase/index.
- Capture section: reason, hashes, SSE count, original/rewritten request/response previews, and an empty state.
- Add a "Capture" button/modal with route, client model, limit, TTL and reason; list/delete active rules.
- Add a "captured only" filter checkbox.
- Add all new Chinese source strings to `en-US.ts`.

- [ ] **Step 3: Run the web gate**

```bash
cd web
npm test -- --run
npx tsc --noEmit
npm run build
```

Expected: PASS.

- [ ] **Step 4: Update documentation**

- README: `stats.capture` config, capture modes/limits, request ID header and its node-forwarded-only scope, capture admin API, body-capture privacy, x rewrite blind spots.
- AGENTS: capture defaults/live reload, request ID limitation, capture security and rule maxima.
- Remove `storeBody` references.
- Ensure `example.yaml` loads.

- [ ] **Step 5: Run the final gates**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test -- --run && npx tsc --noEmit && npm run build
```

Expected: all PASS.

- [ ] **Step 6: Fill the Implementation record and commit**

```bash
git add web README.md AGENTS.md docs/superpowers/plans/2026-11-06-rewriter-forensics.md
git commit -m "feat(web): show rewrite forensics and capture controls"
```

**Implementation record**
- Date/status:
- Repo/commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

## Self-Review

- Spec coverage: request ID, structured errors, capture policy/rules/manager,
  storage/cleanup, admin API, request detail/list flag, web UI, privacy/docs and
  tests map to Tasks 1-4.
- Type consistency: `stats.Request` fields, `capture.Rule`/`Capture`,
  `RequestRow`/`CaptureRule` and the API paths are reused consistently.
- Review Focus tests: header equality (gateway), error fields (stats/rewriter),
  capture-off no rows (capture/stats), errors-only discard (recorder test),
  sample/rule limits (capture tests), failed rewrite original (capture test),
  SSE count/first-last (capture test), truncation/memory (capture test),
  additive migration (stats), gateway 502/503 gap (docs).
- Deferred: request ID for gateway-generated 502/503, header capture, full SSE
  streams, redaction, and request-ID propagation to clients through upstream
  headers.
