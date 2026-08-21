---
concept: cascade-mode
---

# Cascade mode

## What it is

A cascade mode is a per-node setting that governs how the gate evaluator treats a re-cascade aimed at a receiver that already has a cascade-driven round queued, or recently settled, under the same run scope (see `concept:wait-set`). A run scope never spans frames (see `concept:run-scope`), so every mode works inside the current frame alone: no mode reads a run from another frame, and a receiver's runs in an earlier frame are invisible to the rule. The mode is one of four — most-recent, sequenced, idempotent-queue, idempotent-settled — and each reshapes what the gate evaluator does as a cascade-driven round moves from pending to stale, the point at which the round becomes claimable by the dispatcher. Under most-recent, the arriving round deletes the earlier rounds the dispatcher has not yet claimed and becomes the round that dispatches. Under sequenced, a receiver's rounds from one sender dispatch in arrival order: a later round never dispatches while an older round of the same sender is still queued. Under either idempotent mode, the arriving round is dropped when its resolved attribute bag matches a bag the receiver has already been handed; the two differ in what they compare against, one reading the queued rounds alone and the other also the most recently settled run.

## Purpose

A cascade mode lets each node choose the coalescing its downstream work needs. A node whose effect is overwriting, where the latest value wins, spends no work on intermediate cascades. A node whose effect is order-preserving, where each round carries one logical event the receiver must observe, sees every round dispatch. A node whose work is expensive and deterministic in its inputs re-runs nothing on an unchanged bag.

## Boundaries

A cascade mode owns one rule: the coalesce, queue, or drop the gate evaluator applies as a cascade-driven round moves from pending to stale. It changes nothing before that point. The per-sender-node accumulation rule the cascade walker applies while the round is still being assembled belongs to `concept:cascade` and is the same under every mode. The dispatcher's serialization gate belongs to `concept:node-run`. Handling a failed cascade belongs to `concept:error-policy`.

What a user can count on from the sequenced and the idempotent modes lives in `story:sequenced-preserves-cascade-rounds` and `story:idempotent-mode-dedupes`; the coalescing an operator sees on a slow instance lives in `story:message-queue-coalesces-pending`.

see also: `cascade`, `node-run`, `wait-set`, `node-subscription`, `run-scope`, `error-policy`
