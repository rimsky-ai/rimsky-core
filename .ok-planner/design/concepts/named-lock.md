---
concept: named-lock
status: as-is
aliases: []
---

# Named lock

## What it is

A named lock is a producer-independent capacity-counter primitive. Declared in operator config (a `named_locks:` block) with `mode: mutex | counting` and a capacity. The named-lock spec carries just a name; at runtime it materializes as a `named`-kind row in the claim-handle ledger (see `concept:claim-handle`).

## Purpose

Some constraints have nothing to do with producers — "at most N runs of this template concurrently" or "this whole job is a mutex" — and need a primitive that's deployment-scoped, not data-scoped. Named locks give templates a coarse capacity-counting tool that works without any producer.

## Boundaries

Owns: the per-name capacity declaration in YAML, the named-lock rows in the claim-handle ledger, the rimsky-internal "increment / decrement" disposition at terminal. Does NOT own: scope conflicts (those live on `claim`), per-claim write-semantics (named locks don't have one). Adjacent: `claim`, `claim-handle`, `claim-scope`, `advisory-lock`.

## Invariants

- The claim spec (for scope claims) and the named-lock spec are distinct shapes with no common interface; callers dispatch by kind.
- Both primitives' acquisitions are walked in deterministic `(lock_kind, sort_key)` order to prevent the (N1-held, S1-wait) ⨯ (S1-held, N1-wait) deadlock (`@blessed-invariant 3`).
- Named-lock capacity counts come from active node-runs joined against their claim-handle rows (`@blessed-invariant 2`).

## Aliases and historical names

No live aliases. A CHECK constraint on the claim-handle lock-kind column enumerates `{'named','scope'}`.

## Open within this concept

(no specific tensions distinct from the broader `claim-handle` / `claim-producer` set)

## Notes

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

