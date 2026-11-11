# Retry Attempt Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record every upstream attempt that triggered a retry, count those attempts per node/edge in the monitoring statistics, and show them in the node panel's recent sessions so a `dead` node explains itself.

**Architecture:** x carries the discarded attempts inside the existing single recorder object (`http.attempts`), so the data plane still records exactly one row per client request. cuttle decodes that field with its own small struct (no dependency on a new x tag), stores one row per attempt in a new `request_attempts` table in the parent row's transaction, aggregates counters from the same `seq` watermark the graph already uses, and exposes them through `/api/stats/graph`, `/api/events`, `/api/attempts` and the request detail payload.

**Tech Stack:** Go 1.27, `database/sql` + SQLite, x forwarder/recorder, React 18 + Ant Design + Vite console, vitest.

**Spec:** `docs/superpowers/specs/2026-11-11-retry-attempt-observability-design.md`

## Global Constraints

- cuttle Go commands run with `GOWORK=off`; cuttle has no `replace` directives and must not gain one.
- x runs its own gates (`go build ./...`, `go vet ./...`, `go test -count=1 ./internal/util/forwarder/...`).
- cuttle pins `github.com/go-gost/x v0.31.1` for Tasks 1-6 and 8; only Task 7 changes it, and only after approval. The cuttle side must compile and pass with `v0.31.1` (attempts stay zero until the bump).
- `requests`, `rollups`, `key_rollups`, `summary`, token accounting and `/api/stats/errors` keep their current meaning: only `request_attempts` gains rows.
- Code comments in English only. Console strings are Chinese source keys translated in `web/src/locales/en-US.ts`; every new key gets an entry there.
- `web/` builds into `internal/admin/dist` (git-ignored, embedded): rebuild the console before a Go build whenever `web/src` changed.
- Never push, tag or deploy without explicit approval; commit locally after every task, including that task's Implementation record.
- Task 1 is deliberate sibling-repository work (the spec's cross-repository section): commit it in `go-gost/x` with its own record; cuttle never references the new x type, so no `replace` is needed.
- Attempt response headers pass through the existing `captureHeaders` whitelist (the same one `request_captures` uses); never store raw headers.
- Update `cuttle/AGENTS.md` wherever an invariant this plan changes is stated (the "never insert a row per attempt" stats bullet, the live-monitor tail).

## Review Focus

- A discarded attempt whose response body never arrives (client abort, read deadline) — the attempt must still be recorded with its status and an empty preview, and the retry must proceed.
- A request with no retry state (`retries: 0`, `Expect: 100-continue`, body over 32 MiB) — no attempt must be recorded and no nil-pointer path may be reached.
- A recorder payload from the current x (no `attempts` field) — cuttle must decode nothing, insert no attempt rows and keep every existing statistic identical.
- Attempts for a route/node no longer in the topology — the graph's `RowFilter` must drop them so no phantom node card appears.
- The same recorder payload delivered twice, or a parent row cleaned up while attempts remain — no double counting in the graph and no unbounded orphan rows.

---

### Task 1: x records discarded attempts

**Files:**
- Modify: `x/recorder/recorder.go` (`HTTPRecorderObject`, new `HTTPAttemptRecorderObject`)
- Modify: `x/internal/util/forwarder/retry.go` (`retryState`, `attemptBodyLimit`, `addAttempt`)
- Modify: `x/internal/util/forwarder/sniffer_http.go` (dial-failure branch, retryable-status branch, keep-alive re-init)
- Test: `x/internal/util/forwarder/sniffer_http_retry_test.go`

**Interfaces:**
- Consumes: existing `retryState` (`budget`, `codes`, `replay`, `start`), `retryNode`, `retryUpstream`, `retryRequest` test helpers.
- Produces: `xrecorder.HTTPAttemptRecorderObject{Attempt int; Node string; StatusCode int; Error string; DurationMS int64; BytesIn int64; BytesOut int64; ResponseHeader http.Header; ResponseBody []byte}` serialized as `http.attempts`, and `(*retryState).addAttempt(a xrecorder.HTTPAttemptRecorderObject)`.

- [x] **Step 1: Write the failing test**

Append to `x/internal/util/forwarder/sniffer_http_retry_test.go`:

```go
func TestHandleHTTP_RecordsDiscardedAttempt(t *testing.T) {
	a := newRetryUpstream(t, upstreamReply{status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","message":"Rate limit exceeded."}}`, read: true})
	b := newRetryUpstream(t, upstreamReply{status: http.StatusOK, body: "ok", read: true})

	hh := &sequenceHop{nodes: []*chain.Node{
		retryNode("a", a.addr(), 1, "429,5xx"),
		retryNode("b", b.addr(), 1, "429,5xx"),
	}}
	h := &Sniffer{ReadTimeout: 5 * time.Second}

	status, _, ro := retryRequest(t, h, hh, `{"m":1}`, nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ro.HTTP == nil || len(ro.HTTP.Attempts) != 1 {
		t.Fatalf("recorded attempts = %+v, want 1", ro.HTTP)
	}
	at := ro.HTTP.Attempts[0]
	if at.Attempt != 0 || at.Node != "a" || at.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attempt = %+v, want attempt 0 on node a with 429", at)
	}
	if !strings.Contains(string(at.ResponseBody), "Rate limit exceeded") {
		t.Fatalf("attempt body = %q, want the upstream error body", at.ResponseBody)
	}
	if at.DurationMS < 0 {
		t.Fatalf("attempt duration = %d, want >= 0", at.DurationMS)
	}
}

func TestHandleHTTP_RecordsDiscardedDialFailure(t *testing.T) {
	good := newRetryUpstream(t, upstreamReply{status: http.StatusOK, body: "ok", read: true})

	hh := &sequenceHop{nodes: []*chain.Node{
		retryNode("a", "127.0.0.1:1", 1, "429,5xx"),
		retryNode("b", good.addr(), 1, "429,5xx"),
	}}
	h := &Sniffer{ReadTimeout: 5 * time.Second}
	// The dial function is a HandleOption, not a Sniffer field.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "127.0.0.1:1" {
			return nil, io.ErrUnexpectedEOF
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}

	status, _, ro := retryRequest(t, h, hh, `{"m":1}`, nil, sniffing.WithDial(dial))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ro.HTTP == nil || len(ro.HTTP.Attempts) != 1 {
		t.Fatalf("recorded attempts = %+v, want 1", ro.HTTP)
	}
	if at := ro.HTTP.Attempts[0]; at.StatusCode != 0 || at.Node != "a" || at.Error == "" {
		t.Fatalf("attempt = %+v, want a status-0 dial failure on node a", at)
	}
}
```

Also add a test for a truncated attempt body (the preview read must not break the retry, and the attempt must still be recorded):

```go
func TestHandleHTTP_RecordsAttemptWhenBodyIsCut(t *testing.T) {
	trunc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Promises 1024 bytes, writes a few, then returns: Go closes the
		// connection and the client read fails before the promised length.
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"partial`)
	}))
	t.Cleanup(trunc.Close)
	good := newRetryUpstream(t, upstreamReply{status: http.StatusOK, body: "ok", read: true})

	hh := &sequenceHop{nodes: []*chain.Node{
		retryNode("a", trunc.Listener.Addr().String(), 1, "429,5xx"),
		retryNode("b", good.addr(), 1, "429,5xx"),
	}}
	h := &Sniffer{ReadTimeout: 5 * time.Second}

	status, _, ro := retryRequest(t, h, hh, `{"m":1}`, nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ro.HTTP == nil || len(ro.HTTP.Attempts) != 1 || ro.HTTP.Attempts[0].StatusCode != http.StatusTooManyRequests {
		t.Fatalf("recorded attempts = %+v, want the cut 429 attempt", ro.HTTP)
	}
	if !strings.Contains(string(ro.HTTP.Attempts[0].ResponseBody), "partial") {
		t.Fatalf("preview = %q, want the bytes that did arrive", ro.HTTP.Attempts[0].ResponseBody)
	}
}
```

Finally extend the existing `TestHandleHTTP_NoRetryWithoutRetryMetadata` with:

```go
	if ro.HTTP != nil && len(ro.HTTP.Attempts) != 0 {
		t.Fatalf("recorded attempts = %+v, want none without retry metadata", ro.HTTP.Attempts)
	}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd x && GOWORK=off go test ./internal/util/forwarder/ -run 'RecordsDiscarded|RecordsAttemptWhenBodyIsCut|NoRetryWithoutRetryMetadata' -count=1`
Expected: FAIL — `ro.HTTP.Attempts` is undefined (compile error).

- [x] **Step 3: Implement the recorder field and the two capture points**

In `x/recorder/recorder.go`, add below `HTTPResponseRecorderObject`:

```go
// HTTPAttemptRecorderObject describes one upstream attempt that was discarded
// because its response opted into a retry. The attempt whose response reaches
// the client is the recorder object itself and never appears here.
type HTTPAttemptRecorderObject struct {
	Attempt        int         `json:"attempt"`
	Node           string      `json:"node"`
	StatusCode     int         `json:"statusCode"`
	Error          string      `json:"error,omitempty"`
	DurationMS     int64       `json:"durationMs"`
	BytesIn        int64       `json:"bytesIn"`
	BytesOut       int64       `json:"bytesOut"`
	ResponseHeader http.Header `json:"responseHeader,omitempty"`
	ResponseBody   []byte      `json:"responseBody,omitempty"`
}
```

and to `HTTPRecorderObject`: `Attempts []HTTPAttemptRecorderObject \`json:"attempts,omitempty"\``.

In `x/internal/util/forwarder/retry.go`: add `const attemptBodyLimit = 4 << 10` and `attempts []xrecorder.HTTPAttemptRecorderObject` plus `attemptStart time.Time` to `retryState`; add

