# Route Retries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-route retries for retryable upstream HTTP statuses, re-selecting a node through the existing selector on every attempt, and record a `retries` count on the request row.

**Architecture:** The x HTTP forwarder (which already owns node selection and the HTTP round trip) buffers a replayable copy of the client request when the selected node carries a retry policy, decides on the response status before writing anything to the client, and loops back through the normal hop selection. Cuttle passes the route policy to x as node metadata and records the final attempt once, with its retry count.

**Tech Stack:** Go 1.26 (`github.com/go-gost/x`, `github.com/ginuerzh/cuttle`), SQLite, React + Ant Design console, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-10-route-retry-design.md`

**Prerequisites:**
- Work in a feature branch in each repository (cuttle `main`, x `master`). The x tag must be cut from the commit that contains Tasks 1–2.
- Cuttle commands run with `GOWORK=off`; x commands run in `../x`.
- The x tag push (`v0.31.0`) was approved by the user on 2026-11-10.
- Every task ends with its **Implementation record** filled in and included in that task's commit.

## Global Constraints

- Route fields: `retries` int `0..10`, default `0`; `retryCodes` string, default `429,5xx`, syntax `429` or `5xx`, comma-separated.
- x metadata keys: `retry.max` (int), `retry.codes` (string); absent or `retry.max <= 0` means no retry. Cuttle adds the codes default when `retries > 0` and `retryCodes` is empty.
- Replay buffer limit: 32 MiB (`retryBodyLimit`, a package var for tests). Over the limit, or with `Expect: 100-continue`, the request is forwarded once with no retry.
- Retry only on a complete response status matching `retryCodes`, and on dial/connect errors before the request is written. Never retry after a read error once the request was written.
- Every retry marks the failed node (the normal x marker) and re-runs `hop.Select`.
- One stats row per client request, `retries` = extra attempts performed (`0` = first attempt). Discarded attempts are logged, not stored.
- Body capture stores only the final attempt; two offers at the same `attempt` still append.
- `requests.retries` is additive (`INTEGER NOT NULL DEFAULT 0`); rollups, graphs, filters and captures are otherwise unchanged.
- Cuttle gate: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`.
- Web gate: `npm test`, `npx tsc --noEmit`, `npm run build`. Rebuild the console before the final Go build.

## Review Focus

These are the input classes that will bite a user if the code gets them wrong; each has a test in the owning task.

1. **Oversized request body on a retry-enabled route** — the client body must reach the upstream intact and the request must not be buffered. (Task 2, `TestHandleHTTP_RetryOverCapForwardsOnce`.)
2. **`Expect: 100-continue` request** — the proxy must not block waiting for a body the client is holding back, and must not retry. (Task 2, `TestHandleHTTP_NoRetryForExpectContinue`.)
3. **Every attempt returns a retryable status** — the client must receive the last real upstream response, with the budget respected and one recorded row. (Task 2, `TestHandleHTTP_RetryBudgetIsBounded`.)
4. **A healthy streaming (SSE) response** — must stream through unchanged, with no retry and no buffering. (Task 2, `TestHandleHTTP_StreamingSuccessNotRetried`.)
5. **A route with the default `retries: 0`** — must behave exactly like today: no buffering, one attempt, `retries` absent in the row. (Task 2, `TestHandleHTTP_NoRetryWithoutRetryMetadata`; Task 5 config defaults.)

---

### Task 1: x retry helpers (buffer, replay, codes, state)

**Files:**
- Create: `../x/internal/util/forwarder/retry.go`
- Test: `../x/internal/util/forwarder/retry_test.go`

**Interfaces:**
- Produces:
  - `const retryMaxMetadataKey = "retry.max"`, `retryCodesMetadataKey = "retry.codes"`, `defaultRetryCodes = "429,5xx"`
  - `var retryBodyLimit = 32 << 20`
  - `type retryState struct { budget int; retries int; codes chain.FailCodes; replay []byte; start time.Time }`
  - `func newRetryState(req *http.Request, node *chain.Node) *retryState` — `nil` when `node == nil`, `retry.max <= 0`, the request has `Expect: 100-continue`, or the body cannot be buffered.
  - `func parseRetryCodes(s string) chain.FailCodes` — comma list; `N` (>=100) exact, `Nxx` becomes `N` (<100, hundred wildcard); invalid tokens skipped; empty → `nil`.
  - `func bufferRetryRequest(req *http.Request) ([]byte, bool)` — returns the serialized request and `true`, or `(nil, false)` with `req` unchanged.
  - `func replayRetryRequest(b []byte) (*http.Request, error)`
- Consumes: nothing from earlier tasks.

- [ ] **Step 1: Write the failing tests**

`retry_test.go`:

