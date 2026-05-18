---
concept: backfill
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Backfill

## Definition

A backfill is one invalidate-kind message with a `partition_request_override` payload field, targeting a fan-out node. The target's template uses substitution on its `fan_out.partition_request` field to accept the override:

```yaml
fan_out:
  claim: parcels
  partition_request: "{{trigger.message.payload.partition_request_override | default: <default-from-template>}}"
```

The default clause runs for non-backfill invocations; the substitution-override runs when a backfill message provides one.

## Boundaries

Owns: the backfill-message pattern, the control-api `/instances/{id}/backfills` endpoints (create, list, show, partitions, cancel), the CLI subcommands (`rimsky-cli backfill create/list/show/cancel`), the lineage chain via `backfill_operation_id`. Does NOT own: the fan-out mechanics (see `concept:fan-out`), the message envelope (see `concept:message`), cancellation enforcement at the in-flight frame level (V1 only blocks future-enqueued work; in-flight frames complete normally). Adjacent: `concept:message`, `concept:fan-out`, `concept:lineage`.

## Invariants

- A backfill is structurally a message with `kind: invalidate` + payload `{partition_request_override, backfill_operation_id, reason}`. Rimsky validates that the target node has `fan_out.partition_request` referencing trigger substitution (warning if not).
- `rimsky_messages.backfill_operation_id` is the chain key — multi-fire backfills share the same operation_id; lineage walks chain back via it.
- V1 cancellation: `POST /backfills/{op_id}/cancel` marks the operation cancelled. Pending undelivered messages are skipped (`cancelled = TRUE` filter in `DeliverPendingMessages`); in-flight frames complete normally (no preemption).
- Status rollup: `GET /backfills/{op_id}` queries `rimsky_messages` + `rimsky_node_runs` for the originating message + the parent fan-out run + its children's aggregated states.

## Control-api

- `POST /instances/{id}/backfills` — body `{target_node, partition_request_override, reason}`.
- `GET /instances/{id}/backfills` — list recent backfills.
- `GET /backfills/{op_id}` — single backfill: message + frame + parent run + children states.
- `GET /backfills/{op_id}/partitions` — per-child run state (one row per partition processed).
- `POST /backfills/{op_id}/cancel` — mark cancelled.

## CLI

```sh
rimsky-cli backfill create --instance X --node parcels --range 2024-01-01..2024-09-30 --reason "..."
rimsky-cli backfill list --instance X
rimsky-cli backfill show <op-id>
rimsky-cli backfill cancel <op-id>
```

## Annotation sites

- `code:control/controlapi/backfills.go` — route handlers.
- `code:control/cli/backfill.go` — CLI subcommand group.
- `code:test/scenarios/backfill/` — partition-selector override, status rollup, cancellation, lineage chain.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The "backfill is just an invalidate-message with a payload" design keeps the dispatch machinery uniform — backfills go through the same `rimsky_messages` queue and the same frame-delivery path as operator-API invalidates and publisher-origin messages.
