---
concept: wait-set
status: as-is
aliases: []
references:
  - ../../specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
---

# Wait-set

## What it is

The wait-set is a per-frame ledger (`table:rimsky_wait_set`) that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope, topic_filter)." Cascade walks insert rows when a sender transitions out of a settled state (pessimistic invalidate); the settled-state drain bulk-deletes rows when the sender resolves (fresh / failed / parked).

The wait-set drives dispatch eligibility: a stale node is dispatch-eligible iff its wait-set is empty in the current frame.

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
- Subscription declaration (lives in `concept:subscription`).
- The cascade walk logic (lives in `concept:cascade`).
- Frame lifecycle (lives in `concept:frame`).

## Invariants

- Rows live only within their `frame_id`'s scope (ON DELETE CASCADE from `rimsky_frames`).
- A stale receiver is eligible iff its wait-set is empty for the current frame.
- Bulk-delete on sender resolution covers every topic kind uniformly (idempotent re-fire when filter didn't actually match).
- The PK `(frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope)` ensures `ON CONFLICT DO NOTHING` collapses duplicate inserts within the same transaction.

## Aliases and historical names

None. The pre-2026-05-14 model used `nodes.dependencies` for both cascade fan-out and eligibility gating; the wait-set replaces the eligibility role.

## Open within this concept

None at present.

## Notes

- 2026-05-14: concept introduced by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The `rimsky_wait_set` table is added to the baseline migration; cascade walks insert rows + settled-state drain deletes them.