```go
func TestParseRetryCodes(t *testing.T) {
	cases := []struct {
		in   string
		want chain.FailCodes
	}{
		{"429,5xx", chain.FailCodes{429, 5}},
		{" 502 , 503 ", chain.FailCodes{502, 503}},
		{"", nil},
		{"nope,99,5x", nil},
	}
	for _, c := range cases {
		if got := parseRetryCodes(c.in); !slices.Equal(got, c.want) {
			t.Errorf("parseRetryCodes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNewRetryStateReadsNodeMetadata(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://up.example/v1/messages", strings.NewReader(`{"m":1}`))
	node := chain.NewNode("n", "127.0.0.1:1", chain.MetadataNodeOption(metadata.NewMetadata(map[string]any{
		"retry.max": 2, "retry.codes": "429,502",
	})))
	rs := newRetryState(req, node)
	if rs == nil {
		t.Fatal("nil retry state")
	}
	if rs.budget != 2 || !slices.Equal(rs.codes, chain.FailCodes{429, 502}) {
		t.Fatalf("state = %+v", rs)
	}
	got, err := replayRetryRequest(rs.replay)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != `{"m":1}` {
		t.Fatalf("replayed body = %q", body)
	}
}

func TestNewRetryStateDefaultsAndSkips(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://up.example/", strings.NewReader("x"))

	noMeta := chain.NewNode("n", "127.0.0.1:1")
	if newRetryState(req, noMeta) != nil {
		t.Fatal("retried without metadata")
	}
	zero := chain.NewNode("n", "127.0.0.1:1", chain.MetadataNodeOption(metadata.NewMetadata(map[string]any{"retry.max": 0})))
	if newRetryState(req, zero) != nil {
		t.Fatal("retried with zero budget")
	}
	def := chain.NewNode("n", "127.0.0.1:1", chain.MetadataNodeOption(metadata.NewMetadata(map[string]any{"retry.max": 1})))
	if rs := newRetryState(req, def); rs == nil || !slices.Equal(rs.codes, chain.FailCodes{429, 5}) {
		t.Fatalf("default codes = %+v", rs)
	}
	req.Header.Set("Expect", "100-continue")
	if newRetryState(req, def) != nil {
		t.Fatal("buffered an Expect: 100-continue request")
	}
}

func TestBufferRetryRequestOverCapRestoresBody(t *testing.T) {
	old := retryBodyLimit
	retryBodyLimit = 4
	defer func() { retryBodyLimit = old }()

	req := httptest.NewRequest(http.MethodPost, "http://up.example/", strings.NewReader("12345"))
	req.ContentLength = -1 // force the read path instead of the length early-exit
	if _, ok := bufferRetryRequest(req); ok {
		t.Fatal("buffered a body over the limit")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil || string(body) != "12345" {
		t.Fatalf("body after failed buffer = %q, %v", body, err)
	}
}

func TestBufferRetryRequestReplaysBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://up.example/", strings.NewReader("hello"))
	replay, ok := bufferRetryRequest(req)
	if !ok {
		t.Fatal("did not buffer")
	}
	again, err := replayRetryRequest(replay)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(again.Body)
	if string(body) != "hello" || again.ContentLength != 5 {
		t.Fatalf("replayed body = %q len %d", body, again.ContentLength)
	}
}
```

(`metadata` is `github.com/go-gost/x/metadata`; import aliases follow the package name used in the forwarder tests.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ../x && go test ./internal/util/forwarder -run 'TestParseRetryCodes|TestNewRetryState|TestBufferRetryRequest' -count=1`
Expected: build failure, `undefined: retryBodyLimit`, `undefined: newRetryState`, `undefined: parseRetryCodes`, `undefined: bufferRetryRequest`, `undefined: replayRetryRequest`.

- [ ] **Step 3: Implement `retry.go`**

`bufferRetryRequest` must follow this order exactly:

1. If `strings.Contains(strings.ToLower(req.Header.Get("Expect")), "100-continue")`, return `(nil, false)` unchanged.
2. If `req.ContentLength > int64(retryBodyLimit)`, return `(nil, false)` unchanged.
3. If `req.Body == nil`, serialize the head only.
4. Else read at most `retryBodyLimit+1` bytes; on a read error or overflow, restore
   `req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(read), req.Body))` and return `(nil, false)`.
5. Set `req.Body = io.NopCloser(bytes.NewReader(read))`, `req.ContentLength = int64(len(read))`,
   `req.TransferEncoding = nil`, `req.Header.Del("Transfer-Encoding")`, serialize with `req.Write(&buf)`,
   then set `req.Body` back to a fresh reader over `read` so attempt 1 can use it.

`newRetryState` reads `mdutil.GetInt(node.Options().Metadata, retryMaxMetadataKey)`, chooses
`mdutil.GetString(..., retryCodesMetadataKey)` or `defaultRetryCodes`, buffers, and sets
`start: time.Now()`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd ../x && go test ./internal/util/forwarder -run 'TestParseRetryCodes|TestNewRetryState|TestBufferRetryRequest' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ../x
git add internal/util/forwarder/retry.go internal/util/forwarder/retry_test.go
git commit -m "feat(forward): add HTTP retry helpers"
```

**Implementation record** (fill after the task):
- Date/status: 2026-11-10 / complete
- Repository + commit: `go-gost/x` `bbb071b` (`feat(forward): add HTTP retry helpers`) followed by fix commit `a532bed` (`fix(forward): keep empty retry bodies unchunked and document snapshot ordering`), branch `feat/route-retry`
- Files changed: `internal/util/forwarder/retry.go`, `internal/util/forwarder/retry_test.go`
- Tests/commands and results: focused retry tests RED (undefined helpers) then 7/7 PASS after the fix; `go test ./internal/util/forwarder -count=1` PASS; `go vet ./...`, `go build ./...`, `gofmt -l` clean
- Review findings and fixes: empty non-nil body was normalized to a chunked body on attempt 1 → `http.NoBody` for a zero-length buffer plus framing tests; snapshot-ordering contract was undocumented → doc comment states the snapshot must be the pristine pre-rewrite client request (ruling: replaying rewritten bytes would break cross-node retry)
- Deferred items/limitations: test gaps for nil node / `ContentLength` early-exit / nil body / wildcard match / uppercase `5XX`; out-of-range exact codes accepted but never match; all-invalid `retry.codes` pays the buffering cost with no fallback; `retryState.retries` unused until Task 2; buffering delays attempt 1's upstream body

