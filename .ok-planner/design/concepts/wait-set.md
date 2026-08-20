---
concept: wait-set
---

# Wait-set

## What it is

The wait-set is a per-frame persisted ledger that records "cascade-driven pending receiver R is waiting for sender S in frame F" keyed by frame, receiver run, sender run, and topic kind. Cascade walks insert rows under the per-sender-node accumulation rule (see `concept:cascade`): the wait-set row is inserted on the latest cascade-driven pending receiver run, or on a freshly created pending if the latest pending's wait-set already covers the sender's node. The settled-state drain bulk-marks rows as drained when the sender reaches a settled outcome (per `concept:node-run`) by stamping a drain timestamp rather than deleting the row.

**The gate evaluator, not the dispatcher, decides upstream dependency.** When a sender settles, the drain stamps the drain timestamp on matching rows and triggers the gate evaluator for each affected cascade-driven pending receiver. The gate evaluator checks: are all of this pending's wait-set rows drained, AND no subscribed upstream has an in-flight run in the same frame, AND no other run for this same `(node, run-scope)` is already past pending (`stale`/`running`/`held`/`parked` — the advanced-sibling check that enforces `concept:node-run`'s at-most-one-past-pending invariant uniformly across every `concept:cascade-mode` value)? If all three gates clear, the gate evaluator builds the receiver's resolved attribute bag — substitution resolving each round-driving sender from the settled run pinned by this pending's wait-set rows, and every other subscribed sender from its most-recent fresh-settled attribute store, both **in the current frame** (the sender-run rows created inside this frame; prior-frame sender-runs are invisible) — plus the receiver's own carry-forward from the predecessor run **in the current frame's RunScope** by `sequence` (per `concept:attribute`) — persists it on the run's attribute bag, applies the per-node cascade-mode rule, and transitions the row `pending → stale`. The dispatcher then claims `stale` rows by `sequence`, gated by the serialization constraint ("no same-(node, run-scope) run in `{running, held, parked}`") and by a second check that the row carries no undrained wait-set row. The gate evaluator is the authoritative upstream-dependency gate; the dispatcher's check is defence in depth against a stale row reaching the queue ahead of its own drain.

## Purpose

Derive dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics: cascade walks announce coupling, and the gate evaluator settles upstream dependency before a row becomes claimable, so the dispatcher's claim adds only the serialization gate and a defence-in-depth re-check of drain.

Wait-set insertion is gated by walk-time CEL filter evaluation: a cascade-walk match inserts a wait-set row only when the subscriber's `when:` predicate evaluates true against the emitted signal; the settled-state drain releases the gate uniformly.

## Multiple pendings per (receiver, run-scope, frame)

Under `concept:cascade`'s per-sender-node accumulation rule, multiple cascade-driven pending node-runs can coexist for the same (receiver, run-scope, frame). Each pending Ri carries its OWN wait-set rows for the cascade events that contributed to it; there is no row sharing across runs. Drain operates per row (the drain query joins on sender_run, which is per-pending). As each Ri's wait-set drains and its gates clear, the gate evaluator transitions Ri to `stale` independently. The dispatcher then claims them in `sequence` order via the serialization gate.

## Boundaries

Owns:
- The per-frame ledger schema and PK invariant.
- The three insertion sites. Two key on the receiver_run (latest cascade-driven pending under the accumulation rule, or a newly-created pending if the per-sender-node check forces a new one — see `concept:cascade`): the subscriber cascade walk, and the force-upstream-refresh pull that walk triggers. The third is sub-graph entry-alias binding at child dispatch, which keys its row on the just-created delegated child's run and stamps it drained in the same transaction.
- The bulk-drain-on-settle rule (stamping a drain timestamp; rows are never deleted on settle).
- The gate evaluator that runs at drain time: build bag, persist, apply the per-node cascade-mode rule, transition pending→stale.

Does NOT own:
- Subscription declaration (lives in `concept:node-subscription`).
- The cascade walk logic and accumulation rule (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).
- The dispatcher's serialization gate ("no same-(node, run-scope) run in `{running, held, parked}`") and its defence-in-depth drain re-check; both live at the dispatcher claim site.

## Invariants

- Rows live only within their frame's scope (cascade-deleted with the owning frame per `concept:frame`).
- Drain stamps the drain timestamp on rows whose sender matches the settling sender. Drained rows are not deleted on settle; they remain queryable as a forensic record.
- **The gate evaluator's pending→stale predicate has three conjuncts.** A cascade-driven pending Ri transitions iff (a) all of Ri's wait-set rows are drained, (b) no subscribed upstream of Ri's receiver-node has an in-flight run in the same frame (in-flight = state in `{pending, stale, running, held, parked}`), except a `held` upstream that shares subgraph co-membership with the receiver — co-members must not gate each other's dispatch (per `decision:held-as-state-not-phase`) — and (c) no other run for Ri's own `(node, run-scope)` is already past pending (state in `{stale, running, held, parked}`) — the advanced-sibling check that enforces `concept:node-run`'s at-most-one-past-pending invariant uniformly across every `concept:cascade-mode` value, including `sequenced`. Self-subscription is exempt from conjunct (b) only: a node's own in-flight run never gates its own pending→stale via the upstream clause; conjunct (c) still serializes the node's own rounds unconditionally.
- Bulk-drain on sender resolution covers every topic kind uniformly.
- **The dispatcher re-checks drain; the gate evaluator is authoritative.** Candidate selection admits a `stale` row only when none of its wait-set rows is undrained, in both persistence backends. The gate evaluator already decides upstream dependency, so this check is defence in depth. The wait-set primary key covers the lookup. The check holds even if a `pending → stale` transition and a sender's drain stamp — separate transactional steps — ever interleave so that a row reaches `stale` before its own drain commits.
- The primary key over frame, receiver_run, sender_run, and topic-kind ensures duplicate inserts within the same transaction collapse to a no-op. The drain-timestamp field is a non-PK lifecycle marker.
- Non-cascade rows (`creation_reason ∈ {operator_invalidate, recalculate, message_delivery}`) have NO wait-set rows. They are created directly in state `stale` per `decision:non-cascade-direct-to-stale`; the wait-set / gate-evaluator path applies only to cascade-driven creation.

Drained rows are the durable record of "which senders woke this receiver in this frame" — a wake-causality trace carrying no signal payload and no attribute data. The sender-run identity pinned on those rows is nonetheless load-bearing for substitution: the gate evaluator resolves each round-driving sender's value from the persisted attribute store of the run its wait-set row pins — so each cascade round's dispatch sees the inputs of its own moment (the `sequenced` cascade-mode promise, per `story:sequenced-preserves-cascade-rounds`) — and falls back to the subscribed sender's most-recent fresh-settled attribute store **in the current frame's RunScope tree** (per `concept:attribute`) for subscribed senders that did not drive the round. Sender-run rows produced in earlier frames are never consulted; they exist for audit forensics only. Cleanup happens via frame cascade-delete.