```go
// addAttempt appends one discarded attempt. Attempts are capped by the retry
// budget, which bounds the slice to retry.max.
func (rs *retryState) addAttempt(a xrecorder.HTTPAttemptRecorderObject) {
	if rs == nil {
		return
	}
	if len(rs.attempts) > 0 && a.Attempt < len(rs.attempts) {
		return
	}
	rs.attempts = append(rs.attempts, a)
}
```

In `x/internal/util/forwarder/sniffer_http.go`:

1. In `HandleHTTP`, take `dialStart := time.Now()` unconditionally at the top of the `if cc == nil` block, before `h.dial(...)`, and assign `rs.attemptStart = dialStart` next to both `newRetryState(req, node)` call sites inside that block. (`retryState` is created only after the dial resolves, so an attempt-0 duration must come from this local start; the `newRetryState` seed remains the no-dial/reused-connection default.)
2. In the dial-failure retry branch, after `rs = newRetryState(req, node)` and `rs.attemptStart = dialStart`, before spending budget, call `rs.addAttempt(xrecorder.HTTPAttemptRecorderObject{Attempt: attempt, Node: nodeName(node), StatusCode: 0, Error: err.Error(), DurationMS: time.Since(rs.attemptStart).Milliseconds()})`.
3. In the retryable-status branch of `httpRoundTrip` (`retryable status %d for node %s`), before `return false, true, nil`, read the preview with `io.ReadAll(io.LimitReader(resp.Body, attemptBodyLimit))` (ignore the error), call `rs.addAttempt(xrecorder.HTTPAttemptRecorderObject{Attempt: attemptFromContext(ctx), Node: node.Name, StatusCode: resp.StatusCode, DurationMS: time.Since(rs.attemptStart).Milliseconds(), BytesIn: req.ContentLength, BytesOut: resp.ContentLength, ResponseHeader: resp.Header.Clone(), ResponseBody: preview})`. `httpRoundTrip` records its own copy of the recorder object, so also seed `ro.HTTP.Attempts = rs.attempts` on that local object (not on `ho.RecorderObject`).
4. In the keep-alive re-init at the top of `HandleHTTP` (`ro.HTTP = &xrecorder.HTTPRecorderObject{...}`), re-attach `ro.HTTP.Attempts = rs.attempts` when `rs != nil`, so a retry `continue` cannot drop them.
5. Add `func nodeName(n *chain.Node) string { if n == nil { return "" }; return n.Name }` in `retry.go`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd x && GOWORK=off go test ./internal/util/forwarder/ -count=1`
Expected: PASS, including the pre-existing retry tests.

- [x] **Step 5: Commit**

```bash
cd x && git add recorder/recorder.go internal/util/forwarder/retry.go internal/util/forwarder/sniffer_http.go internal/util/forwarder/sniffer_http_retry_test.go
git commit -m "feat(forward): record discarded retry attempts on the recorder object"
```

- [x] **Step 6: Implementation record**

Append to this plan: date/status, repo `go-gost/x` + commit SHA, files changed, commands run and their results, review findings, deferred items. Commit it with the task.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `go-gost/x` / `3ceacae feat(forward): record discarded retry attempts on the recorder object` and fix round 1 `6172a02 fix(forward): time discarded attempts from before the dial` (branch `feat/record-retry-attempts`, based on `ba3888a`)
- Files changed: `recorder/recorder.go` (new `HTTPAttemptRecorderObject`, `HTTPRecorderObject.Attempts`), `internal/util/forwarder/retry.go` (`attemptBodyLimit`, `retryState.attemptStart`/`attempts`, `newRetryState` seeds `attemptStart`, `addAttempt`, `nodeName`), `internal/util/forwarder/sniffer_http.go` (per-attempt start before dial, dial-failure attempt capture, retryable-status preview capture, keep-alive re-init re-attach, row re-attach in `httpRoundTrip`, `recordDialFailure` keeps `Attempts`), `internal/util/forwarder/sniffer_http_retry_test.go` (3 new tests plus the `NoRetryWithoutRetryMetadata` extension)
- Tests: RED `cd x && GOWORK=off go test ./internal/util/forwarder/ -run 'RecordsDiscarded|RecordsAttemptWhenBodyIsCut|NoRetryWithoutRetryMetadata' -count=1` → build failure, `ro.HTTP.Attempts undefined` at 9 sites. GREEN same command → `ok ... 0.013s`; `GOWORK=off go test ./internal/util/forwarder/ -count=1` → `ok ... 0.480s`; whole module `GOWORK=off go test -count=1 ./...` → all packages pass. Gates `GOWORK=off go build ./...`, `GOWORK=off go vet ./...` → PASS. `gofmt -l` on the four files → clean. `-v` run of the new tests shows no stray output.
- Review findings/fixes: the brief's keep-alive re-attach alone does not put attempts on the final row, because `httpRoundTrip` builds a fresh `ro.HTTP` and records that local copy — added `ro.HTTP.Attempts = rs.attempts` next to that fresh object (and `ho.RecorderObject.HTTP.Attempts` in the retry branch). `attemptStart` is seeded in `newRetryState` so the attempt-0 dial-failure path (where `rs` is created after the dial) reports a sane duration instead of `time.Since(time.Time{})`; verified in a throwaway test (0 ms locally, 44-byte preview, `statusCode: 429`, base64 `responseBody`). The dial-failure attempt is recorded only after a successful replay, so a failed replay still yields exactly the single dial-failure row. `recordDialFailure` rebuilds `ro.HTTP`, so it now carries `Attempts` across the rebuild; otherwise a 429-then-dial-failure request would lose its first attempt.
- Deferred/limitations: attempt bodies are capped at 4 KiB (`attemptBodyLimit`) and a cut body keeps whatever arrived; `ResponseBody`/`ResponseHeader` serialize as base64/JSON like the existing recorder body fields, which cuttle must decode. The final attempt's own marker/failCodes handling is untouched, and attempts are not attributed to entrypoints. `go-gost/x` was committed only — not tagged or pushed (Task 7 owns that).
- Fix round 1 (`6172a02`): attempt 0 was not timed from the connect — `retryState` is built only after the dial resolves, so the pre-dial `rs != nil` guard never ran for the first attempt and a 30 s connect timeout would have published 0 ms. `HandleHTTP` now takes `dialStart := time.Now()` unconditionally before `h.dial` and assigns `rs.attemptStart = dialStart` at both `newRetryState` call sites inside `if cc == nil`; the `newRetryState` seed stays only as the no-dial (reused connection) default. New regression test `TestHandleHTTP_DialFailureAttemptKeepsConnectTime` (50 ms sleeping dial, asserts `DurationMS >= 40`) fails with `attempt duration = 0 ms` without the fix and passes with it. Also removed the no-op `ho.RecorderObject.HTTP.Attempts = rs.attempts` in the retryable-status branch (that object's `HTTP` is rebuilt each keep-alive iteration; the recorded row is `httpRoundTrip`'s local copy, seeded a few lines above). Gates re-run: focused tests, `./internal/util/forwarder/`, `GOWORK=off go build ./...`, `GOWORK=off go vet ./...` all PASS.
- Fix round 2 (2026-11-11 / complete, final whole-branch review): repo/commit `go-gost/x` / `1d7306e fix(forward): bound the attempt preview read` on the same branch (base `6172a02`). The review found the attempt preview read at `sniffer_http.go:806` running under the response-head read deadline set before `http.ReadResponse` (`ho.ReadTimeout`, 60 s in cuttle), so a `429` whose body stalls delayed the failover for up to a minute. Files changed: `internal/util/forwarder/retry.go` (`const attemptPreviewTimeout = 2 * time.Second` beside `attemptBodyLimit`), `internal/util/forwarder/sniffer_http.go` (`xio.SetReadDeadline(cc, time.Now().Add(attemptPreviewTimeout))` before the preview read, with a comment that the read error is still ignored and the connection is dropped on this path so no deadline is restored), `internal/util/forwarder/sniffer_http_retry_test.go` (the `retryRequest` body factored into a `t.Fatal`-free `retryRequestErr` so a test can bound the exchange; `newStallingBodyUpstream`, `TestHandleHTTP_PreviewReadIsBounded`, and `TestAttemptWireContract`). Tests: RED `GOWORK=off go test ./internal/util/forwarder/ -run 'PreviewReadIsBounded|AttemptWireContract' -count=1 -v` → `--- FAIL: TestHandleHTTP_PreviewReadIsBounded (6.00s) the retry did not proceed within 6s: the attempt preview read is bounded only by the read timeout` (the upstream answers 429 with `Content-Length: 512` and a stalled body, `Sniffer.ReadTimeout` is 10 s, and the test waits on a `select` with a 6 s bound rather than a sleep); GREEN same command → PASS in 2.00 s, asserting status 200, one hit on the second upstream, `Retries: 1`, and one attempt on node `a` with status 429 and the bytes that arrived before the stall. Gates: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test -count=1 ./internal/util/forwarder/...` → `ok ... 2.524s`; `GOWORK=off go test -race -count=1 ./internal/util/forwarder/ -run 'PreviewReadIsBounded|Retry|Attempt'` → `ok ... 3.109s`; `gofmt` clean on the three files. The wire-contract test (final review finding 5) marshals a recorded attempt and asserts exactly the keys `attempt`/`node`/`statusCode`/`error`/`durationMs`/`bytesIn`/`bytesOut`/`responseHeader`/`responseBody`, so a tag rename cannot slip past x's suite while silently disabling cuttle. Deferred: `attemptPreviewTimeout` is a constant, not a knob; the preview still keeps whatever arrived when the deadline cuts it. Report: `.superpowers/sdd/2026-11-11-retry-attempt-observability/final-review-fix-report.md`.

---

### Task 2: cuttle schema, attempt rows and retention

**Files:**
- Create: `internal/stats/attempts.go`
- Modify: `internal/stats/store.go` (`migrate`, `Insert`, `Cleanup`)
- Modify: `AGENTS.md` (stats invariant about one row per request)
- Test: `internal/stats/attempts_test.go`

