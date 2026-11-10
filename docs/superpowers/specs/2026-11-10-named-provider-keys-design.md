# Named provider keys with per-node key pinning

## Intent

A provider can hold several upstream account keys, but today `provider.keys` is
an unnamed `[]string`. When a provider has more than one key, `buildNodes`
expands every key into its own x node and the route selector spreads traffic
across them. There is no way to say "this node, route or model must use account
B's key", so two routes backed by the same provider share every account. That
blocks quota isolation.

Production currently runs one account key per provider, so this is the point to
give every provider key a name, make the name the stable handle, and let a node
optionally pin one key.

## Goals

- `provider.keys` is a list of `{name, key, enabled}` entries. Names are
  unique inside a provider.
- `node.key` (optional) pins the node to one provider key by name; the node
  then builds only that key.
- Unpinned nodes keep today's behavior: every enabled key expands into an x
  node, and the route selector spreads and fails over across them.
- Disabled keys stay configured but are skipped at build time.
- The console manages named keys (add, enable/disable, delete) without ever
  returning a secret.
- The Usage page shows the key name beside the masked preview.
- Old `keys: ["sk-..."]` fails loudly at config load. There is no migration
  command and no compatibility path.

## Non-goals

- Per-request key selection inside one node; the expansion already spreads at
  node level.
- Per-key request attribution in stats. The internal x node name carries the
  key name, but stats still aggregate by the config node.
- Renaming a provider key; delete and re-add is the escape hatch.
- Generating or rotating provider keys; they are issued upstream.
- Backward compatibility with unnamed keys.

## Anti-goals

- Storing the raw key value on the node. It would duplicate a secret into
  `nodes` and into `GET /api/config`.
- Changing the `entrypoint -> route -> node -> provider` chain or making route
  order semantic.
- Silently using another key when a pinned key is missing or disabled.

## Constraints

- `config.Load` uses `yaml.KnownFields(true)`; the shape change is a hard cut.
- x node names stay `<route>/<provider>/<node>[/<key>]`;
  `stats.parseNodeName` reads parts[2] as the node.
- Key names must not contain `/` (they go into the x node name) or `$` /
  control characters (`config.Load` expands the environment on every reload).
- Provider keys are write-only through the admin API; only masked previews are
  returned.
- cuttle's `go.mod` has no `replace`; run the gate with `GOWORK=off`.
- Do not edit sibling repositories as part of this work.

## Approach

### Config schema

```yaml
providers:
  - name: deepseek-dadax
    type: deepseek
    chat: https://api.deepseek.com/v1
    keys:
      - name: account-a
        key: ${DEEPSEEK_A}
      - name: account-b
        key: ${DEEPSEEK_B}
        enabled: false
nodes:
  - name: Deepseek-dadax
    provider: deepseek-dadax
    model: deepseek-flash
    key: account-a        # pinned; omit to spread across all enabled keys
```

- Add `config.ProviderKey{Name, Key string; Enabled *bool}` and change
  `Provider.Keys` to `[]ProviderKey`.
- Name validation mirrors gateway keys: required, trimmed, at most 64 runes,
  unique inside the provider, no `/`, `$` or control characters.
- Key validation keeps the existing non-empty check and adds the gateway-key
  `$` / control-character rejection so a reload cannot reinterpret or fail on
  an admin-written value.
- `enabled` defaults to true, like gateway keys.
- Add `Node.Key string` (`yaml:"key,omitempty"`). Empty means unpinned. When
  set it must name a key of the node's provider; a dangling name is a
  validation error.

### Build

- `buildNodes` resolves the key set per node:
  - Pinned: exactly the named key. A missing name is a config error; a
    disabled key skips the node with a warning, matching disabled models.
  - Unpinned: every enabled key. A provider with keys but none enabled skips
    the node with a warning. A provider with no keys keeps the keyless `[""]`
    node.
- The x node name suffix is the key name, e.g.
  `<route>/<provider>/<node>/account-a`, added only when more than one key node
  is built. Single-key nodes keep today's suffix-free name.
- `stats.parseNodeName` keeps reading parts[2]; its comment changes from
  `<keyIndex>` to `<key>`.