---

### Task 2: x forwarder retry loop, attempt metadata, recorder field

**Files:**
- Modify: `../x/recorder/recorder.go` (add `Retries` to `HandlerRecorderObject`)
- Modify: `../x/internal/util/forwarder/rewrite_metadata.go` (add `MetaKeyAttempt`)
- Modify: `../x/internal/util/forwarder/retry.go` (attempt context key helpers)
- Modify: `../x/internal/util/forwarder/sniffer_sse.go` (`baseMetadata` includes the attempt)
- Modify: `../x/internal/util/forwarder/sniffer_http.go` (retry loop)
- Test: `../x/internal/util/forwarder/sniffer_http_retry_test.go`

**Interfaces:**
- Consumes: `retryState`, `newRetryState`, `replayRetryRequest`, `retryBodyLimit` (Task 1).
- Produces:
  - `xrecorder.HandlerRecorderObject.Retries int` (`json:"retries,omitempty"`)
  - `forwarder.MetaKeyAttempt = "attempt"` in rewriter metadata, zero-based
  - `func withAttempt(ctx context.Context, n int) context.Context`, `func attemptFromContext(ctx context.Context) int`
  - `(*Sniffer).httpRoundTrip(ctx, rw, cc, node, req, pStats, ho, staleResp, rs *retryState) (shouldClose, retry bool, err error)`

- [ ] **Step 1: Write the failing integration tests**

`sniffer_http_retry_test.go`:

```go
type sequenceHop struct {
	mu    sync.Mutex
	nodes []*chain.Node
	calls int
}

func (h *sequenceHop) Select(context.Context, ...hop.SelectOption) *chain.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.nodes[min(h.calls, len(h.nodes)-1)]
	h.calls++
	return n
}

func retryNode(name, addr string, max int, codes string) *chain.Node {
	md := map[string]any{"retry.max": max}
	if codes != "" {
		md["retry.codes"] = codes
	}
	return chain.NewNode(name, addr,
		chain.HTTPNodeOption(&chain.HTTPNodeSettings{FailCodes: chain.FailCodes{429, 5}}),
		chain.MetadataNodeOption(metadata.NewMetadata(md)),
	)
}

func retryRequest(t *testing.T, h *Sniffer, hh hop.Hop, body string, headers map[string]string, extra ...sniffing.HandleOption) (int, string, xrecorder.HandlerRecorderObject) {
	t.Helper()
	rec := &captureRecorder{}
	h.Recorder = rec
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	opts := []sniffing.HandleOption{
		sniffing.WithService("test-svc"),
		sniffing.WithHop(hh),
		sniffing.WithRecorderObject(&xrecorder.HandlerRecorderObject{}),
		sniffing.WithLog(xlogger.Nop()),
	}
	opts = append(opts, extra...)
	errCh := make(chan error, 1)
	go func() { errCh <- h.HandleHTTP(context.Background(), serverConn, opts...) }()

	req, _ := http.NewRequest(http.MethodPost, "http://upstream.example/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Close = true
	if err := req.Write(clientConn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), req)
	if err != nil {
		t.Fatal(err)
	}
	gotBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	clientConn.Close()
	<-errCh

	if len(rec.data) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(rec.data))
	}
	var ro xrecorder.HandlerRecorderObject
	if err := json.Unmarshal(rec.data[0], &ro); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(gotBody), ro
}
```

Every test that does not care about the body uses `_` for the second return.

Add these tests:

```go
func TestHandleHTTP_RetriesRetryableStatusOnNextNode(t *testing.T)
```
Two `httptest` servers: first returns `429` and records its body/hits, second returns `200` with `ok` and records its body/hits. Nodes `a` and `b` both `retryNode(..., 1, "429,5xx")`, hop `&sequenceHop{nodes: []*chain.Node{a, b}}`, body `{"m":1}`. Assert status `200`, `a` hits 1, `b` hits 1, both received `{"m":1}`, `a.Marker().Count() == 1`, `ro.Retries == 1`, `ro.Node == "b"`, `ro.HTTP.StatusCode == 200`.

```go
func TestHandleHTTP_NoRetryWithoutRetryMetadata(t *testing.T)
```
Nodes built with `chain.NewNode(name, addr)` (no metadata), first 429, second 200. Assert status 429, first hits 1, second hits 0, `ro.Retries == 0`.

```go
func TestHandleHTTP_RetryBudgetIsBounded(t *testing.T)
```
Both upstreams always return 429; both nodes `retryNode(..., 2, "")`; hop `[a, b]`. Assert status 429, total upstream hits 3, `ro.Retries == 2`.

```go
func TestHandleHTTP_RetrySkipsNonRetryableStatus(t *testing.T)
```
First returns 400, second 200; both nodes `retryNode(..., 1, "429,5xx")`. Assert status 400, second hits 0, `ro.Retries == 0`.