**Interfaces:**
- Consumes: `Request` (`ID`, `SID`, `TS`, `Route`, `Node`, `Provider`, `GatewayKey`), `parseNodeName`, the `request_seq` increment inside `Insert`.
- Produces: `stats.Attempt` and `stats.AttemptFilter`, `(*Store).Attempts(ctx, AttemptFilter) ([]Attempt, error)`, `(*Store).AttemptsForRequest(ctx, requestID string) ([]Attempt, error)`.

- [x] **Step 1: Write the failing test**

Create `internal/stats/attempts_test.go`:

```go
func TestInsertWritesAttemptsWithoutTouchingRollups(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	req := Request{
		ID: "req-1", SID: "req-1", TS: ts, Provider: "p1", Route: "r1", Node: "n1",
		Status: 200, Retries: 1,
		Attempts: []Attempt{{
			RequestID: "req-1", Attempt: 0, TS: ts, Route: "r1", Node: "n1", Provider: "p1",
			Status: 429, Error: "Rate limit exceeded.", ErrorClass: "upstream_quota",
			DurationMS: 10, BytesIn: 100, BytesOut: 40,
		}},
	}
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	got, err := s.AttemptsForRequest(context.Background(), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != 429 || got[0].ErrorClass != "upstream_quota" {
		t.Fatalf("attempts = %+v", got)
	}

	sum, err := s.Summary(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 1 || sum.Errors != 0 {
		t.Fatalf("summary = %+v, want 1 request and 0 errors", sum)
	}
	rows, err := s.Query(context.Background(), Filter{From: ts.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("request rows = %d, want 1", len(rows))
	}
}

func TestAttemptsAreReplacedAndPruned(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Hour)
	req := Request{ID: "r", SID: "r", TS: ts, Node: "n1", Attempts: []Attempt{
		{RequestID: "r", Attempt: 0, TS: ts, Node: "n1", Status: 429},
	}}
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Attempts[0].Error = "second delivery"
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := s.AttemptsForRequest(context.Background(), "r")
	if len(got) != 1 || got[0].Error != "second delivery" {
		t.Fatalf("attempts = %+v, want one replaced row", got)
	}

	if err := s.Cleanup(context.Background(), time.Now(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.AttemptsForRequest(context.Background(), "r")
	if len(got) != 0 {
		t.Fatalf("attempts after cleanup = %+v, want none", got)
	}
}
```

Check the actual test-store constructor and `Count` signature in `internal/stats/store_test.go` before writing; use whatever helper already exists.

- [x] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'Attempts' -count=1`
Expected: FAIL — `Attempt` and `Store.AttemptsForRequest` undefined.

- [x] **Step 3: Add the table, the type, the write path and the two query methods**

In `internal/stats/attempts.go`:

```go
// Attempt is one discarded upstream attempt: the response opted into a retry,
// so it never reached the client and is stored beside its parent request.
type Attempt struct {
	RequestID  string `json:"requestId"`
	Attempt    int    `json:"attempt"`
	TS         time.Time `json:"ts"`
	Route      string `json:"route,omitempty"`
	Node       string `json:"node,omitempty"`
	Provider   string `json:"provider,omitempty"`
	GatewayKey string `json:"gatewayKey,omitempty"`
	Status     int    `json:"status"`
	Error      string `json:"error,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
	DurationMS int64  `json:"durationMs"`
	BytesIn    int64  `json:"bytesIn"`
	BytesOut   int64  `json:"bytesOut"`
	Upstream   string `json:"upstream,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	BodyPreview     string            `json:"bodyPreview,omitempty"`
}

// AttemptFilter selects attempts for the admin API and the node panel.
type AttemptFilter struct {
	From, To            time.Time
	Route, Node, Provider string
	Status              int
	ErrorClass          string
	Limit, Offset       int
}
```

Add `Attempts []Attempt \`json:"attempts,omitempty"\`` to `Request`. In `migrate()` add the `CREATE TABLE IF NOT EXISTS request_attempts (...)` statement exactly as in the spec plus `CREATE INDEX IF NOT EXISTS request_attempts_seq ON request_attempts(seq)` and `CREATE INDEX IF NOT EXISTS request_attempts_node ON request_attempts(node, ts)`. In `Insert`'s transaction, after the parent `INSERT`, write each attempt with `INSERT OR REPLACE INTO request_attempts (request_id, attempt, seq, ts, route, node, provider, gateway_key, status, error, error_class, duration_ms, bytes_in, bytes_out, upstream, response_headers, body_preview) VALUES (...)` reusing the parent's `seq`; default `RequestID`/`TS`/`Node`/`Route`/`Provider`/`GatewayKey` from the parent when empty, and encode `ResponseHeaders` with `json.Marshal`. In `Cleanup`, add `DELETE FROM request_attempts WHERE ts < ?` with the same `requestsBefore` cutoff. Do not touch `rollups` or `key_rollups`. Also amend the `AGENTS.md` stats invariant that forbids a row per attempt: it now reads "one `requests` row per client request; discarded attempts go to `request_attempts` and never to `requests`/`rollups`".

Add the read side in the same task (Task 3 only uses it): `(*Store).AttemptsForRequest(ctx, requestID string) ([]Attempt, error)` ordered by `attempt` ascending, and `(*Store).Attempts(ctx, AttemptFilter) ([]Attempt, error)` newest first with `AttemptFilter.where()` covering `From`/`To`/`Route`/`Node`/`Provider`/`Status`/`ErrorClass`, `limit` defaulted to 100 and capped at 1000, plus `Offset`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -count=1`
Expected: PASS, existing store/graph/recorder tests included.

- [x] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/attempts.go internal/stats/attempts_test.go internal/stats/store.go AGENTS.md
git commit -m "feat(stats): store discarded retry attempts beside their request"
```

- [x] **Step 6: Implementation record** — same rule as Task 1.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `cuttle` / `050bfe0a feat(stats): store discarded retry attempts beside their request` (branch `feat/retry-attempt-observability`, based on `444f6719`)
- Files changed: `internal/stats/attempts.go` (new: `Attempt`, `AttemptFilter`, `attemptColumns`, `insertAttempts`, `AttemptsForRequest`, `Attempts`, `scanAttempts`, `AttemptFilter.where`), `internal/stats/store.go` (`Request.Attempts`, `request_attempts` DDL plus `request_attempts_seq`/`request_attempts_node` indexes in `migrate`, `insertAttempts` call in `Insert`'s transaction, `request_attempts` delete in `Cleanup`, `Cleanup` doc), `internal/stats/attempts_test.go` (new: the brief's two tests verbatim plus `TestAttemptsQueryOrderAndFilters` and `TestOpenAddsAttemptTableToExistingDatabase`), `AGENTS.md` (the one-row-per-request stats invariant now names `request_attempts`)
- Tests: RED `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'Attempts' -count=1` → build failure: `unknown field Attempts in struct literal of type Request`, `undefined: Attempt`, `s.AttemptsForRequest undefined` at 8 sites. GREEN same command → `ok ... 0.803s` (3 tests); `GOWORK=off go test ./internal/stats/ -run 'TestOpenAddsAttemptTable' -count=1` → `ok`; `GOWORK=off go test ./internal/stats/ -count=1` → `ok ... 25.886s`. Full gate `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` → all packages pass. `gofmt -l internal/stats/` → clean.
- Review findings/fixes: the brief's `Consumes: parseNodeName` belongs to Task 3 (`Recorder.Record`); this task stores the already-mapped `Request.Attempts`, so nothing imports `parseNodeName` here — no action needed, recorded so Task 3 is not expected to find a helper. Added the two tests the brief omits: `Attempts` ordering (newest first, tie-broken by request id then attempt descending), `From`/`To`/`Node`/`ErrorClass`+`Status` filters, `Limit`/`Offset` paging and a non-nil empty slice (the `/api/attempts` handler in Task 5 must return `[]`, not `null`); and an upgrade test that drops `request_attempts` from a current database so `Open` has to recreate the table and both indexes, then asserts an inserted attempt inherits the parent's non-zero `seq`. `AttemptsForRequest` returns a non-nil slice for the same reason.
- Deferred/limitations: `AttemptFilter.Status` uses `Status > 0`, mirroring `Filter.where`, so a `status=0` filter (dial-failure attempts) is indistinguishable from "unfiltered"; Task 5 must not promise that filter until `AttemptFilter` grows a presence flag. Attempts use `INSERT OR REPLACE` keyed on `(request_id, attempt)` as specified: a resent payload with the same attempt indexes keeps the newest row, but a resent payload with *fewer* attempts leaves the older extras behind (x records once per client request today, so this is unreachable until a re-delivery path exists). Retention uses the `requestsBefore` cutoff only; there is no backfill for attempts recorded before this change.
- Fix round 1 `4e9f1e38 fix(stats): clamp attempt limit, share the parent timestamp, index attempt ts`: `(*Store).Attempts` now clamps `Limit > 1000` to 1000 instead of resetting it to the 100 default (`Filter.Limit`/`Query` keep the old behaviour, out of scope); `insertAttempts` always stores the parent request's `ts` and ignores any caller-supplied `Attempt.TS` (the type comment says so, since retention and the graph windows filter on it); `migrate()` gains `CREATE INDEX IF NOT EXISTS request_attempts_ts ON request_attempts(ts)` for `Cleanup`'s `DELETE ... WHERE ts < ?` and `Attempts`' `ORDER BY ts DESC` (spec/plan DDL blocks left unchanged as instructed). Tests: `TestAttemptsShareParentTimestamp` (three attempts with `-1h`/`+48h`/zero `Attempt.TS` all read back with the parent's `ts`, and a far-future `Attempt.TS` cannot survive `Cleanup`), `TestAttemptsLimitIsCapped` (1005 attempts, `Limit: 5000` → 1000 rows, `Limit: 1000` → 1000, default → 100), `TestAttemptsQueryOrderAndFilters` reworked to assert the shared `ts`, `TestOpenAddsAttemptTableToExistingDatabase` also checks the new index. RED before the fix: `attempt 0 ts = ... 09:00 ... want the parent's ... 10:00` and `limit=5000 rows = 100, want the 1000-row cap`; GREEN: `GOWORK=off go test ./internal/stats/ -run 'Attempts' -count=1` → `ok ... 1.320s`, `TestOpenAddsAttemptTable` → `ok`, `GOWORK=off go build ./...` + `GOWORK=off go vet ./...` clean, `GOWORK=off go test ./internal/stats/ -count=1` → `ok ... 17.938s`. Report: `.superpowers/sdd/2026-11-11-retry-attempt-observability/task-2-report.md` (fix round 1).