- The catalog fetch (`provider/models.go`) and the admin health probe
  (`admin/probe.go`) authenticate with the first enabled key instead of
  `Keys[0]`; when every key is disabled they fall back to the keyless request.

### Admin API

- Replace `providerView.KeysSet` / `KeyPreviews` with
  `keys: [{name, keyPreview, enabled}]`; the raw value is never returned. The
  HTTP field is still `keys` on both the read view and the write payload; the
  console keeps them in separate TypeScript types (a masked `ProviderKeyView`
  and a raw input type).
- `keyUpdates` becomes name-based:
  - `{name, key}` appends a key; a duplicate name is rejected against the
    post-delete set, so one request may delete and re-add the same name (value
    rotation).
  - `{name, delete: true}` deletes a key; deleting a key is rejected only when
    a node of the same provider pins that name.
  - `{name, enabled}` sets the flag when `key` and `delete` are absent; it
    uses set semantics like `PUT /api/gateway-keys/{name}`.
- Full `keys` replacement (`POST /api/providers`, `PUT /api/providers/{name}`
  with `keys`, and `PUT /api/config`) follows one rule:
  - an absent (`nil`) list preserves the stored keys for that provider;
  - an explicit `[]` clears them;
  - a non-empty list replaces the set by name. An entry with an empty `key`
    whose name already exists keeps the stored value, so a GET -> PUT
    round-trip of the masked view is safe; any other empty `key` is a
    validation error.
  This aligns `PUT /api/providers/{name}` with `PUT /api/config`, which
  already clears only on an explicit `[]`.
- The Usage page queries every configured key, including disabled ones, and
  shows `enabled`; `provider.AccountUsage` gains `Name` and the admin usage
  view carries `name` beside `keyPreview`.

### Web

- Types: `Provider.keys: ProviderKeyView[]` (masked) for reads, a separate
  raw input type for create/update payloads, and `Node.key?: string`.
- Provider form: named key rows with preview, enabled switch, delete, and an
  add row that takes a name and a value.
- Node form: a key `Select` with "全部 key" plus every key's name and preview,
  including disabled keys with a marker (a disabled pin is valid; the node is
  skipped at build time).
- Node list/detail: show the pinned key name.
- Add zh and en strings for the new labels.

### Docs

- Update `README.md`, `example.yaml` and the `AGENTS.md` config, admin API and
  web invariants.

### Production rollout (k3s)

- Back up `/data/cuttle.yaml`, add `name:` to each provider key, write it back,
  then roll out the new image. There is no product migration command.

## Alternatives considered

- **`node.keyIndex` (positional pin).** Smallest change and no secret
  duplication, but deleting a key shifts every later index, so a pin can
  silently move to another account. Guarding that needs node-aware deletion,
  and names are wanted anyway to tell accounts apart. Rejected.
- **`node.key` stores the raw key value.** Stable across key edits, but
  duplicates the secret into `nodes` and `GET /api/config`, and lets the node
  key drift from the provider's key list. Rejected.
- **Compatible scalar parsing plus a `cuttle migrate` command.** Keeps old
  configs running, but adds a second unnamed key state (not pinnable, not
  addressable by name) to config, admin and the console. Rejected by the
  operator in favor of a hard cut and a manual k3s edit.

## Decisions left to the builder

- Console layout and copy for the named key rows and the node pin Select.
- Whether to surface the pinned key name anywhere beyond the node card.
- k3s rollout commands, backup name and verification steps.

## Verification

- config: named keys parse; scalar keys fail; duplicate or invalid names fail;
  a node pin resolves; a dangling pin fails; a disabled key is accepted but
  skipped at build.
- gateway: a pinned node sends only that key; an unpinned node expands enabled
  keys; disabled keys are skipped; all-disabled and keyless cases still work;
  key-level failover still works for unpinned nodes.
- admin: the view masks values; append/delete/toggle by name work; deleting a
  pinned key is rejected; create with named keys works.
- web: type and payload tests, provider key rows, node pin select.
- Full Go gate with `GOWORK=off` and the web gate.
- k3s: the edited config loads, `/api/health` is OK, and a real request routes
  through a pinned node.