```go
func TestHandleHTTP_RetryMarksNodeWithoutFailCodes(t *testing.T)
```
Node has metadata `retry.max: 1` but no `HTTPNodeSettings.FailCodes`; first returns 429, second 200. Assert `a.Marker().Count() == 1` and status 200.

```go
func TestHandleHTTP_DialFailureRetriesNextNode(t *testing.T)
```
Nodes `a` (unreachable `127.0.0.1:1`) and `b` (`httptest` server), both `retryNode(..., 1, "429,5xx")`. Pass `sniffing.WithDial` that returns `io.ErrUnexpectedEOF` for `a.Addr` and `net.Dial` otherwise. Assert status 200, `b` hits 1, `a.Marker().Count() == 1`, `ro.Retries == 1`.

```go
func TestHandleHTTP_ResponseReadErrorDoesNotRetry(t *testing.T)
```
Use a raw `net.Listen("tcp", "127.0.0.1:0")` server that accepts and closes immediately as node `a`; node `b` is a normal 200 upstream. Both `retryNode(..., 1, "5xx")`. Assert status 503, `b` hits 0, `ro.Retries == 0`.

```go
func TestHandleHTTP_NoRetryForExpectContinue(t *testing.T)
```
First 429, second 200; nodes `retryNode(..., 1, "429,5xx")`; pass header `Expect: 100-continue`. Assert status 429, second hits 0.

```go
func TestHandleHTTP_RetryOverCapForwardsOnce(t *testing.T)
```
Set `retryBodyLimit = 4` (restore with `defer`). First upstream 429 and records its body, second 200. Body `12345`. Assert status 429, first received `12345`, second hits 0.

```go
func TestHandleHTTP_StreamingSuccessNotRetried(t *testing.T)
```
First returns `200`, `Content-Type: text/event-stream`, body `data: {}\n\n`; second returns 200 `ok`. Nodes `retryNode(..., 1, "5xx")`. Assert status 200, first hits 1, second hits 0, `ro.Retries == 0`.

```go
func TestRewriteBodyMetadataIncludesAttempt(t *testing.T)
```
Construct `rb := &rewriteBody{ctx: withAttempt(context.Background(), 2), sid: "s", direction: "request", uri: "/u"}` and assert `rb.baseMetadata()[MetaKeyAttempt] == 2`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ../x && go test ./internal/util/forwarder -run 'TestHandleHTTP_(Retries|NoRetry|Retry|DialFailure|ResponseRead|Streaming)|TestRewriteBodyMetadataIncludesAttempt' -count=1`
Expected: build failure on the new `Retries` field and `withAttempt`, and behavioral failures (no retry).

- [ ] **Step 3: Add the recorder field and attempt metadata**

- In `recorder/recorder.go`, add `Retries int \`json:"retries,omitempty"\`` to `HandlerRecorderObject` with a comment that it counts extra attempts for the recorded request.
- In `rewrite_metadata.go`, add `MetaKeyAttempt = "attempt"`.
- In `retry.go`, add the context key helpers (unexported key type; `attemptFromContext` returns `0` when absent).
- In `sniffer_sse.go`, `baseMetadata` passes `map[string]any{MetaKeyAttempt: attemptFromContext(b.ctx)}` as the extras.

- [ ] **Step 4: Implement the retry loop in `sniffer_http.go`**

In `HandleHTTP`:
- Track `attempt := 0` and `rs *retryState` per client request; after `http.ReadRequest` reads the next keep-alive request, reset both **and** `ho.RecorderObject.Retries = 0` so the next request's row starts at zero.
- Guard the cache lookup with `(rs == nil || rs.retries == 0)`.
- In the dial-error branch, first try stale serving (unchanged), then: create `rs` with `newRetryState(req, node)` if nil; if `rs != nil && rs.budget > 0`, decrement `budget`, increment `retries`, set `ho.RecorderObject.Retries`, close `staleResp`, `attempt++`, replace `req` with `replayRetryRequest(rs.replay)`, set `cc = nil`, and `continue`. Fall through to the existing record/write path when replay or budget fails.
- After a successful dial (end of the `if cc == nil` block), create `rs` with `newRetryState(req, node)` when nil.
- Call `h.httpRoundTrip(withAttempt(ctx, attempt), ..., rs)`.
- On `retry == true`: decrement `budget`, increment `retries`, set `ho.RecorderObject.Retries`, close and nil `cc`, `attempt++`, replace `req` from `rs.replay`, and `continue`; a replay error returns the error.
- Keep `upstreamHost`/log fields as they are.