---

### Task 3: cuttle maps the recorder payload into attempts

**Files:**
- Modify: `internal/stats/recorder.go`
- Modify: `internal/stats/attempts.go` (`attemptMessage`)
- Test: `internal/stats/recorder_test.go`, `internal/stats/attempts_test.go`

**Interfaces:**
- Consumes: `recorder.Record`'s `b []byte`, `parseNodeName`, `GatewayKeyIDFromContext`, `ClassifyError`, `captureHeaders`, `Store.Insert` from Task 2.
- Produces: `attemptMessage(body []byte) string` and populated `stats.Attempt` rows on `Request.Attempts`.

- [x] **Step 1: Write the failing test**

Add to `internal/stats/recorder_test.go`:

```go
func TestRecorderRecordsDiscardedAttempts(t *testing.T) {
	s := newStore(t)
	body, err := json.Marshal(map[string]any{
		"sid":  "rid",
		"node": "r1/p1/n2",
		"time": time.Now().UTC(),
		"http": map[string]any{
			"statusCode": 200,
			"attempts": []map[string]any{{
				"attempt":        0,
				"node":           "r1/p1/n1",
				"statusCode":     429,
				"responseHeader": map[string][]string{"Retry-After": {"79477"}},
				"responseBody":   []byte(`{"error":{"type":"rate_limit_error","message":"Rate limit exceeded."}}`),
				"durationMs":     9000,
				"bytesIn":        1000,
				"bytesOut":       60,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, NewFacts(), nil, nil).Record(context.Background(), body); err != nil {
		t.Fatal(err)
	}

	got, err := s.AttemptsForRequest(context.Background(), "rid")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("attempts = %+v, want 1", got)
	}
	a := got[0]
	if a.Node != "n1" || a.Provider != "p1" || a.Route != "r1" || a.Status != 429 {
		t.Fatalf("attempt = %+v", a)
	}
	if a.Error != "Rate limit exceeded." || a.ErrorClass != "upstream_quota" {
		t.Fatalf("attempt message/class = %q/%q", a.Error, a.ErrorClass)
	}
	if a.ResponseHeaders["retry-after"] != "79477" {
		t.Fatalf("attempt headers = %+v", a.ResponseHeaders)
	}
}

func TestRecorderIgnoresPayloadWithoutAttempts(t *testing.T) {
	s := newStore(t)
	body := []byte(`{"sid":"rid2","node":"r1/p1/n1","http":{"statusCode":429}}`)
	if err := NewRecorder(s, NewFacts(), nil, nil).Record(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	got, err := s.AttemptsForRequest(context.Background(), "rid2")
	if err != nil || len(got) != 0 {
		t.Fatalf("attempts = %+v err = %v, want none", got, err)
	}
	row, err := s.Get(context.Background(), "rid2")
	if err != nil || row == nil || row.Status != 429 {
		t.Fatalf("parent row = %+v err = %v", row, err)
	}
}
```

And unit tests for the message extractor:

