---
concept: cascade-mode
status: as-is
aliases: []
---

# Cascade mode

## What it is

A cascade mode is a per-node setting that governs how the cascade walker (per `concept:cascade`) treats a re-cascade targeting a receiver whose latest cascade-driven pending row, or recently-settled run, for this `(receiver, run-scope, frame)` already exists. The mode selects from a closed enumeration of four behaviors — `most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled` — each reshaping how the walker's accumulate-or-queue gate (the per-sender-node accumulation rule in `concept:cascade`) interacts with subsequent rounds, and how the gate evaluator's pending→stale transition (per `concept:wait-set`) treats bag-equivalent successors.

## Purpose

Different consumers want different coalescing semantics for cascades. A node whose downstream effect is overwriting — the latest value wins — should not spend work on stale intermediate cascades, and prefers `most-recent`. A node whose downstream effect is order-preserving — each cascade round delivers one logical event the receiver must observe — wants `sequenced` so every round dispatches. A node whose downstream work is expensive but deterministic on its inputs wants idempotency around byte-equivalent bags so it does not re-run unchanged work, and picks one of the idempotent modes depending on whether dedup should span only the queue or also include the most-recently-settled run.

## Boundaries

Owns: the per-mode rule applied at the walker's accumulate-or-queue gate, and the per-mode dedup rule applied at the gate evaluator's pending→stale transition. Does NOT own: the per-sender-node accumulation rule itself (lives at `concept:cascade`); the dispatcher's serialization gate (lives at `concept:node-run`); error-policy handling for failed cascades (lives at `concept:error-policy`). Adjacent: `concept:cascade`, `concept:node-run`, `concept:wait-set`, `concept:node-subscription`.

## The four modes

| Mode | Walker behavior on re-cascade | Gate-eval behavior |
|---|---|---|
| `most-recent` | The newest cascade-driven pending wins. Prior cascade-driven stale rows for the same `(receiver, run-scope)` that have not yet been claimed by the dispatcher are deleted before the new pending transitions to stale. | No additional dedup; the surviving stale transitions on its own wait-set draining. |
| `sequenced` | Each cascade round creates its own pending; prior pendings are preserved and dispatch in sequence order. No coalescing across rounds. | No dedup; every queued round dispatches. |
| `idempotent-queue` | The new cascade accumulates per the standard walker rule. | At pending→stale, the resolved bag is canonicalized to a stable byte form and compared against the currently-queued stale row for this `(receiver, run-scope)`. If byte-equivalent, the new row is dropped. |
| `idempotent-settled` | Same as `idempotent-queue`. | At pending→stale, the resolved bag is compared against both the currently-queued stale and the most-recently-settled successful run for this `(receiver, run-scope)`. If byte-equivalent to either, the new row is dropped. |

## Invariants

- The mode is per-node and selected at template registration. An unset mode defaults to `most-recent` per `decision:mode-default-most-recent`.
- The mode applies only to cascade-driven node-runs (those whose creation reason is `cascade` per `concept:node-run`'s creation-reason axis). Non-cascade rows — `operator_invalidate`, `recalculate`, `message_delivery` — are not subject to mode rules; they are created directly in state `stale` and dispatch on their own per `decision:non-cascade-direct-to-stale`.
- The idempotent modes' bag comparison is byte-equivalence after canonicalization to a stable form; semantically identical bags that differ only in JSON serialization (key order, whitespace, number formatting) are treated as duplicates.
- The mode does not change the per-sender-node accumulation rule (per `concept:cascade`) — it changes what happens after the walker has decided whether to accumulate or create a new pending, and what the gate evaluator does at pending→stale.
- Each mode's behavior on the user-outcome surface has a dedicated story: `story:most-recent-coalesces-cascades`, `story:sequenced-preserves-cascade-rounds`, and `story:idempotent-mode-dedupes` (covering both `idempotent-queue` and `idempotent-settled`).