In `httpRoundTrip`:
- Add the `rs` parameter and the named `retry` return; skip the recording defer when `retry` is true.
- Set `ro.Time = rs.start` when `rs != nil`, otherwise `time.Now()` as today.
- Immediately after `http.ReadResponse` returns the final response and before any fail-code handling, body/header rewrite, caching or client write: if `rs != nil && rs.budget > 0 && resp.StatusCode != http.StatusSwitchingProtocols && rs.codes.Match(resp.StatusCode)`, mark the node (`node.Marker().Mark()`), log one `Warnf` with node name and status, and return `(false, true, nil)`. The deferred `resp.Body.Close()` owns the body.
- Do not duplicate the fail-code `Mark` for the same attempt; the retry return happens before the existing failCodes block.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd ../x && go test ./internal/util/forwarder -count=1`
Expected: PASS, including the pre-existing failCodes and rewrite tests.

- [ ] **Step 6: Run the x gate**

Run: `cd ../x && go build ./... && go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ../x
git add recorder/recorder.go internal/util/forwarder/rewrite_metadata.go internal/util/forwarder/retry.go internal/util/forwarder/sniffer_sse.go internal/util/forwarder/sniffer_http.go internal/util/forwarder/sniffer_http_retry_test.go
git commit -m "feat(forward): retry retryable upstream statuses through the hop selector"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 3: release x `v0.31.0`

**Files:** none (tag only).

**Interfaces:**
- Produces: the `github.com/go-gost/x` tag `v0.31.0` consumed by Task 4.

- [ ] **Step 1: Confirm the x gate is green and the tree is clean**

Run: `cd ../x && go build ./... && go vet ./... && go test ./... -count=1 && git status --short`
Expected: PASS, and only unrelated untracked docs (if any) in the status.

- [ ] **Step 2: Tag and push**

Run: `cd ../x && git tag -a v0.31.0 -m "v0.31.0: HTTP retries for the forwarder and dashboard releases" && git push origin v0.31.0`
Expected: the tag is created and pushed. Do not push branches unless the user asks separately.

- [ ] **Step 3: Record the tag commit**

Run: `cd ../x && git rev-parse v0.31.0^{commit}`
Record the SHA in this task's implementation record and in the cuttle spec/plan notes.

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: bump cuttle to x `v0.31.0` and verify marker semantics

**Files:**
- Modify: `go.mod`, `go.sum`
- Test: `internal/gateway/gateway_test.go` (add the marker regression)

**Interfaces:**
- Consumes: x `v0.31.0` (Task 3), specifically `HandlerRecorderObject.Retries` and the `b9d895a` marker behavior.

- [ ] **Step 1: Bump the dependency**

Run: `GOWORK=off go get github.com/go-gost/x@v0.31.0 && GOWORK=off go mod tidy`
Expected: `go.mod` shows `github.com/go-gost/x v0.31.0`; no `replace`. If the Go module proxy has not picked up the fresh tag yet, retry, or run the `go get` with `GOPROXY=direct` once.

- [ ] **Step 2: Write the marker regression test**

In `internal/gateway/gateway_test.go` add `TestFailMarkerAccumulatesAcrossRequests`:
- A single `httptest` upstream controlled by an `atomic.Bool`: `true` returns 500, `false` returns 200.
- `cfg := testConfig(upstream.URL)`; set `cfg.Routes[0].MaxFails = 2`, `cfg.Routes[0].FailTimeout = gwconfig.Duration(30 * time.Second)`, strategy `fifo`.
- Start the gateway and send two requests (`POST /v1/chat/completions`, body `{"model":"deepseek-chat","messages":[]}`). After both, assert `RouteHealth()[0].Nodes[0].FailCount == 2` and `Dead == true`.
- Flip the upstream to 200, send one request, assert status 200, `FailCount == 0` and `Dead == false`.

- [ ] **Step 3: Run the test**

Run: `GOWORK=off go test ./internal/gateway -run TestFailMarkerAccumulatesAcrossRequests -count=1`
Expected: PASS with `v0.31.0`; it would fail on `v0.30.0` because a successful connect used to reset the marker.

- [ ] **Step 4: Run the cuttle gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS. Fix any regression caused by the x delta in this task and record it.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/gateway/gateway_test.go
git commit -m "chore(deps): bump go-gost/x to v0.31.0"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: route config fields, defaults, validation, admin round-trip

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Test: `internal/admin/entrypoint_test.go`

**Interfaces:**
- Produces: `config.Route.Retries int` (`yaml:"retries,omitempty" json:"retries,omitempty"`), `config.Route.RetryCodes string` (`yaml:"retryCodes,omitempty" json:"retryCodes,omitempty"`), and `func parseRetryCodes(s string) ([]int, error)` used by validation.
- Consumes: nothing.

- [ ] **Step 1: Write the failing config tests**

Add to `config_test.go`:
- `TestRouteRetryDefaults`: load a YAML config with a valid route that omits both fields; assert `Retries == 0` and `RetryCodes == "429,5xx"`.
- `TestRouteRetryValidation`: table of invalid cases, each asserting the error names the route and the field: `retries: -1`, `retries: 11`, `retryCodes: "abc"`, `retryCodes: "5x"`, `retryCodes: "99"`, `retryCodes: "429,,502"`. Include one valid case `retryCodes: "429,5xx,502"` that loads.

Use the existing YAML fixture helpers in `config_test.go`; include an existing provider/node so the route's node reference resolves.