```go
func TestAttemptMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":{"type":"rate_limit_error","message":"Rate limit exceeded."}}`: "Rate limit exceeded.",
		`{"error":{"message":"boom"}}`:  "boom",
		`{"message":"plain"}`:           "plain",
		`not json at all`:               "not json at all",
	}
	for body, want := range cases {
		if got := attemptMessage([]byte(body)); got != want {
			t.Fatalf("attemptMessage(%q) = %q, want %q", body, got, want)
		}
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'AttemptMessage|RecordsDiscardedAttempts' -count=1`
Expected: FAIL — `attemptMessage` undefined, no attempt rows.

- [x] **Step 3: Implement decoding and the queries**

In `recorder.go`, next to the `xrecorder.HandlerRecorderObject` unmarshal, decode the same bytes again into a local struct so cuttle never names the new x type:

```go
type recordedAttempt struct {
	Attempt        int                 `json:"attempt"`
	Node           string              `json:"node"`
	StatusCode     int                 `json:"statusCode"`
	Error          string              `json:"error,omitempty"`
	DurationMS     int64               `json:"durationMs"`
	BytesIn        int64               `json:"bytesIn"`
	BytesOut       int64               `json:"bytesOut"`
	ResponseHeader map[string][]string `json:"responseHeader,omitempty"`
	ResponseBody   []byte              `json:"responseBody,omitempty"`
}

type recordedHTTP struct {
	Attempts []recordedAttempt `json:"attempts"`
}

type recordedAttemptHost struct {
	HTTP *recordedHTTP `json:"http"`
}
```

Ignore the unmarshal error (an old x simply has no `http.attempts`). For each attempt: `route, provider, node := parseNodeName(a.Node)`, fall back to the parent's values when empty, set `TS: ro.Time`, `GatewayKey: req.GatewayKey`, `Status: a.StatusCode`, `Error: firstNonEmpty(a.Error, attemptMessage(a.ResponseBody))`, `ErrorClass: ClassifyError(a.StatusCode, errText, "")`, `ResponseHeaders: captureHeaders(http.Header(a.ResponseHeader))`, `BodyPreview: string(a.ResponseBody)`.

In `attempts.go` add `attemptMessage` (JSON `error.message`, then `error.type`, then `message`, else the preview's first line trimmed to 200 chars). The query methods and `AttemptFilter.where()` already exist from Task 2 — do not duplicate them.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/recorder.go internal/stats/attempts.go internal/stats/recorder_test.go internal/stats/attempts_test.go
git commit -m "feat(stats): decode discarded attempts from the recorder payload"
```

- [x] **Step 6: Implementation record** — same rule.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `cuttle` / `f8933729 feat(stats): decode discarded attempts from the recorder payload` (branch `feat/retry-attempt-observability`, based on `5c8909ff`)
- Files changed: `internal/stats/recorder.go` (local `recordedAttempt`/`recordedHTTP`/`recordedAttemptHost` decode structs, `decodeAttempts`, `req.Attempts = decodeAttempts(req, b)` after the parent row is fully computed, `net/http` import for `http.Header`), `internal/stats/attempts.go` (`attemptMessage`, `firstNonEmpty`), `internal/stats/recorder_test.go` (the brief's two tests verbatim plus `TestRecorderAttemptFallsBackToParent`), `internal/stats/attempts_test.go` (the brief's `TestAttemptMessage` verbatim plus `TestAttemptMessageFallbacks`, `strings` import)
- Tests: RED `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'AttemptMessage|RecordsDiscardedAttempts|RecorderAttemptFallsBack' -count=1` → build failure `undefined: attemptMessage` at 5 sites; after `attemptMessage` landed (message tests green) the same run failed on the mapping: `attempts = [], want 1` and `attempts = [] err = <nil>, want 1`. GREEN `GOWORK=off go test ./internal/stats/ -run 'AttemptMessage|RecordsDiscardedAttempts|RecorderAttemptFallsBack|IgnoresPayloadWithoutAttempts' -count=1 -v` → all 5 tests PASS, `ok ... 1.444s`; `GOWORK=off go test ./internal/stats/ -count=1` → `ok ... 19.598s`; full gate `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` → every package passes. `gofmt -l internal/stats/` → clean, `-v` output has no stray lines.
- Review findings/fixes: the second decode is gated on `json.Unmarshal` succeeding instead of ignoring the error outright — a payload whose `responseBody` is not base64 fails the decode after partially filling the slice, which would store empty attempt rows; an old x (no `http.attempts`) still yields `len == 0` and inserts nothing. `attemptMessage` cuts the fallback with `[]rune` so a multi-byte body is not sliced mid-rune (the budget is 200 characters, not bytes). Verified the local JSON tags against `go-gost/x` `recorder/recorder.go` (Task 1's `HTTPAttemptRecorderObject` uses the same `attempt`/`node`/`statusCode`/`error`/`durationMs`/`bytesIn`/`bytesOut`/`responseHeader`/`responseBody` names, and the pinned `v0.31.1` has none of them, so cuttle names no new x type). The extra test covers the paths the brief leaves untested: parent route/provider/node fallback for an unnamed attempt, gateway key from the context, the attempt-level `error` string beating the empty body, `status 0` → `network`, and the parent `ts` read back — the first draft asserted the untruncated `time.Now()` and failed on `UnixMilli` rounding, so the test now truncates to the millisecond (a test bug, not a mapping bug).
- Deferred/limitations: `{"error":"boom"}` (a string error rather than an object) does not parse as `error.message`, so its message falls back to the raw preview line — exactly what the brief specifies; no `firstNonEmpty` for it. `Attempt.Upstream` stays empty: the x attempt object carries no upstream, and inheriting the parent's final upstream would attribute a discarded attempt to the endpoint that eventually served the request. Attempts bypass `capture.maxBytes`; the only bound is x's 4 KiB `attemptBodyLimit`, and the full preview is stored. The recorder payload is now unmarshalled twice per row (x recorder object + local struct), accepted per the brief to keep cuttle off the new x type. `AttemptFilter.Status` still cannot express status 0 (already ruled on), and attempts are not attributed to entrypoints.

---

### Task 4: monitoring counters (graph snapshot, live tail, health tooltip data)

**Files:**
- Modify: `internal/stats/graph.go` (`NodeMetrics`, `EdgeMetrics`, `comboStats`, `Aggregator`, `Metrics`)
- Modify: `internal/stats/store.go` (`GraphAggregate`, new `ForEachAttemptAfter`)
- Modify: `internal/admin/handlers_events.go` (tail both tables)
- Modify: `AGENTS.md` (live-monitor invariant)
- Test: `internal/stats/graph_test.go`, `internal/admin/events_test.go`

**Interfaces:**
- Consumes: `comboKey`, `Aggregator.Add`, `RowFilter`, `GraphAggregate`, `ForEachRequestAfter`.
- Produces: `GraphAttemptRow{RequestID string; Attempt int; TS int64; Route, Node, Provider string; Status int}` (`Attempt` is required for the `(request_id, attempt)` dedupe), `(*Aggregator).AddAttempt(GraphAttemptRow)`, `(*Store).ForEachAttemptAfter(ctx, afterSeq int64, fn func(GraphAttemptRow)) (int64, error)`, `NodeMetrics.Attempts/RetriedRequests/AttemptStatus/FailureStatus`, `EdgeMetrics.Attempts/RetriedRequests`.

- [x] **Step 1: Write the failing test**

Add to `internal/stats/graph_test.go`:

```go
func TestGraphCountsAttemptsAndFailures(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	// parent request that failed once and was retried into a 502
	if err := s.Insert(context.Background(), Request{
		ID: "r1", SID: "r1", TS: ts, Route: "r", Node: "n2", Provider: "p", Status: 502,
		Attempts: []Attempt{
			{RequestID: "r1", Attempt: 0, TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 429},
			{RequestID: "r1", Attempt: 1, TS: ts, Route: "r", Node: "n2", Provider: "p", Status: 503},
		},
	}); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	n1 := g.Nodes["node:n1"]
	if n1.Attempts != 1 || n1.RetriedRequests != 1 || n1.AttemptStatus["429"] != 1 {
		t.Fatalf("node:n1 = %+v", n1)
	}
	n2 := g.Nodes["node:n2"]
	if n2.Attempts != 1 || n2.FailureStatus["502"] != 1 || n2.AttemptStatus["503"] != 1 {
		t.Fatalf("node:n2 = %+v", n2)
	}
	edge := g.Edges["route:r|node:n1"]
	if edge.Attempts != 1 || edge.RetriedRequests != 1 {
		t.Fatalf("edge = %+v", edge)
	}
	if got := g.Nodes["provider:p"]; got.Attempts != 2 || got.RetriedRequests != 1 {
		t.Fatalf("provider:p = %+v", got)
	}
}
```

Add to `internal/stats/graph_test.go` a dedupe test for the live path (`INSERT OR REPLACE` reinserts the parent with a new seq, so the tail can hand the same attempt over twice):

```go
func TestAggregatorDedupesAttempts(t *testing.T) {
	a := NewAggregator()
	row := GraphAttemptRow{RequestID: "r1", TS: 1, Route: "r", Node: "n", Provider: "p", Status: 429}
	a.AddAttempt(row)
	a.AddAttempt(row)
	g := a.Metrics(nil, nil, GraphMetrics{}, true)
	if n := g.Nodes["node:n"].Attempts; n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	if n := g.Nodes["node:n"].RetriedRequests; n != 1 {
		t.Fatalf("retried requests = %d, want 1", n)
	}
}
```

Add to `internal/admin/events_test.go` a test that the live tail picks up an attempt inserted after the snapshot (reuse the existing event-stream test helper; assert the next snapshot carries `attempts` for the node).

Add to `internal/stats/graph_test.go` a filter test — attempts must obey the topology `RowFilter` like request rows:

```go
func TestGraphDropsAttemptsOutsideTopology(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	if err := s.Insert(context.Background(), Request{
		ID: "r1", SID: "r1", TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 200,
		Attempts: []Attempt{{RequestID: "r1", Attempt: 0, TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 429}},
	}); err != nil {
		t.Fatal(err)
	}
	hide := func(route, node, provider string) bool { return node != "n1" }
	g, err := s.GraphWithFilter(context.Background(), ts.Add(-time.Hour), time.Now(), nil, hide)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["node:n1"].Attempts; got != 0 {
		t.Fatalf("node:n1 attempts = %d, want 0", got)
	}
	if _, ok := g.Edges["route:r|node:n1"]; ok {
		t.Fatal("edge for a filtered-out node must not exist")
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ ./internal/admin/ -run 'GraphCountsAttempts|Events' -count=1`
Expected: FAIL — `NodeMetrics.Attempts` undefined and the tail ignores attempts.

- [x] **Step 3: Implement the counters**

In `graph.go`: add `failures map[int]int64` to `comboStats` (fill it in `Add` when `failed`); add

```go
type attemptStats struct {
	attempts int64
	retried  map[string]struct{}
	status   map[int]int64
}
```

with `attempts map[comboKey]*attemptStats` on `Aggregator`, initialised lazily in `AddAttempt` (dedupe repeated `(request_id, attempt)` deliveries by keying `retried` on the request id and ignoring a second add for the same attempt index — track seen `request_id`+attempt pairs in a `map[string]struct{}`).

In `Metrics`, after the combo loop, walk `a.attempts` with the same `include` filter and add `Attempts`/`RetriedRequests`/`AttemptStatus` to `provider:<id>`, `node:<id>`, `route:<id>`, plus `Edges["route:…|node:…"]`, `Edges["node:…|provider:…"]` (and `unrouted|provider:…` when route is empty). Entrypoints get nothing. Convert `comboStats.failures` into `NodeMetrics.FailureStatus` (string keys).

In `store.go`: `GraphAggregate` after loading request rows also runs `SELECT request_id, ts, route, node, provider, status FROM request_attempts WHERE seq <= ? AND seq > 0 AND ts >= ? AND ts < ?` and calls `agg.AddAttempt`. Add `ForEachAttemptAfter` mirroring `ForEachRequestAfter` (`seq > ? ORDER BY seq`) returning the highest seq seen.

In `handlers_events.go`, after the `ForEachRequestAfter` call, run `ForEachAttemptAfter(ctx, lastRowID, …)` and set `lastRowID = max(lastRowID, attemptsLast)`. Attempt rows with `TS >= sinceMs` only, same comment as requests. Extend the `AGENTS.md` live-monitor invariant: attempts tail by the parent's `seq` and are deduplicated by `(request_id, attempt)`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ ./internal/admin/ -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/graph.go internal/stats/store.go internal/stats/graph_test.go internal/admin/handlers_events.go internal/admin/events_test.go AGENTS.md
git commit -m "feat(stats): count discarded attempts per node and edge"
```

- [x] **Step 6: Implementation record** — same rule.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `cuttle` / `cc6f0bf2 feat(stats): count discarded attempts per node and edge` (branch `feat/retry-attempt-observability`, based on `8fe4d312`)
- Files changed: `internal/stats/graph.go` (`NodeMetrics.Attempts`/`RetriedRequests`/`AttemptStatus`/`FailureStatus`, `EdgeMetrics.Attempts`/`RetriedRequests`, `GraphAttemptRow`, `comboStats.failures`, `attemptStats`, bounded `seenSet` shared by both dedupe sets, `AddAttempt`, `mergeStatus`, attempt nodes/edges plus the per-key parent union in `Metrics`, the attempt read in `GraphAggregate`, `ForEachAttemptAfter`), `internal/admin/handlers_events.go` (attempt tail with its own cursor), `internal/stats/graph_test.go` (the brief's three tests verbatim plus `TestGraphCountsFinalFailuresByStatus` and `TestGraphCountsUnroutedAttempts`), `internal/stats/graph_live_test.go` (`TestForEachAttemptAfterFollowsTheParentSequence`), `internal/admin/events_test.go` (`TestEventsTailsAttempts`, `waitForNodeAttempts`), `AGENTS.md` (live-monitor invariant extended; new stats bullet for the attempt counters). `internal/stats/store.go` was not modified — see the deviations below.
- Tests: RED `cd cuttle && GOWORK=off go test ./internal/stats/ ./internal/admin/ -run 'GraphCountsAttempts|Events' -count=1` → build failure in both packages: `got.Attempts undefined (type NodeMetrics has no field or method Attempts)` (also `AttemptStatus`/`FailureStatus`/`RetriedRequests`), `undefined: GraphAttemptRow`, `s.ForEachAttemptAfter undefined`, `agg.AddAttempt undefined`, `EdgeMetrics … Attempts`. GREEN `GOWORK=off go test ./internal/stats/ ./internal/admin/ -run 'TestGraphCountsAttempts|TestAggregatorDedupesAttempts|TestGraphDropsAttempts|TestGraphCountsFinalFailures|TestForEachAttemptAfter|TestEventsTailsAttempts' -count=1 -v` → all 6 PASS (`ok ... 1.091s`, `ok ... 0.136s`, no stray output); `TestGraphCountsUnroutedAttempts` → PASS. Packages `GOWORK=off go test ./internal/stats/ ./internal/admin/ -count=1` → `ok ... 26.578s` / `ok ... 19.978s`. Gates `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` → every package passes; `gofmt -l internal/` → clean.
- Review findings/fixes: (1) `retriedRequests` cannot be a sum over combos: the first implementation added `len(s.retried)` per combo, and `TestGraphCountsAttemptsAndFailures` caught `provider:p` reporting `RetriedRequests: 2` for one parent attempted on two nodes. `Metrics` now keeps `nodeParents`/`edgeParents` sets and derives the distinct count per graph key after the loops (the same parent can also reach one `route:<id>` through two nodes or one edge through two recorded providers). (2) Deviation — `GraphAttemptRow` carries `Attempt int` in addition to the brief's fields. The brief requires dedupe by `(request_id, attempt)` but its struct has no attempt index, so two discarded attempts from the same node (a single-node route retried twice) would collapse into one; both reads select the existing `attempt` column. (3) Deviation — `GraphAttemptRow`, `AddAttempt`'s reads, `GraphAggregate`'s attempt query and `ForEachAttemptAfter` live in `internal/stats/graph.go`, where `GraphAggregate` and `ForEachRequestAfter` already are, so `store.go` is untouched (the brief's file list mismatches the code). (4) Deviation — the events loop keeps two cursors (`lastRequestSeq`, `lastAttemptSeq`) instead of `lastRowID = max(lastRowID, attemptLast)`. Tailing attempts from the already-advanced `lastRowID` skips the attempts of a request tailed in the same tick, because the attempt reuses the parent's `seq`; and a single shared cursor advanced by one table can skip a row of the other table committed between the two reads. Each tail advances its own monotonic cursor and dedupes independently, and `AGENTS.md` records why. (5) `failureStatus` is merged inside `addNode`, so entrypoint nodes get it too (it comes from the parent row's client model); attempts skip entrypoints, since an attempt has no client model. Both facts are stated on `NodeMetrics` and in the new `AGENTS.md` bullet. (6) `Aggregator.seen` was factored into a bounded `seenSet` so attempts reuse the request eviction policy (`maxSeenAttempts = 200_000`) instead of a second hand-rolled ring. (7) Extra tests beyond the brief: `TestGraphCountsFinalFailuresByStatus` (failed dial keyed `"0"`, `429`, a `200` never counted, no attempt counters without attempt rows), `TestGraphCountsUnroutedAttempts` (`unrouted` node and `unrouted|provider:` edge, the only branch the brief's tests miss) and `TestForEachAttemptAfterFollowsTheParentSequence` (tail order and fields including the attempt index, out-of-window rows still advance the cursor, and `INSERT OR REPLACE`-driven re-delivery is not double counted through the real store).
- Deferred/limitations: `GraphMetrics.Errors` still lists parent rows only (unchanged on purpose — folding attempts in would break its request denominators); nothing exposes the new counters through the API or console yet (Tasks 5-6), so `web/` is untouched. Memory stays bounded but is per connection: `retried` holds one request id per combo and `seenAttempts` caps at 200k keys, so a live session long enough to evict can re-count an evicted attempt — the same tradeoff the request dedupe already accepts. `AddAttempt` skips dedupe for an empty `request_id` (it is not added to `retried` either). Attempt counters are additive only: pre-upgrade attempts stay invisible.
- Fix round 2 (2026-11-11 / complete, final whole-branch review): repo/commit `cuttle` / `bb01c42a fix(stats): report the newest failure and attempt per graph node` on the same branch (base `f1200c49`). The review found the dead-badge tooltip unable to name the spec's "most recent failure": the graph carried counts but no timestamps. Files changed: `internal/stats/graph.go` (`comboStats.lastFailureTS`/`lastFailureStatus` filled in `Add`; `attemptStats.lastTS`/`lastStatus`/`lastAttempt` filled in `AddAttempt`, the attempt index breaking the tie because one parent's attempts share its timestamp; `NodeMetrics.LastFailureTS int64 json:"lastFailureTs,omitempty"`, `LastFailureStatus int json:"lastFailureStatus"`, `LastAttemptTS int64 json:"lastAttemptTs,omitempty"`, `LastAttemptStatus int json:"lastAttemptStatus"`; `addNode`/`addAttemptNode` merge newest-wins with `>` so a graph key unioning several combos reports the newest of them), `internal/stats/graph_test.go` (`TestGraphReportsNewestFailureAndAttempt`, `TestAggregatorNewestAttemptBreaksTiesByIndex`, `TestGraphPayloadCarriesLastFailureFields`). Tests: RED `GOWORK=off go test ./internal/stats/ -run 'NewestFailure|PayloadCarriesLastFailure' -count=1` → `n1.LastFailureTS undefined (type NodeMetrics has no field or method LastFailureTS)` at 11 sites; GREEN `-run 'NewestFailure|NewestAttempt|PayloadCarriesLastFailure'` → 3 PASS; `GOWORK=off go test ./internal/stats/ -count=1` → `ok ... 23.818s`; gates `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` → every package `ok` (admin 18.8s, gateway 18.7s, stats 31.5s). The two status fields carry no `omitempty` (they are always sent, `0` meaning none) while the timestamps do, so the console keys "has a timestamp" off the ts field only. Deferred: the live and snapshot paths both derive the fields from the aggregate, so nothing new is carried between ticks.

---

### Task 5: admin API for attempts and request detail

**Files:**
- Create: `internal/admin/handlers_attempts.go`
- Modify: `internal/admin/server.go` (route registration), `internal/admin/labels.go` (`attemptView` + labels), `internal/admin/handlers_stats.go` (`handleRequest` includes attempts)
- Test: `internal/admin/attempts_test.go`

**Interfaces:**
- Consumes: `stats.Store.Attempts`, `stats.Store.AttemptsForRequest`, `stats.AttemptFilter`, `labelSet`.
- Produces: `GET /api/attempts` returning `[]attemptView` (`Attempt` + `nodeName`/`routeName`/`providerName` labels), and `requestView.Attempts []attemptView` on `GET /api/requests/{id}`.

- [x] **Step 1: Write the failing test**

Create `internal/admin/attempts_test.go` covering: filters (`node`, `status`, `errorClass`, `from`/`to`, `limit`), label decoration, empty list `[]` (never `null`), and that `GET /api/requests/{id}` returns its attempts while a request without attempts omits the key.

- [x] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/admin/ -run 'Attempts' -count=1`
Expected: FAIL — 404 / route not registered.

- [x] **Step 3: Implement the endpoint**

`handlers_attempts.go`: `func (s *Server) handleAttempts(w http.ResponseWriter, r *http.Request)` parsing `from`/`to` with `parseRange`, plus `node`, `route`, `provider`, `status`, `errorClass`, `limit`, `offset`; return `[]attemptView` (empty slice, not nil). Register `api.HandleFunc("GET /api/attempts", s.handleAttempts)` in `server.go` next to `GET /api/requests`. Extend `requestView` with an outer `Attempts []attemptView \`json:"attempts,omitempty"\`` field — the shallower field shadows the embedded `stats.Request.Attempts` — populated in `handleRequest` via `AttemptsForRequest` (labels applied with `newLabelSet`). `handleRequests` leaves it unset, so list payloads carry no `attempts` key and never `null`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/admin/ -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd cuttle && git add internal/admin/handlers_attempts.go internal/admin/attempts_test.go internal/admin/server.go internal/admin/labels.go internal/admin/handlers_stats.go
git commit -m "feat(admin): expose discarded attempts and attach them to request detail"
```

- [x] **Step 6: Implementation record** — same rule.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `cuttle` / `bca4169a feat(admin): expose discarded attempts and attach them to request detail` (branch `feat/retry-attempt-observability`, based on `6a771080`)
- Files changed: `internal/admin/handlers_attempts.go` (new: `handleAttempts` parsing `from`/`to` through `parseRange` plus `route`/`node`/`provider`/`status`/`errorClass`/`limit`/`offset`, `[]`, handoff to `labelSet.attemptView`), `internal/admin/server.go` (`GET /api/attempts` registered beside `GET /api/requests`), `internal/admin/labels.go` (`attemptView` = `stats.Attempt` + `providerName`/`nodeName`/`routeName`, `labelSet.attemptView`, and `requestView.Attempts []attemptView` shadowing the embedded write-side `stats.Request.Attempts`), `internal/admin/handlers_stats.go` (`handleRequest` loads `AttemptsForRequest` and decorates each row), `internal/admin/attempts_test.go` (new: `TestAttemptsListFiltersAndLabels`, `TestRequestDetailCarriesAttempts`, `seedAttemptLabels`).
- Tests: RED `cd cuttle && GOWORK=off go test ./internal/admin/ -run 'Attempts' -count=1` → `--- FAIL: TestAttemptsListFiltersAndLabels … attempts_test.go:99: GET http://127.0.0.1:35983/api/attempts = 404: 404 page not found`, `--- FAIL: TestRequestDetailCarriesAttempts … attempts_test.go:182: request detail has no attempts key`, `FAIL github.com/ginuerzh/cuttle/internal/admin 0.338s`. GREEN same command → `ok github.com/ginuerzh/cuttle/internal/admin 0.337s`; `GOWORK=off go test ./internal/admin/ -run 'Attempts|RequestDetail|StatsAPI|Labels' -v -count=1` → 6 tests PASS (`TestAttemptsListFiltersAndLabels`, `TestRequestDetailCarriesAttempts`, `TestEventsTailsAttempts`, `TestLabelSetPrefersConfigThenStoredLabels`, `TestRequestViewsCarryLabels`, `TestStatsAPI`), no stray output. `GOWORK=off go test ./internal/admin/ -count=1` → `ok … 15.986s`. Gates `GOWORK=off go build ./...` → OK, `GOWORK=off go vet ./...` → OK, `GOWORK=off go test ./... -count=1` → every package `ok`; `gofmt -l internal/admin/` → clean.
- Review findings/fixes: (1) The test type declares explicit wire fields instead of embedding `stats.Attempt`: an embedded field is named `Attempt`, which collides with the promoted `Attempt int`, so `row.Attempt` resolved to the struct and the package did not build. Explicit fields also make the test assert the JSON shape the console consumes. (2) The `status=0` case is asserted explicitly — the filter applies only to a positive value, so `0` lists every attempt instead of selecting dial failures — and the handler comment states the same rule, keeping the API from advertising a `0` filter. (3) A weak assertion (`BodyPreview == ""`) was replaced by the recorded `Error` message passing through, which is the field the console shows. (4) `nonNilSlice` is kept around an already non-nil `make(...)`, matching `handleRequests` and preserving `[]` if the loop changes. (5) `handleRequest` only appends, so a request without attempts keeps `Attempts` nil and `omitempty` drops the key; the test covers both the detail omission and the list's key-free rows.
- Deferred/limitations: attempts carry the `gatewayKey` id but no `gatewayKeyName` label — the brief fixes the labels to `nodeName`/`routeName`/`providerName`, so a console that wants the key label must decode it itself or a follow-up must add the field. No `entrypoint` filter: an attempt has no client model, the same reason Task 4 gives for excluding entrypoints from the attempt counters. No total count or cursor paging beyond the pass-through `offset`, and no test for the `stats == nil` 503, which no other stats handler asserts either. `web/` is untouched (Task 6).

---

### Task 6: console — attempt cards, dead tooltip

**Files:**
- Modify: `web/src/api/types.ts` (`AttemptRow`, `NodeMetrics`/`EdgeMetrics` fields, `RequestRow.attempts`), `web/src/api/client.ts` (`getAttempts`), `web/src/App.tsx` (`NodePanel`, `AttemptCard`, `deadTitle`), `web/src/locales/en-US.ts`
- Modify: `README.md` (admin endpoints list: `GET /api/attempts`, detail `attempts`) and `AGENTS.md` (admin-API invariant for the new endpoint) — the repo rule keeps docs in sync with behavior, and this is the last unblocked code task.
- Test: `web/src/api/client.test.ts`, `web/src/App.test.tsx`

**Interfaces:**
- Consumes: `GET /api/attempts`, the graph `NodeMetrics` fields from Task 4, `SessionCard` layout conventions.
- Produces: `getAttempts(q: AttemptQuery): Promise<AttemptRow[]>`; `AttemptCard` rendered in the node panel; dead badge `title` text.

- [x] **Step 1: Write the failing tests**

`web/src/api/client.test.ts`: `getAttempts` builds `/api/attempts?from=…&to=…&limit=…&node=…&status=429` (only present params).

`web/src/App.test.tsx`: render `NodePanel` for `node:n1` with `metrics={{attempts: 2, retriedRequests: 1, attemptStatus: {'429': 2}, failureStatus: {'429': 1}, ...}}` and a stubbed `getAttempts` returning one 429 attempt; assert the SSR HTML contains `重试尝试`, `429`, the upstream message, and that the dead badge `title` reads `失败 1（429×1）· 重试丢弃 2`.

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/api/client.test.ts src/App.test.tsx`
Expected: FAIL — `getAttempts` undefined, no `重试尝试` text.

- [x] **Step 3: Implement the console changes**

Add `AttemptRow` (`requestId`, `attempt`, `ts`, `route`, `node`, `provider`, `status`, `error`, `errorClass`, `durationMs`, `bytesIn`, `bytesOut`, `bodyPreview`, plus label fields) and extend `NodeMetrics`/`EdgeMetrics` with `attempts`, `retriedRequests`, `attemptStatus?`, `failureStatus?`. `getAttempts` mirrors `getRequests`. In `App.tsx`: a `useNodeAttempts` hook next to `useNodeSessions` (same since/range/errorsOnly inputs), an `AttemptCard` styled like `SessionCard` with a `重试尝试` tag, and merge both lists by timestamp for rendering (attempts click through to `getRequest(row.requestId)`). `deadTitle(metrics)` builds `失败 N（429×2, 503×1）· 重试丢弃 M`, omitting a section when empty and returning `窗口内没有失败记录` when both are empty; use it as the dead icon's `title` while keeping `aria-label="dead"`. Add the Chinese keys to `en-US.ts` (`重试尝试`, `上游尝试`, `窗口内没有失败记录`, the tooltip template).

- [x] **Step 4: Run the web gate**

Run: `cd cuttle/web && npx vitest run && npx tsc --noEmit && npm run build`
Expected: PASS. Rebuild leaves `internal/admin/dist` updated.

- [x] **Step 5: Run the Go gate with the rebuilt console**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
cd cuttle && git add web/src internal/admin
git commit -m "feat(web): show retry attempts and explain a dead node"
```

(Do not commit `internal/admin/dist`, it is git-ignored.)

- [x] **Step 7: Implementation record** — same rule.

**Implementation record**
- Date/status: 2026-11-11 / complete
- Repo/commit: `cuttle` / `adda5b4f feat(web): show retry attempts and explain a dead node` (branch `feat/retry-attempt-observability`, based on `ecb3073a`)
- Files changed: `web/src/api/types.ts` (`AttemptRow`, `RequestRow.attempts`, `NodeMetrics.attempts`/`retriedRequests`/`attemptStatus`/`failureStatus`, `EdgeMetrics.attempts`/`retriedRequests`), `web/src/api/client.ts` (`AttemptQuery`, `getAttempts` mirroring `getRequests`, `status=0` never sent because it means "any status"), `web/src/App.tsx` (`attemptQuery`, `useNodeAttempts`, `NodeActivity`/`mergeNodeActivity`, `formatStatusCounts`, `deadTitle`, `AttemptCard`, the `重试尝试`/`重试请求` description rows, the merged activity list with `getRequest` click-through, the dead badge `title`), `web/src/locales/en-US.ts` (10 new keys), `web/src/App.test.tsx` (NodePanel attempt summary, dead tooltip, `deadTitle`, `AttemptCard`, `mergeNodeActivity`), `web/src/App.dom.test.tsx` (new jsdom test: merged list, attempt click-through, no attempt fetch for an entrypoint panel), `web/src/api/client.test.ts` (URL building, `status=0` omission), `web/src/graph/activity.test.ts` + `web/src/graph/nodes.test.tsx` (fixtures gain `attempts`/`retriedRequests` now that they are required), `README.md` (endpoint list, request-detail `attempts`, attempts endpoint filters, node-panel/tooltip paragraphs), `AGENTS.md` (admin-API bullet for `GET /api/attempts` and the detail `attempts` key, web invariant for the merged panel list and the fixed tooltip format).
- Tests: RED `cd cuttle/web && npx vitest run src/api/client.test.ts src/App.test.tsx` → `Test Files 2 failed (2)`, `Tests 13 failed | 32 passed (45)`: `TypeError: getAttempts is not a function` (twice), `TypeError: deadTitle is not a function` (5×), `TypeError: mergeNodeActivity is not a function`, `Element type is invalid … got: undefined` for `AttemptCard`, plus the two NodePanel assertions (`to contain '重试尝试'`, `to contain 'title="失败 1（429×1）· 重试丢弃 2"'`) and the old `title="dead"` assertion. GREEN `npx vitest run src/api/client.test.ts src/App.test.tsx` → `Test Files 2 passed (2)`, `Tests 45 passed (45)`; `npx vitest run src/App.dom.test.tsx` → `3 passed`; full web gate `npx vitest run` → `Test Files 45 passed (45)`, `Tests 334 passed (334)`, `npx tsc --noEmit` → clean, `npm run build` → `✓ built in 8.73s` (bundle rebuilt into the git-ignored `internal/admin/dist`). Go gate with the rebuilt console: `GOWORK=off go build ./...` → OK, `GOWORK=off go vet ./...` → OK, `GOWORK=off go test ./... -count=1` → every package `ok` (admin 18.3s, gateway 18.2s, stats 28.4s).
- Review findings/fixes: (1) Deviation — the brief's `NodePanel` SSR test cannot observe rows: `renderToStaticMarkup` never runs effects, so the stubbed `getAttempts`/`getRequests` would never resolve and the card text cannot appear. The SSR assertions therefore cover what the first pass renders (the metrics-derived `重试尝试`/`重试请求` rows, the dead-badge `title`), `AttemptCard` and `mergeNodeActivity` are asserted directly, and a new `web/src/App.dom.test.tsx` (the repo's `*.dom.test.tsx` jsdom convention) covers the hook wiring: both lists render, an attempt card opens its parent request through `getRequest`, and an entrypoint panel fetches nothing. (2) The brief's sample metrics are internally inconsistent (`retriedRequests: 1` beside `重试丢弃 2`); "重试丢弃 M" is the discarded-attempt count (`attempts`), which matches the required `失败 1（429×1）· 重试丢弃 2` title, and `retriedRequests` is shown as its own `重试请求` row instead. (3) The tooltip needed three keys instead of one template plus a join: joining with `' · '` produced `） · ` and a leading space the fixed format forbids, and an English join reads better with spaces, so `失败 {failed}（{statuses}）· 重试丢弃 {discarded}`, `失败 {failed}（{statuses}）` and `重试丢弃 {discarded}` are separate keys. (4) An entrypoint panel must not fetch attempts: `GET /api/attempts` has no `entrypoint` filter (an attempt carries no client model), so `attemptQuery` returns `null` for `entrypoint:`/`unrouted` ids instead of fetching the whole window. (5) `status=0` is never sent — the admin API treats `0` as "any status" — and a test pins it. (6) Existing request fixtures did not need `errorsOnly` to hide attempts: a discarded attempt is always a failure, so the 仅错误 toggle filters sessions only and the attempt cards stay. (7) `attempts`/`retriedRequests` are required on `NodeMetrics`/`EdgeMetrics` (the Go fields have no `omitempty`), so four fixture sites gained the fields instead of making the type lie; `attemptStatus`/`failureStatus` stay optional because Go omits them when empty. (8) `nodes.test.tsx`/`activity.test.ts` fixtures were updated mechanically (`attempts: 0, retriedRequests: 0`) — no assertions changed.
- Deferred/limitations: no browser check; UI behavior is verified only by TypeScript, Vitest (SSR + jsdom) and the rebuilt bundle compiling into the Go binary. The request detail drawer does not render the `attempts` array it now receives (Task 6 renders attempts in the node panel only), and the edge panel does not show `EdgeMetrics.attempts`/`retriedRequests` — the types mirror the payload, the panels do not display them. The topology node badge keeps its own `title` (`dead` / `dead in {routes}`); only the panel badge gained the failure explanation. `internal/admin/dist` is git-ignored and not committed.
- Fix round 1 (2026-11-11 / complete): repo/commit `cuttle` / `b002ac8a fix(web): list discarded attempts in the request detail` on the same branch. The review found the spec-mandated detail section missing and a README sentence that still placed the attempt cards "below the sessions" although the panel merges both lists newest first. Files changed: `web/src/components/RequestDetail.tsx` (`RequestDetail` now reads `request.attempts` and renders a `上游尝试` section between the request rows and `捕获详情`: per attempt `#<attempt+1>`, a status tag (`连接失败` for status 0, red/orange/green by status), the upstream message, duration, `route` and `provider/node` labels; nothing when the array is absent or empty — the drawer is shared with the Sessions page, so one change covers both entry points), `web/src/components/RequestDetail.test.tsx` (three SSR tests: section content and placement above `捕获详情`, a status-0 attempt shown as `连接失败` with `unrouted`, and no section for a missing or empty array), `web/src/App.dom.test.tsx` (the attempt-card click-through also asserts the portalled drawer lists the attempt under `上游尝试`), `README.md` (merged newest-first wording plus the detail `上游尝试` mention), `AGENTS.md` (detail-drawer sentence in the web invariant). Tests: RED `git stash push -- web/src/components/RequestDetail.tsx` then `cd cuttle/web && npx vitest run src/components/RequestDetail.test.tsx` → `Tests 2 failed | 4 passed (6)`, `AssertionError: expected '<div class="ant-descriptions ant-desc…' to contain '上游尝试'` and `'#3'`; GREEN same command → `6 passed`, `npx vitest run src/App.dom.test.tsx` → `3 passed`; web gate `npx vitest run` → `Test Files 45 passed (45)`, `Tests 337 passed (337)`, `npx tsc --noEmit` → clean, `npm run build` → `✓ built in 9.09s`; Go gate with the rebuilt bundle: `GOWORK=off go build ./...` and `GOWORK=off go vet ./...` → OK, `GOWORK=off go test ./... -count=1` → every package `ok` (admin 18.3s, gateway 19.9s, stats 30.4s). The earlier deferred item about the detail drawer not rendering `attempts` is closed by this commit; the edge panel and the topology badge remain as recorded above.
- Fix round 2 (2026-11-11 / complete, final whole-branch review): repo/commit `cuttle` / `cd116cbb fix(web): explain a dead node with the newest failure` on the same branch. The review found the tooltip missing the spec's newest failure, the topology badge still saying only `dead`, and the shipped docs promising data the pinned x cannot produce. Files changed: `web/src/graph/deadTitle.ts` (new shared module, no cycles: `deadTitle`, `hasFailureData`, `formatStatusCounts` and the newest-failure pick; the tooltip is now `失败 N（429×2, 503×1）· 重试丢弃 M · 最后 03:00:16 429`, each section omitted when empty, `最后 …` only when a timestamp exists, `窗口内没有失败记录` when both count sets are empty, and `joinSections` normalizing the Chinese `）· ` boundary that a uniform ` · ` join gets wrong), `web/src/graph/deadTitle.test.ts` (six-row table plus the en-US translation and `hasFailureData`), `web/src/graph/nodes.tsx` (the fully-dead title is `deadTitle(metrics)`; a dead node without failure data keeps the plain `dead`; the partial `dead in {routes}` case is unchanged), `web/src/graph/nodes.test.tsx` (two new assertions), `web/src/App.tsx` (imports the shared module), `web/src/time.ts` (`formatClock`, local `HH:mm:ss`, reused by `formatDateTime`), `web/src/api/types.ts` (the four new `NodeMetrics` fields), `web/src/locales/en-US.ts` (`最后 {time} {status}`, dropping the now unused combined key), fixture updates in `web/src/App.test.tsx`, `web/src/App.dom.test.tsx`, `web/src/graph/activity.test.ts` and `web/src/graph/nodes.test.tsx` for the two required status fields, `README.md` and `AGENTS.md` (the x-release caveat: attempts need the `github.com/go-gost/x` release that writes `http.attempts`; with the pinned `v0.31.1` the lists stay empty and the counters read zero, so cuttle behaves exactly as before — plus the extended tooltip format and the topology badge's plain-`dead` fallback). Tests: RED `npx vitest run src/graph/deadTitle.test.ts src/graph/nodes.test.tsx` → `Failed to load url ./deadTitle` and `title="dead"` instead of the breakdown; after the module landed the Chinese join was still `） · ` and the nodes test ran in the browser-default language, both fixed; GREEN full web gate `npx vitest run` → `Test Files 46 passed (46)`, `Tests 342 passed (342)`, `npx tsc --noEmit` → clean, `npm run build` → `✓ built in 9.19s`; Go gate with the rebuilt bundle: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` → every package `ok`. `internal/admin` needed no change: it marshals `stats.GraphMetrics` directly, and the payload test in Task 4's fix round pins the JSON keys. The earlier deferred item about the topology badge is closed by this commit. Deferred: still no browser check (Vitest SSR only), and the `最后` label may name a discarded attempt's status rather than the parent's final one — the newest failure of either kind is what the spec asks for.

---

### Task 7 (blocked on approval): x release and cuttle bump

**Files:**
- Modify: `cuttle/go.mod`, `cuttle/go.sum`

**Interfaces:**
- Consumes: the x tag containing Task 1.
- Produces: cuttle building against the published x that emits `http.attempts`; optional follow-up cleanup removing the local decode struct.

- [x] **Step 1: Confirm approval** to tag and push `go-gost/x`; do not tag without it.
- [x] **Step 2:** `cd x && git tag vX.Y.Z && git push origin vX.Y.Z`, then `cd cuttle && GOWORK=off go get github.com/go-gost/x@vX.Y.Z && GOWORK=off go mod tidy`.
- [x] **Step 3:** `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`.
- [x] **Step 4: Commit** `chore(deps): bump go-gost/x to vX.Y.Z`.
- [x] **Step 5: Implementation record**

**Implementation record (Task 7)**
- Date/status: 2026-11-11 / complete (approved by the user: "x打tag", "上线")
- Repos/commits: `go-gost/x` tag `v0.32.0` (annotated, tag object `65969ba`, commit `7856886` = the merge of `feat/record-retry-attempts`; `master` had moved on to unrelated mux/mTLS work, so the tag pins the feature commit rather than HEAD); `cuttle` `chore(deps): bump go-gost/x to v0.32.0` on `main`.
- Files changed: `cuttle/go.mod`, `cuttle/go.sum` (`go-gost/x v0.31.1` → `v0.32.0`).
- Tests: `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1` → all packages ok with the tagged x; `GOWORK=off go mod tidy` left `core v0.6.1` and `llm-api-converter v0.6.1` unchanged.
- Review findings: none (dependency bump only; the cuttle side had been building against `v0.31.1` with an empty attempt list, so the bump only switches the producer on).
- Deferred: the local decode structs in `internal/stats/recorder.go` could now be replaced by `xrecorder.HTTPAttemptRecorderObject`; kept deliberately (no `replace`, no need to re-pin cuttle when x moves again).

### Task 8 (blocked on approval): production verification

- [x] **Step 1:** build and push the image, `kubectl --context pi -n llm rollout restart deploy/llm-gw`, verify `/api/health`.
- [x] **Step 2:** trigger a `deepseek-flash` request that lands on `oc-go-free`; confirm the node panel lists a `重试尝试` card with the 429 message, the dead tooltip shows counts, and `SELECT count(*) FROM request_attempts` grows.
- [x] **Step 3:** confirm `rollups`, `summary` and the 请求 list are unchanged in shape (one row per client request).
- [x] **Step 4: Implementation record**

**Implementation record (Task 8)**
- Date/status: 2026-11-11 / complete (approved by the user: "上线")
- Repos/commits: `cuttle` `main` pushed `339725c6..d569a8ee` (CI run `43036827715` success), image `ghcr.io/ginuerzh/cuttle:latest` → digest `sha256:86d294cf53518a41fadfdcbb379fc63788900fea702ea0b1aa22df8082d552c2`; deployed pod `llm-gw-b59c9994c-4knwv`; `GET /api/health` → `{"version":"main-d569a8e","commit":"d569a8ee…","builtAt":"2026-11-11T14:16:23Z"}`.
- Verification: the live DB has `request_attempts` with all three indexes and `user_version 2`; after two natural `deepseek-flash` retries (log `retryable status 429 for node r_pwld6zor5u/p_tjk7i2rga4/n_zcyfy5lhpq` at 14:21:00 and 14:22:10) the table holds two rows:
  - `dbq7jj6…`: attempt 0, node `n_zcyfy5lhpq` (oc-go-free), 429, `upstream_quota`, "Rate limit exceeded. Please try again later.", 981 ms, headers `cf-ray` + `retry-after: 34739`, body preview the upstream JSON error; parent row ended 200 on `n_yd3tois3gy` (cc-dsf) with `retries: 1`.
  - `dbq7k4m…`: same shape, 1,009,494 B request, 1,665 ms, `retry-after: 34669`.
  These are exactly the "retried and recovered" requests that previously left no trace, and the `retry-after` values (~9.6 h) confirm the multi-hour quota window behind the dead flapping.
- UI: the console bundle shipped in the image; the attempt cards / tooltip / drawer were verified by Vitest, SSR and jsdom tests only — no browser check was performed (stated per `AGENTS.md`).
- Deferred: none from this task; the earlier deferred minors remain as recorded.

---

## Deferred / limitations (recorded, not implemented here)

- No backfill: retried attempts from before this change stay invisible.
- Attempt previews are capped at 4 KiB; full request/response capture stays final-attempt-only.
- Attempts are not attributed to entrypoints (no client model on an attempt).
- `/api/stats/errors`, the 最近错误 banner and `GraphMetrics.Errors` keep their request-only semantics.
