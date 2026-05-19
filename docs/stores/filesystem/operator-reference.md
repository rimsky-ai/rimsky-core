---
concept: claim-producer-fs-store
definition: |
  The bundled reference claim-producer that treats folders under a configured root as the resource each claim acquires. Concrete-paths only; scope-conflict is byte-equal on the path bytes.
proto_symbol: ClaimProducer in protocols/proto/v1/claim_producer.proto
config_field: rimsky.yml:claim_producers
api_surface: (none)
related: [claim-producer, claim, scope, write-semantics]
deprecated_terms: [filesystem-store-bundle]
---

# Filesystem store (`stores/filesystem/`)

The bundled filesystem store is a reference [claim producer](claim-producer.md) that treats folders under a configured root as the resource each [claim](claim.md) acquires.

## Selectors

Two selector forms:

- **Concrete-path selector** — e.g. `documents/alpha`. Resolves to a path under `<root>/<selector>`. Two claims on the same logical path produce byte-equal scopes (for the cross-claim conflict check).
- **Pick-policy selector** — e.g. `@docs-ring`. Resolves via a configured `pick_policy` entry to a discovered folder under `<root>/<policy.root>`. Each pick policy implements a queue/ring/single-pass workflow over the folders it discovers.

## Pick-policy actions (v2)

Each pick policy declares two action fields. Both are required.

| Field | Fires on |
|---|---|
| `on_commit` | `Commit` (success path) |
| `on_give_up` | `Abandon` (failure path) |

Each action takes a value from the v2 vocabulary:

| Action | Queue entry | Folder on disk |
|---|---|---|
| `pop` | consumed | kept in place |
| `pop_and_move: <target>` | consumed | renamed to `<root>/<target>/<folder>` |
| `pop_and_delete` | consumed | destroyed (`os.RemoveAll`) |
| `recycle` | returned to queue tail | kept in place |

YAML shape: bare strings for non-parameterized actions; one-key map for `pop_and_move`:

```yaml
pick_policies:
  "@docs":
    root: documents
    folder_pattern: "^[a-z][a-z0-9_-]*$"
    on_commit:
      pop_and_move: documents.failed
    on_give_up: recycle
    sync_strategy: on_drain
    visibility_timeout_seconds: 1800
```

## `sync_strategy`

Controls when the store re-discovers folders under `<root>/<policy.root>`.

| Value | Sync runs when |
|---|---|
| `on_open` | every `Open` call (default) |
| `on_drain` | only when `available/` is empty AND no `drained` sentinel is present (start of a new pass) |
| `explicit` | only via the admin endpoint |
| `never` | only at startup (initial population) |

The `on_drain` mode produces single-pass-then-refresh queue mode: each pass yields N `Acquired` outcomes followed by one `Unavailable`. The next `Open` after that re-runs sync and starts a new pass.

## Common patterns

- **Ring-mode + live discovery:** `recycle + on_open`. Forever-cycling queue with new folders detected automatically.
- **Queue-mode + auto-refresh:** `pop + on_drain`. Process each item once per pass; a new pass starts after the next `Open` fires sync.
- **Stage-promote:** `pop_and_move(target=promoted) + on_open`. Folders move to a sibling directory after success.
- **One-shot ingest:** `pop_and_delete + on_drain`. Folders disappear after success; queue drains permanently.
- **Static queue + explicit refresh:** `pop + explicit`. Operator triggers admin sync to repopulate.

## Validator rejections (config-load)

- `pop + sync_strategy: on_open` — queue would never drain (`runSync` re-adds popped folders).
- `pop_and_move` target on a different filesystem from `root` — `os.Rename` is not atomic across filesystems.
- Old vocabulary (`release_to_back`, `release_to_head`, `delete`, `OnCommitDefault`, `OnGiveUpDefault`) — pre-v1 break-cleanly.

## Validator warnings

- `recycle + sync_strategy: on_drain` — legal but inert; the queue never empties so `on_drain` never fires.