- [ ] **Step 2: Run the config tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run TestRouteRetry -count=1`
Expected: FAIL (`Retries`/`RetryCodes` unseen, defaults empty, invalid values accepted).

- [ ] **Step 3: Implement the struct fields, defaults and validation**

- Add the fields after `FailTimeout`.
- In `applyDefaults`, set `c.Routes[i].RetryCodes = "429,5xx"` when empty.
- In `Validate` for each route: reject `Retries < 0 || Retries > 10` with `route %q: retries must be between 0 and 10`; reject a non-empty `RetryCodes` with an invalid token via `parseRetryCodes`, wrapped as `route %q: retryCodes: ...`.
- `parseRetryCodes` accepts trimmed `N` (>= 100) and `Nxx` (single digit 1–9), rejects empty tokens and anything else.

- [ ] **Step 4: Add the admin round-trip test**

In `internal/admin/entrypoint_test.go`, add `TestRouteRetryFieldsRoundTrip`: create a route with `{"name":"retry","nodes":["n"],"retries":2,"retryCodes":"429,502"}`, then `GET /api/routes` and assert both fields are present with those values. Also `PUT /api/routes/retry` with `{"name":"retry","nodes":["n"],"retries":1,"retryCodes":"5xx"}` and assert the updated values.

- [ ] **Step 5: Run the tests**

Run: `GOWORK=off go test ./internal/config ./internal/admin -run 'TestRouteRetry|TestRouteRetryFieldsRoundTrip' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/admin/entrypoint_test.go
git commit -m "feat(config): add route retry settings"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: carry the route retry policy on every node

**Files:**
- Modify: `internal/gateway/build.go`
- Test: `internal/gateway/route_retry_test.go` (new)

**Interfaces:**
- Consumes: `config.Route.Retries`, `config.Route.RetryCodes` (Task 5).
- Produces: every node built for a route carries `retry.max` (`int`) and `retry.codes` (`string`) in its x metadata when `Retries > 0`; otherwise neither key exists. An empty `RetryCodes` with `Retries > 0` is stamped as `429,5xx`.

- [ ] **Step 1: Write the failing test**

`route_retry_test.go` (package `gateway`):
- Build a config with one provider with two enabled keys, two nodes (one `Backup: true`), and a route `{Name: "r", Nodes: []string{"n1","n2"}, Retries: 2, RetryCodes: "429,502"}`.
- Call `buildRoutePlan(...)`, take one hop (`plan.hops[""]`), assert it implements `hop.NodeList`, and for every node assert `mdutil.GetInt(n.Options().Metadata, "retry.max") == 2` and `mdutil.GetString(..., "retry.codes") == "429,502"`.
- A second config with `Retries: 0` asserts both metadata reads are zero/empty for every node.
- A third with `Retries: 1, RetryCodes: ""` asserts the stamped codes are `429,5xx`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/gateway -run TestRouteRetryMetadata -count=1`
Expected: FAIL (metadata absent).

- [ ] **Step 3: Implement the stamping**

In `buildVariantHop`, after `buildNodes` returns without error:
- When `r.Retries > 0`, compute `codes := r.RetryCodes`; if empty use `429,5xx`; for every returned `*xconfig.NodeConfig`, ensure `Metadata` is non-nil and set `retry.max`/`retry.codes`.
- Do not touch `buildNodes`'s signature.

- [ ] **Step 4: Run the test**

Run: `GOWORK=off go test ./internal/gateway -run TestRouteRetryMetadata -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/build.go internal/gateway/route_retry_test.go
git commit -m "feat(gateway): carry route retry policy on nodes"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 7: record `retries` in stats

**Files:**
- Modify: `internal/stats/store.go`
- Modify: `internal/stats/recorder.go`
- Test: `internal/stats/store_test.go`
- Test: `internal/stats/recorder_test.go`

**Interfaces:**
- Consumes: `xrecorder.HandlerRecorderObject.Retries` (Task 2 / bump Task 4).
- Produces: `stats.Request.Retries int` (`json:"retries"`), SQLite column `requests.retries INTEGER NOT NULL DEFAULT 0`, recorded from the x recorder object.

- [ ] **Step 1: Write the failing tests**

- In `store_test.go`: `TestInsertQueryRetries` — insert a `Request{ID:"r1", SID:"s1", TS:..., Retries:2, Status:200}` through `newStore(t)`, then `s.Get(ctx, "r1")` and `s.Query(ctx, Filter{})` assert `Retries == 2`; a row inserted with zero reads `0`.
- In `recorder_test.go`: `TestRecorderRecordsRetries` — build the JSON body used by the existing recorder tests, add `"retries": 1`, call `NewRecorder(...).Record(...)`, then `s.Get` and assert `Retries == 1`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestInsertQueryRetries|TestRecorderRecordsRetries' -count=1`
Expected: FAIL (`Retries` unknown / zero).

- [ ] **Step 3: Implement**

- Add `Retries int` to `stats.Request` next to `Truncated`.
- Add `"retries": "INTEGER NOT NULL DEFAULT 0"` to the `ensureColumns("requests", ...)` map.
- Add `retries` to the `INSERT OR REPLACE INTO requests` column list and one `?`/arg (`r.Retries`) at the matching position.
- Add `retries` to both `SELECT` column lists (`Query`, `Get`) after `truncated`, and `&r.Retries` to `scanRequests` at the same position.
- In `recorder.go`, set `Retries: ro.Retries`.

- [ ] **Step 4: Run the tests**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS, including the existing migration tests.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/store.go internal/stats/recorder.go internal/stats/store_test.go internal/stats/recorder_test.go
git commit -m "feat(stats): record per-request retries"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 8: capture keeps only the final attempt

