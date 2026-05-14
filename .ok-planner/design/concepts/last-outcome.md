---
concept: last-outcome
status: as-is
aliases: []
references:
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
---

# Last outcome

## What it is

`last_outcome` is a TEXT column on `rimsky_nodes` carrying one of `{fresh_changed, fresh_unchanged, passed, pure_cascade, failed}`. Written by the supervisor's terminal-handler resolution alongside the state transition. Separate from `state`.

## Purpose

Splits "did this node change in a way that propagates" out of `state`. The cascade-firing gate reads `last_outcome == fresh_changed`; the dispatch-eligibility gate reads `state`. Keeping them in different columns keeps each predicate one-column-simple.

## Boundaries

Owns: the cascade-firing gate, the audit-readable "what happened" string. Does NOT own: dispatch eligibility (that's `state`), audit reason (that's `transition-reason`). Adjacent: `node-state`, `transition-reason`, `cascade`, `lifecycle-handler` (the four `on_executor_complete` resolutions feed this value).

## Invariants

- The cascade gate is `last_outcome == fresh_changed`, not the raw `Complete.changed` bool (`CLAUDE.md "Non-obvious gotchas"`, `runtime/cascade_invalidate.go`).
- Under `on_executor_complete: by_changed` (default), `last_outcome` mirrors `Complete.changed`; under `always_propagate` it's forced to `fresh_changed`; under `never_propagate` it's forced to `fresh_unchanged`.
- `pure_cascade` marks the no-executor-invocation path: a node going `stale → fresh` because all upstream values resolved `fresh_unchanged`.

## Relationship to sibling concept

`concept:last-outcome` is the cascade-firing gate (read by the supervisor's terminal-complete path to decide whether to fire cascade propagation). Sibling concept `concept:transition-reason` is the audit-grade enum carried on every node-state transition.

The cascade-fire predicate is `last_outcome == fresh_changed`, regardless of `transition_reason`. The two enums describe different facets of the same transition: `transition_reason` records "what kind of transition this was" (HandlerComplete, OperatorReset, Invalidate, etc.); `last_outcome` records "what cascade effect, if any, this transition has."

See `concept:transition-reason` for the typical pairing table (`HandlerComplete` + handler resolution → outcome mapping).

## Aliases and historical names

Pre-migration-004 code referenced `t.Changed` directly; the cascade gate was the bool. The split was added under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).

## Open within this concept

- `TransitionReason` (cascade audit) and `last_outcome` (cascade gate) carry overlapping but distinct vocabularies — see `tensions/transition-reason-vs-last-outcome.md`.


## Notes

- 2026-05-14: values become filter predicates on `state` subscriptions (`outcome:` filter on `SubscriptionEntry`). Subscription validation cross-checks `outcome:` against the enum. See `concept:subscription`. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
