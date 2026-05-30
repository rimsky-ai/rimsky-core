---
concept: backfill
status: as-is
aliases: []
---

# Backfill

## Definition

A backfill is one invalidate-kind message with a `partition_request_override` payload field, targeting a fan-out node. The target's template uses substitution on its fan-out partition-request field to accept the override: the field is authored as a substitution that pulls the override out of the triggering message's payload, falling back to a template-declared default. The fallback runs for non-backfill invocations; the substitution-override runs when a backfill message provides one.

The override genuinely reaches the node. Fan-out acquisition resolves the node's `partition_request` through the substitution engine at acquisition time, binding the triggering message's payload (see `concept:fan-out`). Previously the template's `partition_request` was passed verbatim, un-substituted, so the override was silently ignored and the fallback always fired — every backfill processed the template default regardless of the operator's override. That is fixed: the override now drives the partitions actually processed.

## Boundaries

Owns: the backfill-message pattern, the control-api backfill endpoints (create, list, show, partitions, cancel), the matching CLI backfill subcommands, the lineage chain via the backfill-operation key. Does NOT own: the fan-out mechanics (see `concept:fan-out`), the message envelope (see `concept:message`), cancellation enforcement at the in-flight frame level (V1 only blocks future-enqueued work; in-flight frames complete normally). Adjacent: `concept:message`, `concept:fan-out`, `concept:lineage`.

## Invariants

- A backfill is structurally a message with `kind: invalidate` + payload `{partition_request_override, backfill_operation_id, reason}`. `backfill:create` **rejects (400)** a target that is not a fan-out node wired to accept the partition override (its `fan_out.partition_request` must reference the trigger substitution). A backfill is meaningless without a partition — and thus a fan-out — so a non-fan-out / unwired target is refused at submit with a message naming the requirement, rather than silently degrading to a plain invalidate. The dry-run preview projects the same validation (a bad target fails the same way).
- The backfill-operation key on the message row is the chain key — multi-fire backfills share the same operation id; lineage walks chain back via it.
- V1 cancellation: the cancel endpoint marks the operation cancelled. Pending undelivered messages are skipped (a cancelled filter on the pending-delivery path); in-flight frames complete normally (no preemption).
- Status rollup: the single-backfill fetch endpoint queries the message ledger and the node-run ledger for the originating message + the parent fan-out run + its children's aggregated states.

## Operator surface

The control-api offers five backfill operations on an instance: create a backfill (targeting a node, with a partition-request override and a reason), list recent backfills, fetch a single backfill (message + frame + parent run + children states), list its per-child partition states (one row per partition processed), and cancel an operation. The CLI exposes the same five as create/list/show/cancel subcommands, with create taking an instance, target node, range, and reason.

## Notes

Introduced by spec:2026-05-15-data-platform-extensions-design. The "backfill is just an invalidate-message with a payload" design keeps the dispatch machinery uniform — backfills go through the same message queue and the same frame-delivery path as operator-API invalidates and publisher-origin messages.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`: the target-validation invariant tightens from "warn if not a wired fan-out target" to "`backfill:create` **rejects (400)**." And the override now actually functions — fan-out acquisition substitutes the node's `partition_request` from the triggering message at acquisition (previously it passed the template's `partition_request` verbatim, so the override was silently ignored and the fallback always fired). See `concept:fan-out`, `concept:message` (delivery default flips to `serial_queue`).
