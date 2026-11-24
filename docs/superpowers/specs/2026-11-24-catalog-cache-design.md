# Catalog limits cache design

Date: 2026-11-24
Status: proposed

## Intent

Admin configuration saves currently wait up to five seconds for every referenced
provider model catalog. A single unavailable catalog (currently Gemini) consumes
the shared deadline before the data-plane reload and config write complete.
Configuration management must not depend on upstream catalog availability.

## Goals

- `PUT /api/nodes/{id}` and other admin config mutations return without waiting
  for provider catalog HTTP requests.
- Preserve the existing live `max_tokens` output-cap behavior when a usable
  catalog has been fetched.
- Keep the last good limits when a later refresh fails.
- Bound and coalesce background refresh work, and stop it on gateway shutdown.

## Non-goals

- No UI or config schema changes.
- No change to request rewriting or the cap semantics.
- No guarantee that a brand-new provider has a cap before its first background
  refresh completes and a later reload consumes it.
- No cache fingerprint for offline provider type/base-URL edits; those edits can
  retain the previous provider's limits until the TTL expires. The admin API
  already forbids those edits.
- No attempt to make startup independent of catalogs; startup may perform one
  bounded synchronous prime so the first served build has caps.

## Constraints

- `buildHopGroup` currently performs catalog I/O before compiling routes.
- `admin.replaceLocked` calls `Gateway.Reload` synchronously while holding the
  admin mutation lock.
- Provider ids are immutable through the admin API. Provider type and non-generic
  API bases are also immutable there, so a provider-id keyed cache is safe for
  normal admin workflows. Key rotation does not change model output limits.
- Catalog failures are already best-effort and must remain non-fatal.

## Approaches considered

### A. Last-good cache with asynchronous refresh (chosen)

Keep a gateway-owned per-provider cache of model output limits. Startup performs
one bounded synchronous prime. Later builds read the cache immediately and
schedule missing, stale, or retryable entries for background refresh. Refreshes
are per-provider, coalesced, bounded, and concurrent across providers.

Trade-off: limits can be stale until a refresh and later reload. This matches the
existing documented staleness model while removing upstream I/O from saves.

### B. Concurrent catalogs with short per-provider deadlines

Fetch catalogs concurrently during every reload with a small shared or
per-provider budget.

Rejected: saves would still wait on the network and inherit provider latency.

### C. Remove runtime catalog reads

Use only static configuration or startup values.

Rejected: loses catalog freshness and changes the current feature contract more
than necessary.

## Design

- Add a `catalogLimitsCache` owned by `Gateway`.
- Cache entries are keyed by provider id and hold the last good output-limit map,
  fetch time, and last attempt time.
- Use a ten-minute freshness TTL and a bounded retry interval after failures.
- `Limits(cfg)` returns a deep copy immediately and schedules background refresh
  for missing or stale providers. A failed refresh retains the previous limits.
- Background requests use independent five-second contexts so one provider
  cannot cancel another provider's refresh.
- Refactor `buildHopGroup` to accept resolved `catalogLimits`; it performs no
  catalog network I/O.
- `Gateway.New` primes the cache once with the existing bounded startup budget,
  then applies the initial build. `Gateway.Close` cancels and joins refreshes.

## Verification

- A blocked catalog must not delay a reload that has cached limits.
- A failed refresh must retain the last good cap.
- Missing/stale entries must refresh in the background without duplicate calls.
- Existing cap propagation and protocol clamp tests must remain green.
- Full Go and web gates must pass before completion.

## Implementation record

No implementation yet.
