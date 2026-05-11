---
concept: named-lock
status: as-is
aliases: []
references:
  - _discover/2026-05-10-named-and-scope-locks-deterministic-order.md
  - _discover/2026-05-10-advisory-locks-tick-and-migrate.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
---

# Named lock

## What it is

A named lock is a producer-independent capacity-counter primitive. Declared in operator config (`named_locks:` block in `rimsky.yml`) with `mode: mutex | counting` and a capacity. The Go type is `locks.NamedLockSpec{Name}`; runtime rows are `lock_kind='named'` in `rimsky_claim_handle`.

## Purpose

Some constraints have nothing to do with producers — "at most N runs of this template concurrently" or "this whole job is a mutex" — and need a primitive that's deployment-scoped, not data-scoped. Named locks give templates a coarse capacity-counting tool that works without any producer.

## Boundaries

Owns: the per-name capacity declaration in YAML, the named-lock rows in `rimsky_claim_handle`, the rimsky-internal "increment / decrement" disposition at terminal. Does NOT own: scope conflicts (those live on `claim`), per-claim write-semantics (named locks don't have one). Adjacent: `claim`, `claim-handle`, `scope`, `advisory-lock`.

## Invariants

- `ClaimSpec` (for scope claims) and `NamedLockSpec` are distinct types — no common interface. Callers dispatch by type.
- Both primitives' acquisitions are walked in `(lock_kind, sort_key)` deterministic order to prevent the (N1-held, S1-wait) ⨯ (S1-held, N1-wait) deadlock (`@blessed-invariant 3`).
- Named-lock capacity counts come from `rimsky_worker_request.claimed_by IS NOT NULL` joined against `rimsky_claim_handle` rows (`@blessed-invariant 2`).

## Aliases and historical names

No live aliases. The CHECK constraint on `rimsky_claim_handle.lock_kind` enumerates `{'named','scope'}`.

## Open within this concept

(no specific tensions distinct from the broader `claim-handle` / `claim-producer` set)

