---
concept: wait-set
status: as-is
aliases: []
---

# Wait-set

## What it is

The wait-set is a per-frame persisted ledger that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope, topic_filter)." Cascade walks insert rows when a sender transitions out of a settled state (pessimistic invalidate); the settled-state drain bulk-marks rows as drained when the sender resolves (fresh / failed / parked) by stamping a drain timestamp rather than deleting the row.

The wait-set drives dispatch eligibility: a stale node is dispatch-eligible iff no undrained rows exist for it in the current frame. Drained rows remain queryable for the substitution-context builder.

## Purpose

Derive dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics: cascade walks announce coupling at run-time; eligibility predicates query the ledger.

Idempotent re-fire handles the "filter didn't actually match" case: every cascade-walk match inserts a wait-set row regardless of filter compatibility; the settled-state drain releases the gate uniformly.

## Boundaries

Owns:
- The per-frame ledger schema and PK invariant.
- The insert-on-cascade-walk rule.
- The bulk-delete-on-settle rule.
- The eligibility predicate used by the ready-for-dispatch query.

Does NOT own:
- Subscription declaration (lives in `concept:node-subscription`).
- The cascade walk logic (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).

## Invariants

- Rows live only within their frame's scope (cascade-deleted with the owning frame per `concept:frame`).
- Drain stamps the drain timestamp on rows whose sender matches the settling sender. Drained rows remain queryable for the substitution-context builder. Eligibility predicate: a stale run is dispatch-eligible iff no undrained rows exist for it in the current frame.
- Bulk-drain on sender resolution covers every topic kind uniformly (idempotent re-fire when filter didn't actually match).
- The primary key `(frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)` ensures duplicate inserts within the same transaction collapse to a no-op. The drain-timestamp field is a non-PK lifecycle marker; the substitution-context builder reads the drained attribute rows for a receiver.

Drained rows are the durable record of "which senders contributed to this receiver's dispatch in this frame." The substitution-context builder queries them (filtered to attribute-topic rows, with sender state checked against settled-success outcomes) to populate the dependency map for `{{nodes.X.attribute.Y}}` directives. Cleanup happens via frame cascade-delete.

## Aliases and historical names

None. The pre-2026-05-14 model used a per-node declared-dependencies list for both cascade fan-out and eligibility gating; the wait-set replaces the eligibility role.

## Open within this concept

None at present.

## Notes

- 2026-05-14: concept introduced by `spec:2026-05-14-subscription-cascade-and-quality-of-life`. The wait-set ledger is added to the baseline schema; cascade walks insert rows + settled-state drain deletes them.
- 2026-05-20 — Mark-don't-delete on drain. New drain-timestamp field on the ledger; drain marks rather than deletes; eligibility predicate updates to "no undrained rows." The delete-by-sender accessor becomes a mark-drained-by-sender accessor. New accessor lists the drained attribute rows for a receiver, for the substitution-context builder. PK enumeration in this file corrected to the actual schema shape (`receiver_run_id`/`sender_run_id`, per-run identity since 2026-05-15). See `spec:2026-05-20-attribute-pull-resolution`.
- 2026-05-23 — Per spec:2026-05-23-signal-taxonomy-and-policy-decoupling: wait-set insertion is now gated by walk-time CEL filter evaluation (`concept:signal` payload predicate against subscriber `when:`). The pessimistic-invalidate rule (insert wait-set rows for every subscription edge regardless of filter compatibility) retires. Row shape and the drain-on-settled-state rule are unchanged; the `topic_kind` enum still accepts `state | attribute | event` (the new top-level signal kinds `terminal | transient | message` map to `state` for back-compat with the topic-kind constraint).
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
