---
concept: named-lock
---

# Named lock

## What it is

A named lock is a producer-independent capacity-counter primitive. Declared in operator config with a single capacity-limit value per name; a limit of one is a mutex, a limit greater than one a counting semaphore; the mode is inferred from the limit, not declared separately. The named-lock spec carries just a name; at runtime it materializes as a named-kind row in the claim-handle ledger (see `concept:claim-handle`).

## Purpose

Some constraints have nothing to do with producers — "at most N runs of this template concurrently" or "this whole job is a mutex" — and need a primitive that's deployment-scoped, not data-scoped. Named locks give templates a coarse capacity-counting tool that works without any producer.

## Boundaries

Owns: the per-name capacity declaration in config, the named-lock rows in the claim-handle ledger, the rimsky-internal counter disposition at terminal. Does NOT own: scope conflicts (those live on `claim`), per-claim write-semantics (named locks don't have one). Adjacent: `claim`, `claim-handle`, `claim-scope`, `advisory-lock`.

## Invariants

- The claim spec (for scope claims) and the named-lock spec are distinct shapes with no common interface; callers dispatch by kind.
- Both primitives' acquisitions are walked in deterministic `(lock_kind, sort_key)` order to prevent the (N1-held, S1-wait) ⨯ (S1-held, N1-wait) deadlock (invariant 3).
- Named-lock capacity counts are a single-table count of the named lock's own claim-handle rows in the active state; committed and abandoned rows are no longer held and do not count (invariant 2).
- Capacity-counting correctness depends on `concept:advisory-lock`'s per-name in-transaction serialization: the count-then-insert is race-free only because concurrent acquisitions of the same name serialize on that lock. Weakening the advisory-lock serialization breaks this invariant.
- A template's lock-name reference may embed substitution directives: the declared text resolves through the standard substitution grammar at acquisition time, alongside claim-selector resolution (see `concept:claim-scope`), and the capacity lookup uses the resolved name. Directive syntax in lock names is validated at registration; a literal (directive-free) lock name must match an operator-declared lock or registration fails (per `concept:template`'s unconditional reference validation). A lock-name substitution failure fails that acquisition and routes through the node's substitution-failure error policy (see `decision:substitution-failure-routes-with-substitution`).
