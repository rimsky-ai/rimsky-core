---
concept: claim-producer-pg-store
definition: |
  The bundled reference claim-producer that treats rows in an operator-owned items table as the resource each claim acquires. Implements scope-conflict and items-table queue semantics store-internally.
proto_symbol: ClaimProducer in protocols/proto/v1/claim_producer.proto
config_field: rimsky.yml:claim_producers
api_surface: (none)
related: [claim-producer, claim, scope, write-semantics]
deprecated_terms: [postgres-store-bundle]
---

# Postgres store (`stores/postgres/`)

The bundled postgres store is a reference [claim producer](claim-producer.md) that treats rows in an operator-owned items table as the resource each [claim](claim.md) acquires.

## Selectors

Two selector forms:

- **Scope selector** — selector echoed verbatim as both address and scope; no items table involvement.
- **Pick-policy selector** — e.g. `@review-queue`. Resolves via a configured `pick_policy` to a row in the configured items table via `FOR UPDATE SKIP LOCKED`.

## Items table schema

Each pick policy points at an operator-managed table. Required columns:

| Column | Type |
|---|---|
| `item_id` | `text` |
| `payload` | `jsonb` |
| `state` | `text` |
| `claim_token` | `text` |
| `claimed_at` | `timestamp with time zone` |
| `enqueued_at` | `timestamp with time zone` |
| `priority` | `integer` |
| `sequence` | `bigint` |

The store verifies the schema at startup; missing columns or wrong types fail config-load.

## Pick-policy actions (v2)

Each pick policy declares two action fields. Both are required.

| Field | Fires on |
|---|---|
| `on_commit` | `Commit` (success path) |
| `on_give_up` | `Abandon` (failure path) |

The pg-store supports a subset of the v2 vocabulary because the items-table row IS the resource — there's no separate folder concept.

| Action | Supported | Effect |
|---|---|---|
| `pop` | yes | row deleted |
| `recycle` | yes | row returns to queue tail (claim_token cleared, sequence re-bumped) |
| `pop_and_move` | rejected at config-load | no folder concept |
| `pop_and_delete` | rejected at config-load | semantically equivalent to `pop`; the redundant name would mislead |

YAML shape:

```yaml
pick_policies:
  "@review-queue":
    items_table: items_inbound
    on_commit: pop
    on_give_up: recycle
    visibility_timeout_seconds: 300
```

## Common patterns

- **Queue-mode (one-shot ingest):** `pop + recycle`. Each item processed once; failures recycle to the tail for retry.
- **Ring-mode (forever-cycling):** `recycle + recycle`. Items rotate indefinitely.

## Validator rejections (config-load)

- `pop_and_move` action — not supported by postgres store.
- `pop_and_delete` action — not supported (use `pop`).
- Old vocabulary (`release_to_back`, `release_to_head`, `delete`, `OnCommitDefault`, `OnGiveUpDefault`) — pre-v1 break-cleanly.
- Items-table identifier with mixed case, hyphens, or other non-`[a-z_][a-z0-9_]*` characters.

## No `sync_strategy`

The pg-store has no analogous mechanism — the items table is the source of truth and no auto-discovery is involved. Operators populate the table externally (the store's admin endpoint exposes a bulk-insert API).
