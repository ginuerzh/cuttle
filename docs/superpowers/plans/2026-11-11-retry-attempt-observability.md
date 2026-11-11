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

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd x && GOWORK=off go test ./internal/util/forwarder/ -run 'RecordsDiscarded|RecordsAttemptWhenBodyIsCut|NoRetryWithoutRetryMetadata' -count=1`
Expected: FAIL — `ro.HTTP.Attempts` is undefined (compile error).

- [ ] **Step 3: Implement the recorder field and the two capture points**

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

1. In `HandleHTTP`, set `rs.attemptStart = time.Now()` immediately before every `h.dial(...)` call inside the `if cc == nil` block.
2. In the dial-failure retry branch, after `rs = newRetryState(req, node)` and before spending budget, call `rs.addAttempt(xrecorder.HTTPAttemptRecorderObject{Attempt: attempt, Node: nodeName(node), StatusCode: 0, Error: err.Error(), DurationMS: time.Since(rs.attemptStart).Milliseconds()})`, then copy `rs.attempts` onto `ro.HTTP.Attempts`.
3. In the retryable-status branch of `httpRoundTrip` (`retryable status %d for node %s`), before `return false, true, nil`, read the preview with `io.ReadAll(io.LimitReader(resp.Body, attemptBodyLimit))` (ignore the error), call `rs.addAttempt(xrecorder.HTTPAttemptRecorderObject{Attempt: attemptFromContext(ctx), Node: node.Name, StatusCode: resp.StatusCode, DurationMS: time.Since(rs.attemptStart).Milliseconds(), BytesIn: req.ContentLength, BytesOut: resp.ContentLength, ResponseHeader: resp.Header.Clone(), ResponseBody: preview})`, and sync `ho.RecorderObject.HTTP.Attempts = rs.attempts`.
4. In the keep-alive re-init at the top of `HandleHTTP` (`ro.HTTP = &xrecorder.HTTPRecorderObject{...}`), re-attach `ro.HTTP.Attempts = rs.attempts` when `rs != nil`, so a retry `continue` cannot drop them.
5. Add `func nodeName(n *chain.Node) string { if n == nil { return "" }; return n.Name }` in `retry.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd x && GOWORK=off go test ./internal/util/forwarder/ -count=1`
Expected: PASS, including the pre-existing retry tests.

- [ ] **Step 5: Commit**

```bash
cd x && git add recorder/recorder.go internal/util/forwarder/retry.go internal/util/forwarder/sniffer_http.go internal/util/forwarder/sniffer_http_retry_test.go
git commit -m "feat(forward): record discarded retry attempts on the recorder object"
```

- [ ] **Step 6: Implementation record**

Append to this plan: date/status, repo `go-gost/x` + commit SHA, files changed, commands run and their results, review findings, deferred items. Commit it with the task.

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

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'Attempts' -count=1`
Expected: FAIL — `Attempt` and `Store.AttemptsForRequest` undefined.

- [ ] **Step 3: Add the table, the type, the write path and the two query methods**

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

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -count=1`
Expected: PASS, existing store/graph/recorder tests included.

- [ ] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/attempts.go internal/stats/attempts_test.go internal/stats/store.go AGENTS.md
git commit -m "feat(stats): store discarded retry attempts beside their request"
```

- [ ] **Step 6: Implementation record** — same rule as Task 1.

---

### Task 3: cuttle maps the recorder payload into attempts

**Files:**
- Modify: `internal/stats/recorder.go`
- Modify: `internal/stats/attempts.go` (`attemptMessage`)
- Test: `internal/stats/recorder_test.go`, `internal/stats/attempts_test.go`

**Interfaces:**
- Consumes: `recorder.Record`'s `b []byte`, `parseNodeName`, `GatewayKeyIDFromContext`, `ClassifyError`, `captureHeaders`, `Store.Insert` from Task 2.
- Produces: `attemptMessage(body []byte) string` and populated `stats.Attempt` rows on `Request.Attempts`.

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -run 'AttemptMessage|RecordsDiscardedAttempts' -count=1`
Expected: FAIL — `attemptMessage` undefined, no attempt rows.

- [ ] **Step 3: Implement decoding and the queries**

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

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/recorder.go internal/stats/attempts.go internal/stats/recorder_test.go internal/stats/attempts_test.go
git commit -m "feat(stats): decode discarded attempts from the recorder payload"
```

