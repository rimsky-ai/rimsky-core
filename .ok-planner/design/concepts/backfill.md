---
concept: backfill
status: as-is
aliases: []
---

# Backfill

## Definition

A backfill is one invalidate-kind message with a `partition_request_override` payload field, targeting a fan-out node. The target's template uses substitution on its fan-out partition-request field to accept the override: the field is authored as a substitution that pulls the override out of the triggering message's payload, falling back to a template-declared default. The default clause runs for non-backfill invocations; the substitution-override runs when a backfill message provides one.

## Boundaries

Owns: the backfill-message pattern, the control-api backfill endpoints (create, list, show, partitions, cancel), the matching CLI backfill subcommands, the lineage chain via the backfill-operation key. Does NOT own: the fan-out mechanics (see `concept:fan-out`), the message envelope (see `concept:message`), cancellation enforcement at the in-flight frame level (V1 only blocks future-enqueued work; in-flight frames complete normally). Adjacent: `concept:message`, `concept:fan-out`, `concept:lineage`.

## Invariants

- A backfill is structurally a message with `kind: invalidate` + payload `{partition_request_override, backfill_operation_id, reason}`. Rimsky validates that the target node has `fan_out.partition_request` referencing trigger substitution (warning if not).
- The backfill-operation key on the message row is the chain key — multi-fire backfills share the same operation id; lineage walks chain back via it.
- V1 cancellation: the cancel endpoint marks the operation cancelled. Pending undelivered messages are skipped (a cancelled filter on the pending-delivery path); in-flight frames complete normally (no preemption).
- Status rollup: the single-backfill fetch endpoint queries the message ledger and the node-run ledger for the originating message + the parent fan-out run + its children's aggregated states.

## Operator surface

The control-api offers five backfill operations on an instance: create a backfill (targeting a node, with a partition-request override and a reason), list recent backfills, fetch a single backfill (message + frame + parent run + children states), list its per-child partition states (one row per partition processed), and cancel an operation. The CLI exposes the same five as create/list/show/cancel subcommands, with create taking an instance, target node, range, and reason.

## Notes

Introduced by spec:2026-05-15-data-platform-extensions-design. The "backfill is just an invalidate-message with a payload" design keeps the dispatch machinery uniform — backfills go through the same message queue and the same frame-delivery path as operator-API invalidates and publisher-origin messages.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
