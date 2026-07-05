---
concept: wait-set
status: as-is
aliases: []
---

# Wait-set

## What it is

The wait-set is a per-frame persisted ledger that records "cascade-driven pending receiver R is waiting for sender S in frame F" keyed by frame, receiver run, sender run, topic kind, and subscription scope. Cascade walks insert rows under the per-sender-node accumulation rule (see `concept:cascade`): the wait-set row is inserted on the latest cascade-driven pending receiver run, or on a freshly created pending if the latest pending's wait-set already covers the sender's node. The settled-state drain bulk-marks rows as drained when the sender reaches a settled outcome (per `concept:node-run`) by stamping a drain timestamp rather than deleting the row.

**The wait-set feeds the gate evaluator, not the dispatcher.** When a sender settles, the drain stamps the drain timestamp on matching rows and triggers the gate evaluator for each affected cascade-driven pending receiver. The gate evaluator checks: are all of this pending's wait-set rows drained, AND no subscribed upstream has an in-flight run in the same frame? If both gates clear, the gate evaluator builds the receiver's resolved attribute bag — substitution against each subscribed sender's most-recent fresh-settled attribute store **in the current frame** (the sender-run rows created inside this frame; prior-frame sender-runs are invisible) plus the receiver's own carry-forward from the predecessor run **in the current frame's RunScope** by `sequence` (per `concept:attribute`) — persists it on the run's attribute bag, applies the per-template cascade-mode rule, and transitions the row `pending → stale`. The dispatcher then claims `stale` rows by `sequence`, gated only by the serialization constraint ("no same-(node, run-scope) run in `{running, held, parked}`"). The upstream-dependency predicate no longer lives at dispatch time; it is fully resolved by the row being in `stale` at all.

## Purpose

Derive dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics: cascade walks announce coupling at gate-evaluator time; the dispatcher's claim only checks the serialization gate.

Wait-set insertion is gated by walk-time CEL filter evaluation: a cascade-walk match inserts a wait-set row only when the subscriber's `when:` predicate evaluates true against the emitted signal; the settled-state drain releases the gate uniformly.

## Multiple pendings per (receiver, run-scope, frame)

Under `concept:cascade`'s per-sender-node accumulation rule, multiple cascade-driven pending node-runs can coexist for the same (receiver, run-scope, frame). Each pending Ri carries its OWN wait-set rows for the cascade events that contributed to it; there is no row sharing across runs. Drain operates per row (the drain query joins on sender_run, which is per-pending). As each Ri's wait-set drains and its gates clear, the gate evaluator transitions Ri to `stale` independently. The dispatcher then claims them in `sequence` order via the serialization gate.

## Boundaries

Owns:
- The per-frame ledger schema and PK invariant.
- The single insertion path: on cascade-walk, keyed by the receiver_run (latest cascade-driven pending under accumulation rule, or a newly-created pending if the per-sender-node check forces a new one — see `concept:cascade`).
- The bulk-drain-on-settle rule (stamping a drain timestamp; rows are never deleted on settle).
- The gate evaluator that runs at drain time: build bag, persist, apply the per-template cascade-mode rule, transition pending→stale.

Does NOT own:
- Subscription declaration (lives in `concept:node-subscription`).
- The cascade walk logic and accumulation rule (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).
- The dispatcher's serialization gate ("no same-(node, run-scope) run in `{running, held, parked}`"); that lives at the dispatcher claim site.

## Invariants

- Rows live only within their frame's scope (cascade-deleted with the owning frame per `concept:frame`).
- Drain stamps the drain timestamp on rows whose sender matches the settling sender. Drained rows are not deleted on settle; they remain queryable as a forensic record.
- The gate evaluator's pending→stale predicate: a cascade-driven pending Ri transitions iff all of Ri's wait-set rows are drained AND no subscribed upstream of Ri's receiver-node has an in-flight run in the same frame (in-flight = state in `{pending, stale, running, held, parked}`). Self-subscription is exempt: a node's own in-flight run never gates its own pending→stale.
- Bulk-drain on sender resolution covers every topic kind uniformly.
- The primary key over frame, receiver_run, sender_run, topic-kind, and scope ensures duplicate inserts within the same transaction collapse to a no-op. The drain-timestamp field is a non-PK lifecycle marker.
- Non-cascade rows (`creation_reason ∈ {operator_invalidate, recalculate, message_delivery}`) have NO wait-set rows. They are created directly in state `stale` per `decision:non-cascade-direct-to-stale`; the wait-set / gate-evaluator path applies only to cascade-driven creation.

Drained rows are the durable record of "which senders woke this receiver in this frame" — a wake-causality trace, not a data carrier. The gate evaluator builds the receiver's substitution input by querying each subscribed sender's most-recent fresh-settled attribute store **in the current frame's RunScope tree** (per `concept:attribute`), independent of which wait-set rows drained. Sender-run rows produced in earlier frames are never consulted; they exist for audit forensics only. Cleanup happens via frame cascade-delete.