- [ ] **Step 6: Implementation record** — same rule.

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
- Produces: `GraphAttemptRow{RequestID string; TS int64; Route, Node, Provider string; Status int}`, `(*Aggregator).AddAttempt(GraphAttemptRow)`, `(*Store).ForEachAttemptAfter(ctx, afterSeq int64, fn func(GraphAttemptRow)) (int64, error)`, `NodeMetrics.Attempts/RetriedRequests/AttemptStatus/FailureStatus`, `EdgeMetrics.Attempts/RetriedRequests`.

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ ./internal/admin/ -run 'GraphCountsAttempts|Events' -count=1`
Expected: FAIL — `NodeMetrics.Attempts` undefined and the tail ignores attempts.

- [ ] **Step 3: Implement the counters**

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

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ ./internal/admin/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd cuttle && git add internal/stats/graph.go internal/stats/store.go internal/stats/graph_test.go internal/admin/handlers_events.go internal/admin/events_test.go AGENTS.md
git commit -m "feat(stats): count discarded attempts per node and edge"
```

- [ ] **Step 6: Implementation record** — same rule.

---

### Task 5: admin API for attempts and request detail

**Files:**
- Create: `internal/admin/handlers_attempts.go`
- Modify: `internal/admin/server.go` (route registration), `internal/admin/labels.go` (`attemptView` + labels), `internal/admin/handlers_stats.go` (`handleRequest` includes attempts)
- Test: `internal/admin/attempts_test.go`

**Interfaces:**
- Consumes: `stats.Store.Attempts`, `stats.Store.AttemptsForRequest`, `stats.AttemptFilter`, `labelSet`.
- Produces: `GET /api/attempts` returning `[]attemptView` (`Attempt` + `nodeName`/`routeName`/`providerName` labels), and `requestView.Attempts []attemptView` on `GET /api/requests/{id}`.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/attempts_test.go` covering: filters (`node`, `status`, `errorClass`, `from`/`to`, `limit`), label decoration, empty list `[]` (never `null`), and that `GET /api/requests/{id}` returns its attempts while a request without attempts omits the key.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/admin/ -run 'Attempts' -count=1`
Expected: FAIL — 404 / route not registered.

- [ ] **Step 3: Implement the endpoint**

`handlers_attempts.go`: `func (s *Server) handleAttempts(w http.ResponseWriter, r *http.Request)` parsing `from`/`to` with `parseRange`, plus `node`, `route`, `provider`, `status`, `errorClass`, `limit`, `offset`; return `[]attemptView` (empty slice, not nil). Register `api.HandleFunc("GET /api/attempts", s.handleAttempts)` in `server.go` next to `GET /api/requests`. Extend `requestView` with an outer `Attempts []attemptView \`json:"attempts,omitempty"\`` field — the shallower field shadows the embedded `stats.Request.Attempts` — populated in `handleRequest` via `AttemptsForRequest` (labels applied with `newLabelSet`). `handleRequests` leaves it unset, so list payloads carry no `attempts` key and never `null`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd cuttle && GOWORK=off go test ./internal/admin/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd cuttle && git add internal/admin/handlers_attempts.go internal/admin/attempts_test.go internal/admin/server.go internal/admin/labels.go internal/admin/handlers_stats.go
git commit -m "feat(admin): expose discarded attempts and attach them to request detail"
```

- [ ] **Step 6: Implementation record** — same rule.

---

### Task 6: console — attempt cards, dead tooltip

**Files:**
- Modify: `web/src/api/types.ts` (`AttemptRow`, `NodeMetrics`/`EdgeMetrics` fields, `RequestRow.attempts`), `web/src/api/client.ts` (`getAttempts`), `web/src/App.tsx` (`NodePanel`, `AttemptCard`, `deadTitle`), `web/src/locales/en-US.ts`
- Test: `web/src/api/client.test.ts`, `web/src/App.test.tsx`

**Interfaces:**
- Consumes: `GET /api/attempts`, the graph `NodeMetrics` fields from Task 4, `SessionCard` layout conventions.
- Produces: `getAttempts(q: AttemptQuery): Promise<AttemptRow[]>`; `AttemptCard` rendered in the node panel; dead badge `title` text.

- [ ] **Step 1: Write the failing tests**

`web/src/api/client.test.ts`: `getAttempts` builds `/api/attempts?from=…&to=…&limit=…&node=…&status=429` (only present params).

`web/src/App.test.tsx`: render `NodePanel` for `node:n1` with `metrics={{attempts: 2, retriedRequests: 1, attemptStatus: {'429': 2}, failureStatus: {'429': 1}, ...}}` and a stubbed `getAttempts` returning one 429 attempt; assert the SSR HTML contains `重试尝试`, `429`, the upstream message, and that the dead badge `title` reads `失败 1（429×1）· 重试丢弃 2`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd cuttle/web && npx vitest run src/api/client.test.ts src/App.test.tsx`
Expected: FAIL — `getAttempts` undefined, no `重试尝试` text.

