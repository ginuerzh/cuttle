# Entrypoint specificity match (longest-match routing)

Date: 2026-11-16
Status: approved

## Intent

Entrypoint matching in the admin UI's FIFO-based hop group currently
rewards declaration order: a broader entrypoint listed earlier swallows
every request including those meant for a more specific one listed later.
The concrete failure: a Web UI-created `fable` entrypoint (`claude-fable*`,
added after a `*` catchall) was unreachable; every `claude-fable-5` request
was served by the catchall's route. Entrypoints should behave like
URI routes: a more specific match wins regardless of file order.

Audience: cuttle operators defining `entrypoints:` in config (or creating
them via the web UI, which appends new entries at the end of the list).

## Goals

- Compile-time specificity sort: ordering by score, then by wildcard count,
  then by original declaration order (stable).
- Score = number of non-`*` characters in the entrypoint's `match` globs,
  computed as the **max** literal count across globs (a request can match at
  most one glob per entrypoint, so summing would let redundant globs outrank
  an exact match). Tiebreak: total asterisk count ascending across globs;
  final tiebreak: original declaration order (stable).
- `exact > prefix > catchall` always falls out of the score, not a special
  case.
- Sort applies uniformly to every compiled variant (protocol-group entries
  and the fallback entry), because they all derive from `cfg.Entrypoints`.
- Behavior on equally-specific entries keeps the current semantics
  (declaration order).
- Document the change in README and the upgrade notes: configs that placed
  specific entries after `*` will start winning after upgrade.

## Non-goals

- No new config fields (no `priority:`), no UI changes, no write-back to the
  YAML. The list order in the config/UI remains the declaration order; the
  build decides matching itself.
- Not URI-path matching. Path is still used as a protocol-group
  precondition; only the body-model glob is scored. `match` remains a model
  glob, not a URL.
- No change to binding order inside an entrypoint (probe semantics stay).

## Constraints

- Entrypoint matching is a mechanism, not a policy document: it must stay
  deterministic, stable, and cheap.
- The sort runs before any entry is appended to the hop group; the FIFO
  group selector itself is unchanged.
- The spec lives in cuttle; YAML validation does not reject any previously
  valid `match` value.

## Approaches considered

### A. Catchall always last (rejected)

Only handles the `*` case; `claude-*` vs `claude-fable*` still suffers. Kept as a fallback reading if B turns out to be contentious.

### B. Longest-match specificity (chosen)

Generalizes to everything: `exact > prefix > catchall` falls out of the score. Stable, cheap, one compile pass. Trade-off: any glob like `claude-*-pro-*-spec-*` can beat an exact match if it has more literals — this is the requested URI-like semantics; it is strict to understand because score is published in the doc, not implicit.

### C. Validation rejects later catchall (rejected)

Fails loudly on currently-valid configs; the operator just edited their own live config the same way this morning (`bak-20261116-pre-fable-order`) — a config that used to work "unexpectedly well after" is exactly what they want here, not a reload error.

## The build

- New `ordering.go` in `internal/gateway/`: `specificityScore(match []string) int` (max non-`*` count across globs) and `orderEntrypoints(eps []config.Entrypoint) []config.Entrypoint` (stable: score desc, total `*` count asc, index asc).
- `build.go`: stable-sort `cfg.Entrypoints` clone before the variant loop; every subsequent matcher is generated in specificity order; FIFO group selector stays as is.
- Unit tests: stable-sort permutations on a fixture; score edge cases; empty/draft entries keep position among equally-scored ones.
- Gateway integration test: `*` catchall listed before `claude-fable*` still yields the fable route for `claude-fable-5` (the config that currently loses by order).
- README note + release-worthy changelog line.

## Documented limitation (to README)

A `*` catchall still shadows nothing. A `claude-` beats `*` but loses to `claude-fable*` and `claude-fable-5`. Equally specific entries fall back to declaration order.