**Files:**
- Modify: `internal/capture/capture.go` (`session.attempt`, `Offer` signature/reset)
- Modify: `internal/rewriter/rewriter.go` (pass the attempt from rewriter metadata)
- Test: `internal/capture/capture_test.go`
- Test: `internal/rewriter/rewriter_test.go`

**Interfaces:**
- Consumes: `attempt` in rewriter metadata (`forwarder.MetaKeyAttempt`, Task 2) delivered as `md["attempt"]` (read with the existing `intValue` helper).
- Produces: `(*capture.Manager).Offer(sid, direction string, original, rewritten []byte, ssePhase string, eventIndex, attempt int)`; a request-direction offer with a new attempt resets the session's captured data first.

- [ ] **Step 1: Write the failing tests**

- `TestNewAttemptReplacesPreviousCapture` in `capture_test.go`: `testManager(Policy{Mode:"errors", MaxBytes:64})`; `Start("s1","r","m")`; `Offer("s1","request",[]byte("a"),[]byte("ra"),"",0,0)`; `Offer("s1","response",[]byte("o"),[]byte("ro"),"",0,0)`; `Offer("s1","request",[]byte("a"),[]byte("rb"),"",0,1)`; `Finish("s1")`. Assert `RequestRewritten == "rb"` (not `"rarb"`), `ResponseRewritten == ""`, `RequestSHA256 == sha256("rb")`, and `m.totalBytes == 0` after `Finish` (the reset must subtract the discarded attempt's bytes).
- Keep `TestSecondChunkMarksTruncated` as an append test by changing its two Offer calls to the same attempt (`attempt 0`); do not weaken its assertions.
- `TestRewritePassesAttemptToCapture` in `rewriter_test.go`: create `capMgr := capture.NewManager(capture.Policy{Mode:"errors", MaxBytes:256})`, `rw := New(engine.New(engine.Options{}), Options{Capture: capMgr, Protocol: "chat", Route: "r"})`, and call `rw.Rewrite` twice with a minimal valid chat request body, using metadata `{"sid":"s1","direction":"request","uri":"/v1/chat/completions","attempt":0}` then `attempt:1`. `capMgr.Finish("s1")` must return a capture whose `RequestRewritten` contains the body once, not twice.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/capture ./internal/rewriter -run 'TestNewAttemptReplacesPreviousCapture|TestSecondChunkMarksTruncated|TestRewritePassesAttemptToCapture' -count=1`
Expected: FAIL (Offer arity, or concatenated previews).

- [ ] **Step 3: Implement the capture reset**

- Add `attempt int` to `session`, initialized to `-1` in `newSession`.
- `Offer` gains the `attempt` parameter. For the request direction: if `attempt != s.attempt`, subtract `s.bytes` from `m.totalBytes`, rebuild `*s = *newSession(reason, onError, created)` with the preserved `Reason`/`OnError`/`CreatedAt` from the old capture, and set `s.attempt = attempt`. Then append as today. Response directions do not reset; they just record with the same attempt.
- Update every `Offer` call in `capture_test.go` with the attempt argument (`0` for the existing single-attempt tests).
- Update the two call sites in `internal/rewriter/rewriter.go`: in `Rewrite`, read `attempt := intValue(md["attempt"])`; add an `attempt int` parameter to `captureRewrite` and pass it through to both `Offer` calls (the error path and the success path).

- [ ] **Step 4: Run the tests**

Run: `GOWORK=off go test ./internal/capture ./internal/rewriter -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capture/capture.go internal/capture/capture_test.go internal/rewriter/rewriter.go internal/rewriter/rewriter_test.go
git commit -m "feat(capture): keep the final retry attempt only"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 9: console types, route fields, and the Sessions retry marker

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/requestLabels.ts`
- Modify: `web/src/pages/Requests.tsx`
- Modify: `web/src/components/RequestDetail.tsx`
- Modify: `web/src/pages/Edit.tsx`
- Modify: `web/src/pages/Routes.tsx`
- Modify: `web/src/locales/en-US.ts`
- Test: `web/src/pages/Requests.test.ts`
- Test: `web/src/pages/Edit.test.tsx` (if it renders the route form; otherwise rely on tsc + the manual form check)

**Interfaces:**
- Consumes: `Route.retries`/`route.retryCodes`, `RequestRow.retries` (Tasks 5 and 7).
- Produces: `retryLabel(row: RequestRow): string`; UI fields and tags.

- [ ] **Step 1: Write the failing label test**

In `Requests.test.ts`, add a case for `retryLabel`: `retryLabel({...base, retries: 0}) === ''` and `retryLabel({...base, retries: 2}) === '重试 2 次'` (import `retryLabel` from `./Requests`; re-export it there like the other helpers).

- [ ] **Step 2: Run the web test to verify it fails**

Run: `cd web && npm test -- --run src/pages/Requests.test.ts`
Expected: FAIL (missing export).

- [ ] **Step 3: Implement the types and helpers**

- `types.ts`: add `retries?: number; retryCodes?: string;` to `Route` and `retries?: number;` to `RequestRow`.
- `requestLabels.ts`: add `retryLabel(row)` returning `''` when `(row.retries ?? 0) <= 0`, else `t('重试 {count} 次', { count: row.retries })`.
- `Requests.tsx`: re-export `retryLabel`; render `<Tag color="geekblue">{retryLabel(r)}</Tag>` inside the existing tags `Space` only when the label is non-empty.
- `RequestDetail.tsx`: add `<Descriptions.Item label="retries">{request.retries ?? 0}</Descriptions.Item>` next to the duration row.
- `Edit.tsx`: in both route forms (the edit drawer and the create modal) add `retries` (`InputNumber`, min 0, max 10, initial 0) and `retryCodes` (`Input`, placeholder `429,5xx`, initial `429,5xx`); include both in the submitted values and in the form defaults for existing routes.
- `Routes.tsx`: append ` · retries N` to the subtitle only when `(r.retries ?? 0) > 0`.
- `locales/en-US.ts`: add `'重试 {count} 次': 'retried {count}×'`.

- [ ] **Step 4: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS. Rebuilding writes `internal/admin/dist` (git-ignored).

- [ ] **Step 5: Commit**

```bash
git add web/src/api/types.ts web/src/requestLabels.ts web/src/pages/Requests.tsx web/src/components/RequestDetail.tsx web/src/pages/Edit.tsx web/src/pages/Routes.tsx web/src/locales/en-US.ts web/src/pages/Requests.test.ts
git commit -m "feat(web): show route retries and retried sessions"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 10: gateway end-to-end retry test

**Files:**
- Test: `internal/gateway/retry_e2e_test.go` (new, package `gateway_test`)

**Interfaces:**
- Consumes: config fields (Task 5), node metadata (Task 6), stats `retries` (Task 7), x retry behavior (Task 4).

- [ ] **Step 1: Write the test**

`TestGatewayRetriesRetryableUpstream`:
- First `httptest` server counts hits and returns 429 with a small JSON error body; second counts hits and returns a valid chat completion JSON (same body shape as `internal/gateway/stats_test.go`).
- Config: `Gateway.Listen = "127.0.0.1:0"`, `Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")`, entrypoint `*` → route `r`, two providers `a`/`b` (type `generic`, `Chat` pointing at the two servers, one key each), two nodes `na`/`nb` (model `deepseek-chat`), route `{Name:"r", Nodes:[]string{"na","nb"}, Strategy:"fifo", Retries:1, RetryCodes:"429,5xx"}`.
- Start the gateway, POST `/v1/chat/completions` with `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`, drain the body, assert status 200.
- Assert first hits 1, second hits 1.
- Use the existing `waitRow` helper (same package) and assert `row.Retries == 1`, `row.Status == 200`, `row.Node == "nb"`.
- Assert the response header `X-Cuttle-Request-ID` equals `row.ID` (the retried request keeps one identity across attempts).

- [ ] **Step 2: Run the test**

Run: `GOWORK=off go test ./internal/gateway -run TestGatewayRetriesRetryableUpstream -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/retry_e2e_test.go
git commit -m "test(gateway): cover end-to-end route retries"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 11: docs, example config, and the full gate

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `example.yaml`
- Rebuild: `internal/admin/dist` (git-ignored, generated by `npm run build`)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Update the user guide**

- `README.md` routes table: add `retries` (`Extra attempts; 0 disables, default 0`) and `retryCodes` (`Statuses that trigger a retry, default 429,5xx`).
- Add a short paragraph after the routes section: retries re-select through the route strategy, only trigger before any response byte reaches the client (dial failures included, post-write read failures not), samples are bounded by a 32 MiB request buffer, `Expect: 100-continue` requests are not retried, and no `Retry-After` backoff exists in v1. State the worst case: each attempt is bounded by `gateway.readTimeout`, so `retries: 10` can hold a connection for roughly eleven read timeouts.
- Stats section: mention `requests.retries` and that Sessions shows `重试 N 次`.
- Do not promise attempt-level rows or attempt-level captures.

- [ ] **Step 2: Update the agent invariants**

`AGENTS.md`:
- Routing invariants: routes carry `retries`/`retryCodes`; the values are stamped on every node as `retry.max`/`retry.codes`, and x owns the retry loop.
- Gateway invariants: retries re-run `hop.Select`, mark the failed node, happen only before response bytes, and keep one request row.
- Stats invariants: `requests.retries` is additive, counts extra attempts only, and capture keeps the final attempt (`attempt` metadata).
- Gotchas: the x dependency must be `v0.31.0` or newer for retries; `GOWORK=off` still applies.

- [ ] **Step 3: Update `example.yaml`**

Add `retries: 1` and `retryCodes: "429,5xx"` to one route; run `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1` so the fixture stays valid.

- [ ] **Step 4: Rebuild the console and run the full gate**

```bash
cd web && npm run build
cd ..
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npx tsc --noEmit
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md AGENTS.md example.yaml
git commit -m "docs: document route retries"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

## Self-review notes

- Spec coverage: config (Task 5), node metadata (Task 6), x retry loop and marker (Tasks 1–2), stats `retries` (Task 7), capture final attempt (Task 8), UI (Task 9), release/bump and marker regression (Tasks 3–4), end-to-end (Task 10), docs (Task 11).
- Failure modes listed in Review Focus are covered by named tests in Task 2 and Task 5.
- The x tag is pushed before the cuttle bump so `GOWORK=off` verification uses the published module, never a `replace`.
- Interface names are consistent across tasks: `retry.max`/`retry.codes`, `MetaKeyAttempt`, `retries`, `HandlerRecorderObject.Retries`, `stats.Request.Retries`.