- [ ] **Step 3: Implement the console changes**

Add `AttemptRow` (`requestId`, `attempt`, `ts`, `route`, `node`, `provider`, `status`, `error`, `errorClass`, `durationMs`, `bytesIn`, `bytesOut`, `bodyPreview`, plus label fields) and extend `NodeMetrics`/`EdgeMetrics` with `attempts`, `retriedRequests`, `attemptStatus?`, `failureStatus?`. `getAttempts` mirrors `getRequests`. In `App.tsx`: a `useNodeAttempts` hook next to `useNodeSessions` (same since/range/errorsOnly inputs), an `AttemptCard` styled like `SessionCard` with a `重试尝试` tag, and merge both lists by timestamp for rendering (attempts click through to `getRequest(row.requestId)`). `deadTitle(metrics)` builds `失败 N（429×2, 503×1）· 重试丢弃 M`, omitting a section when empty and returning `窗口内没有失败记录` when both are empty; use it as the dead icon's `title` while keeping `aria-label="dead"`. Add the Chinese keys to `en-US.ts` (`重试尝试`, `上游尝试`, `窗口内没有失败记录`, the tooltip template).

- [ ] **Step 4: Run the web gate**

Run: `cd cuttle/web && npx vitest run && npx tsc --noEmit && npm run build`
Expected: PASS. Rebuild leaves `internal/admin/dist` updated.

- [ ] **Step 5: Run the Go gate with the rebuilt console**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd cuttle && git add web/src internal/admin
git commit -m "feat(web): show retry attempts and explain a dead node"
```

(Do not commit `internal/admin/dist`, it is git-ignored.)

- [ ] **Step 7: Implementation record** — same rule.

---

### Task 7 (blocked on approval): x release and cuttle bump

**Files:**
- Modify: `cuttle/go.mod`, `cuttle/go.sum`

**Interfaces:**
- Consumes: the x tag containing Task 1.
- Produces: cuttle building against the published x that emits `http.attempts`; optional follow-up cleanup removing the local decode struct.

- [ ] **Step 1: Confirm approval** to tag and push `go-gost/x`; do not tag without it.
- [ ] **Step 2:** `cd x && git tag vX.Y.Z && git push origin vX.Y.Z`, then `cd cuttle && GOWORK=off go get github.com/go-gost/x@vX.Y.Z && GOWORK=off go mod tidy`.
- [ ] **Step 3:** `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`.
- [ ] **Step 4: Commit** `chore(deps): bump go-gost/x to vX.Y.Z`.
- [ ] **Step 5: Implementation record** — same rule.

### Task 8 (blocked on approval): production verification

- [ ] **Step 1:** build and push the image, `kubectl --context pi -n llm rollout restart deploy/llm-gw`, verify `/api/health`.
- [ ] **Step 2:** trigger a `deepseek-flash` request that lands on `oc-go-free`; confirm the node panel lists a `重试尝试` card with the 429 message, the dead tooltip shows counts, and `SELECT count(*) FROM request_attempts` grows.
- [ ] **Step 3:** confirm `rollups`, `summary` and the 请求 list are unchanged in shape (one row per client request).
- [ ] **Step 4: Implementation record** with the verified numbers, or the deferred reason if the deploy is postponed.

---

## Deferred / limitations (recorded, not implemented here)

- No backfill: retried attempts from before this change stay invisible.
- Attempt previews are capped at 4 KiB; full request/response capture stays final-attempt-only.
- Attempts are not attributed to entrypoints (no client model on an attempt).
- `/api/stats/errors`, the 最近错误 banner and `GraphMetrics.Errors` keep their request-only semantics.
