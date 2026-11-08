# Overview animation switch and icon flash

Date: 2026-11-08
Status: proposed
Repository: `llm-gw`

## Intent

The left navigation should read `密钥` instead of `网关密钥`. The Overview
should make new traffic easier to notice by flashing the node's icon instead
of drawing a ring around the whole card, and operators need one switch that
turns every Overview animation off.

## Behavior

### Navigation label

- `NAV_LABELS['gateway-keys']` becomes `密钥`. The English translation is the
  existing `Key` entry.
- The Gateway keys page heading, dialogs, alerts and the Sessions filter keep
  the term `网关密钥`; only the left navigation label changes.

### Node icon flash

- Remove the card-level expanding ring (`NodePulse` / `gw-node-pulse`).
- When a snapshot reports new traffic for a node, the node's `IconBadge`
  plays one flash: scale `1 -> 1.24 -> 1.10 -> 1`, brightness up, and a bright
  cyan halo, finishing in about 600 ms. The duration is the flash window that
  option C used in the preview; the intensity is option B.
- The animation is one-shot. A new pulse remounts the badge and replays it;
  the class stays until the next pulse but the CSS animation does not loop.
- Error pulses use the same shape with a red halo.
- The badge keeps its protocol color at rest, so nothing changes visually
  while idle.
- `prefers-reduced-motion: reduce` still suppresses the flash, as it did for
  the ring.

### Animation switch

- Add a small `动画` switch to the Overview toolbar, in the monitor-mode
  control group next to the time range, default **on**.
- Persist it in `localStorage` under `llm-gw.animation` (`1` on, `0` off) and
  read it on startup. Storage failures fall back to on.
- The switch is the single master switch: when off, node icon flashes and the
  edge light-dot animation do not run. Metrics, labels and graph updates keep
  working.
- While the switch is off, activity snapshots continue to advance the
  comparison baseline so turning animations back on does not replay old
  traffic.
- Edit mode hides the switch with the other paused monitor controls; edit mode
  has no traffic animation anyway.

## Approaches considered

1. **CSS keyframes on the icon badge, keyed by the pulse (chosen).** Matches
   the existing one-shot animation pattern, needs no animation library, and
   keeps the work in the node renderer and stylesheet.
2. **Web Animations API or a rAF loop for the badge.** More control over
   easing, but more code and cleanup for an effect CSS already expresses.
3. **Server-side animation setting.** Shared across admins, but it turns a
   per-browser comfort preference into a config-schema and Admin API change.
   Rejected as disproportionate.

## Non-goals

- Changing the edge light-dot animation itself or its error colors.
- A per-node or per-edge animation setting.
- Server-side or cross-device persistence of the switch.
- Translating the page heading or other `网关密钥` strings to `密钥`.
- New visual regression tests; there is no browser screenshot harness.

## Testing

- Navigation: `NAV_LABELS['gateway-keys']` is `密钥` and translates to `Key`.
- Nodes: a pulse renders the icon flash class, an error pulse renders the
  error variant, and no pulse renders neither. The old ring class is gone.
- Toolbar: the switch renders in monitor mode with the configured state and
  calls its handler; edit mode still hides the monitor controls.
- Topology: `animate=false` leaves node pulses and edge pulses at zero while
  metrics still update; toggling back on does not replay an old pulse.
- Preference helper: stores `0`/`1`, reads the default when absent or storage
  throws.
- Full gates: `npm test`, `npx tsc --noEmit`, `npm run build`, then the Go
  build, vet and tests; rebuild the embedded bundle before the final Go build.

## Acceptance criteria

- The left navigation shows `密钥`; the Gateway keys page itself still says
  `网关密钥`.
- New node traffic produces one visible icon flash and no card ring.
- The Overview toolbar shows an `动画` switch, on by default.
- Turning the switch off stops both icon flashes and edge light dots; turning
  it back on does not replay older activity.
- The choice survives a page reload in the same browser.
- With `prefers-reduced-motion: reduce`, no node flash plays.
