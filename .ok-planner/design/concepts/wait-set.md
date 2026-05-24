---
concept: wait-set
status: as-is
aliases: []
references:
  - ../../specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
---

# Wait-set

## What it is

The wait-set is a per-frame ledger (`table:rimsky_wait_set`) that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope, topic_filter)." Cascade walks insert rows when a sender transitions out of a settled state (pessimistic invalidate); the settled-state drain bulk-marks rows as drained when the sender resolves (fresh / failed / parked) by setting `drained_at = NOW()` rather than deleting the row.

The wait-set drives dispatch eligibility: a stale node is dispatch-eligible iff no `drained_at IS NULL` rows exist for it in the current frame. Drained rows remain queryable for the substitution-context builder.

## Purpose

Derive dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics: cascade walks announce coupling at run-time; eligibility predicates query the ledger.

Idempotent re-fire handles the "filter didn't actually match" case: every cascade-walk match inserts a wait-set row regardless of filter compatibility; the settled-state drain releases the gate uniformly.

## Boundaries

Owns:
- The per-frame ledger schema and PK invariant.
- The insert-on-cascade-walk rule.
- The bulk-delete-on-settle rule.
- The eligibility predicate used by `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch`.

Does NOT own:
- Subscription declaration (lives in `concept:node-subscription`).
- The cascade walk logic (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).

## Invariants

- Rows live only within their `frame_id`'s scope (ON DELETE CASCADE from `rimsky_frames`).
- Drain marks `drained_at = NOW()` on rows where `sender_run_id` matches the settling sender. Drained rows remain queryable for the substitution-context builder. Eligibility predicate: a stale run is dispatch-eligible iff no `drained_at IS NULL` rows exist for it in the current frame.
- Bulk-drain on sender resolution covers every topic kind uniformly (idempotent re-fire when filter didn't actually match).
- The PK `(frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)` ensures `ON CONFLICT DO NOTHING` collapses duplicate inserts within the same transaction. The `drained_at TIMESTAMPTZ NULL` column is a non-PK lifecycle marker; the substitution-context builder reads it via `ListDrainedAttributeRowsForReceiver`.

Drained rows are the durable record of "which senders contributed to this receiver's dispatch in this frame." The substitution-context builder queries them (filtered to `topic_kind='attribute'`, with sender state checked against settled-success outcomes) to populate the `Deps` map for `{{nodes.X.attribute.Y}}` directives. Cleanup happens via frame cascade-delete.

## Aliases and historical names

None. The pre-2026-05-14 model used `nodes.dependencies` for both cascade fan-out and eligibility gating; the wait-set replaces the eligibility role.

## Open within this concept

None at present.

## Notes

- 2026-05-14: concept introduced by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The `rimsky_wait_set` table is added to the baseline migration; cascade walks insert rows + settled-state drain deletes them.
- 2026-05-20 — Mark-don't-delete on drain. New `drained_at TIMESTAMPTZ` column on `rimsky_wait_set`; drain marks rather than deletes; eligibility predicate updates to "no undrained rows." `DeleteBySender` renamed to `MarkDrainedBySender`. New `ListDrainedAttributeRowsForReceiver` accessor for the substitution-context builder. PK enumeration in this file corrected to the actual schema shape (`receiver_run_id`/`sender_run_id`, per-run identity since 2026-05-15). See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
- 2026-05-23 — Per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`: wait-set insertion is now gated by walk-time CEL filter evaluation (`concept:signal` payload predicate against subscriber `when:`). The pessimistic-invalidate rule (insert wait-set rows for every subscription edge regardless of filter compatibility) retires. Row shape and the drain-on-settled-state rule are unchanged; the `topic_kind` enum still accepts `state | attribute | event` (the new top-level signal kinds `terminal | transient | message` map to `state` for back-compat with the CHECK constraint).
