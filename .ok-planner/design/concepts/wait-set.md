---
concept: wait-set
status: as-is
aliases: []
---

# Wait-set

## What it is

The wait-set is a per-frame persisted ledger that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope, topic_filter)." Cascade walks insert rows when a sender transitions out of a settled state (pessimistic invalidate); the settled-state drain bulk-marks rows as drained when the sender resolves (fresh / failed / parked) by stamping a drain timestamp rather than deleting the row.

The wait-set feeds dispatch eligibility, which is a two-condition predicate: a stale run is dispatch-eligible iff no undrained wait-set rows exist for it in the current frame AND no subscribed upstream has an in-flight run in the same frame. Drained rows remain queryable for the substitution-context builder.

## Purpose

Derive dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics: cascade walks announce coupling at run-time; eligibility predicates query the ledger.

Wait-set insertion is gated by walk-time CEL filter evaluation: a cascade-walk match inserts a wait-set row only when the subscriber's `when:` predicate evaluates true against the emitted signal; the settled-state drain releases the gate uniformly.

## Boundaries

Owns:
- The per-frame ledger schema and PK invariant.
- The insert-on-cascade-walk rule.
- The bulk-drain-on-settle rule (stamping a drain timestamp; rows are never deleted on settle).
- The eligibility predicate used by the ready-for-dispatch query.

Does NOT own:
- Subscription declaration (lives in `concept:node-subscription`).
- The cascade walk logic (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).

## Invariants

- Rows live only within their frame's scope (cascade-deleted with the owning frame per `concept:frame`).
- Drain stamps the drain timestamp on rows whose sender matches the settling sender. Drained rows remain queryable for the substitution-context builder. Eligibility predicate (two conditions): a stale run is dispatch-eligible iff no undrained wait-set rows exist for it in the current frame AND no subscribed upstream has an in-flight run in the same frame.
- A stale run is not dispatch-eligible while any subscribed upstream has an in-flight run in the same frame, regardless of which propagation path made the receiver stale; the eligibility predicate enforces this independent of wait-set seeding. Self-subscription is exempt: a node's own in-flight run never gates its own dispatch.
- Bulk-drain on sender resolution covers every topic kind uniformly.
- The primary key `(frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)` ensures duplicate inserts within the same transaction collapse to a no-op. The drain-timestamp field is a non-PK lifecycle marker; the substitution-context builder reads the drained attribute rows for a receiver.

Drained rows are the durable record of "which senders contributed to this receiver's dispatch in this frame." The substitution-context builder queries them (filtered to attribute-topic rows, with sender state checked against settled-success outcomes) to populate the dependency map for `{{nodes.X.attribute.Y}}` directives. Cleanup happens via frame cascade-delete.
