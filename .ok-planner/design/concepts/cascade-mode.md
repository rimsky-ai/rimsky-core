---
concept: cascade-mode
---

# Cascade mode

## What it is

A cascade mode is a per-node setting that governs how the gate evaluator's pending→stale transition (per `concept:wait-set`) treats a re-cascade targeting a receiver whose latest cascade-driven pending row, or recently-settled run, in the current `(receiver, run-scope)` already exists. Because RunScopes never span frames (per `concept:run-scope`), the `(receiver, run-scope)` key is always intra-frame — every mode operates purely inside the current frame; prior-frame runs are invisible to the mode rules. The mode selects from a closed enumeration of four behaviors — `most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled` — each reshaping what happens at the pending→stale transition: whether a prior cascade-driven stale is deleted, a new stale row is queued alongside it, or the transitioning row is dropped as a bag-equivalent duplicate. The per-sender-node accumulation rule the cascade walker applies before this point (per `concept:cascade`) is the same for every mode.

## Purpose

Different consumers want different coalescing semantics for cascades. A node whose downstream effect is overwriting — the latest value wins — should not spend work on stale intermediate cascades, and prefers `most-recent`. A node whose downstream effect is order-preserving — each cascade round delivers one logical event the receiver must observe — wants `sequenced` so every round dispatches. A node whose downstream work is expensive but deterministic on its inputs wants idempotency around byte-equivalent bags so it does not re-run unchanged work, and picks one of the idempotent modes depending on whether dedup should span only the queue or also include the most-recently-settled run.

## Boundaries

Owns: the per-mode rule applied at the gate evaluator's pending→stale transition — the coalesce, queue, or dedup action taken as a cascade-driven row transitions from pending to stale. Does NOT own: the per-sender-node accumulation rule the walker applies before this point (lives at `concept:cascade`, and is the same for every mode); the dispatcher's serialization gate (lives at `concept:node-run`); error-policy handling for failed cascades (lives at `concept:error-policy`). Adjacent: `concept:cascade`, `concept:node-run`, `concept:wait-set`, `concept:node-subscription`.

## The four modes

Every `(receiver, run-scope)` key in the rows below is intra-frame by construction (RunScopes never span frames). Prior-frame runs of the same node do not appear in any mode's lookup.

| Mode | Behavior at the gate evaluator's pending→stale transition |
|---|---|
| `most-recent` | Prior cascade-driven stale rows for the same `(receiver, run-scope)` in the current frame that have not yet been claimed by the dispatcher are deleted, then the transitioning row becomes the surviving stale. |
| `sequenced` | No deletion, no dedup: the transitioning row is never dropped in favor of a later round. The gate evaluator's advanced-sibling check (per `concept:node-run`'s at-most-one-past-pending invariant, enforced uniformly across every mode) still applies, so only one round is ever in `stale`/`running`/`held`/`parked` for the `(receiver, run-scope)` at a time — a queued round waits in `pending` until the previous round fully settles, then advances to `stale` in turn. What `sequenced` guarantees is that no round is ever dropped or coalesced: every queued round eventually dispatches, each with its own inputs, in sequence order. |
| `idempotent-queue` | The resolved bag is canonicalized to a stable byte form and compared against the most recent prior cascade row (pending or stale, whichever has the highest sequence) for this `(receiver, run-scope)` in the current frame. If byte-equivalent, the transitioning row is dropped instead of becoming stale. |
| `idempotent-settled` | Same comparison as `idempotent-queue` against the most recent prior cascade row. When no such prior cascade row exists, the resolved bag is instead compared against the most-recently-settled successful run for this `(receiver, run-scope)`. If byte-equivalent to either, the transitioning row is dropped. |

## Invariants

- The mode is per-node and selected at template registration. An unset mode defaults to `most-recent` per `decision:mode-default-most-recent`.
- The mode applies only to cascade-driven node-runs (those whose creation reason is `cascade` per `concept:node-run`'s creation-reason axis). Non-cascade rows — `operator_invalidate`, `recalculate`, `message_delivery` — are not subject to mode rules; they are created directly in state `stale` and dispatch on their own per `decision:non-cascade-direct-to-stale`.
- The idempotent modes' bag comparison is byte-equivalence after canonicalization to a stable form; semantically identical bags that differ only in JSON serialization (key order, whitespace, number formatting) are treated as duplicates.
- The mode does not change the per-sender-node accumulation rule (per `concept:cascade`) — it changes what happens after the walker has decided whether to accumulate or create a new pending, and what the gate evaluator does at pending→stale.
- Each of the sequenced and idempotent modes' behavior on the user-outcome surface has a dedicated story: `story:sequenced-preserves-cascade-rounds` and `story:idempotent-mode-dedupes` (covering both `idempotent-queue` and `idempotent-settled`). The `most-recent` mode's intra-frame coalesce is an implementation detail without its own user story; the operator-facing coalesce concern for a slow instance lives one layer up at `story:message-queue-coalesces-pending`.
- **All mode lookups are intra-frame.** No mode reads a prior-frame run to inform its decision. `idempotent-settled`'s "most-recently-settled successful run" is scoped by run-scope, and run-scope is per-frame, so the settled prior lives in the current frame or does not exist. The runtime does not fall back to a cross-frame or cross-scope prior — if none exists in scope, the row is not deduped.
