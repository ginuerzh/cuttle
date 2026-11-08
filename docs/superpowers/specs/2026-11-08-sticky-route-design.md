# Session-affine weighted routing for a route

## Intent

`deepseek-flash` has two Chat primaries (`oc-go-dsf` weight 10, `cc-dsf`
weight 3) and one native-Anthropic backup (`deepseek-pi`). The route currently
uses `fifo`, so `cc-dsf` gets no traffic while `oc-go-dsf` is healthy, and
`weight` has no effect. Switching the route to `random` would use the weights
but would move a conversation between providers on every request, throwing
away each provider's prompt cache.

Add a `sticky` route strategy that makes the weights effective across
conversations while keeping one conversation on the same node.

## Goals

- A session stays on one eligible node for its whole lifetime, preserving the
  provider's prompt cache.
- First selection is weighted: node `i` is chosen with probability
  `weight_i / sum(weight)`.
- When a node is removed from the eligible set (failed), only sessions mapped
  to that node are remapped; healthy sessions keep their node and cache.
- The mapping survives gateway restarts and config reloads.
- Session identity comes from the client's own session header, with Claude
  Code subagents kept separate from their lead.
- Open mode (no `gateway.keys`) stays a pass-through that does not parse the
  request twice.

## Non-goals

- Cache-aware affinity (`auto`), turn scoping, strict mode or a persisted
  affinity map.
- Real-time load balancing; the weights are static.
- Eliminating the "both primaries failed, use `deepseek-pi`" 400. The interim
  mitigation is `maxFails: 2`; the backup remains a known limitation.
- Changing entrypoint/route precedence or the `entrypoint -> route -> node ->
  provider` chain.

## Constraints

- cuttle routing uses the x hop group and selector; do not replace it with a
  custom dispatcher.
- Open mode must not parse the request; session extraction must happen where
  the HTTP request is already parsed (the x HTTP sniffer).
- x changes are released by tag, then cuttle bumps the tagged dependency.
  cuttle's `go.mod` has no `replace`; run the cuttle gate with `GOWORK=off`.
- The x release also carries the HTTP dial-failure stats fix (`408ad41`).

## Approach

### x: `sticky` strategy (weighted rendezvous hash)

For each eligible node compute:

```
u = hash(clientID + "\x00" + node.Name) / 2^64
score = -ln(u) / weight
```

and select the node with the smallest score. This is weighted rendezvous
hashing: the probability of selecting node `i` is `weight_i / sum(weight)`, the
mapping is deterministic for a client and a node set, and removing a node only
remaps the sessions that hashed to it. It is stateless, so it survives
restarts and reloads.

The strategy reads `xctx.ClientIDFromContext`. An empty client ID falls back
to the existing weighted random strategy, so clients without a session header
still route.

### x: session identity hook

The x HTTP sniffer already parses the request. Add a `ClientID` function to
`sniffing.HandleOptions`, passed by the forward handler from a new
`forward.local` metadata key. The sniffer calls it after `http.ReadRequest`
and stores the result in the request context before hop selection. No extra
parse is added, and open mode keeps its pass-through behavior.

### cuttle: session extraction

`internal/gateway` provides `clientIDFromRequest`:

- Try the known session headers in order: `x-claude-code-session-id`,
  `x-opencode-session`, `x-session-affinity`, `x-session-id`, `session_id`,
  `session-id`, `x-conversation-id`,
  `x-deepseek-harness-session-id`.
- Hash the value with SHA-256; append `|agent:<sha256>` when
  `x-claude-code-agent-id` is present, so a subagent has its own stick.
- Return an empty string when no session header is present.

The gateway passes this function to `forwardlocal` through the handler
metadata (`clientID`). It is the only cuttle data-plane change; the auth
handler is untouched.

### Route and weights

`deepseek-flash` changes from `fifo` to `sticky`; `oc-go-dsf` weight 10 and
`cc-dsf` weight 3 give a 10:3 split across new sessions. `deepseek-pi` stays
`backup: true`; the backup filter removes it while a primary is eligible.
`maxFails: 2` stays as the interim failover mitigation.

## Alternatives considered

- **In-memory `session -> node` map.** Simple, but needs TTL/cleanup and loses
  every mapping on a restart or reload; a conversation can switch providers
  and lose its cache. Rejected for the cache goal.
- **Modulo hash (`strategy: hash`).** Stateless, but ignores weights and
  remaps most sessions when the eligible set changes. Rejected.
- **`strategy: random`.** Uses weights but re-selects every request, so a
  conversation alternates providers and loses prompt cache. Rejected.
- **Full magpie-style affinity (cache-aware, turn, strict, persisted).**
  More state and behavior than needed; deferred.

## Decisions left to the builder

- Hash function and score formulation; the plan uses FNV-1a 64 and
  `-ln(u)/weight`.
- Strategy name and UI label (`sticky`).
- Whether to expose the strategy in the route editor's select (yes).
- Docs updates in `README.md` and `AGENTS.md`.

## Verification

- x unit tests: determinism, weighted distribution, node removal, node order,
  empty client ID, single node, zero weight.
- x sniffer test: the client ID function is applied before hop selection.
- cuttle integration: two fake upstreams with weights 10:3; the same session
  header always hits the same upstream; many sessions split about 10:3; a 5xx
  marks the node and the session moves, then sticks to the new node.
- cuttle full gate with `GOWORK=off` after the x tag, plus the web gate.
