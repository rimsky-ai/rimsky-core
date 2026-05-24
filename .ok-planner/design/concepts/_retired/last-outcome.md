---
concept: last-outcome
status: retired
aliases: []
references:
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - ../../specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
---

> **Retired 2026-05-23** per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`.
>
> The column retired alongside the cascade-fire-gate semantic (cascade-fire is now subscriber-driven via `concept:signal`). Signal-payload fields (`changed` on `terminal/success`, `discarded_claims` on `transient/retry`) carry the granularity that mattered. The lineage projection's `last_outcome` field was replaced with `settling_signal_type`. The replacement column on `rimsky_node_runs` is `settling_signal_type` (added by migration 013, dropped `last_outcome` by migration 014).

# Last outcome

## What it is

`last_outcome` is a TEXT column on `rimsky_nodes` carrying one of `{fresh_changed, fresh_unchanged, passed, pure_cascade, failed}`. Written by the supervisor's terminal-handler resolution alongside the state transition. Separate from `state`.

### Truth table

The five values capture the resolution flavor of the most recent terminal-for-this-frame transition:

| Value | Meaning | Cascade effect |
|---|---|---|
| `fresh_changed` | Node committed and propagated; landed in `fresh`. | Fires downstream cascade propagation. |
| `fresh_unchanged` | Node committed without change; landed in `fresh`. | Halts propagation at this node; downstream cascade does NOT fire. |
| `passed` | Lifecycle handler resolved `pass` (Unavailable / Blocked / Errored skipped without error routing). | No cascade fire. |
| `pure_cascade` | Node transitioned `stale → fresh` via dependency fallthrough only (no executor invocation; all upstream values resolved `fresh_unchanged`). | No cascade fire. |
| `failed` | Node landed in `failed` via `give_up` policy or `dispatch_impossible`. | No cascade fire from `failed`. |

The five named node states (`fresh`, `stale`, `running`, `failed`, `parked`) are orthogonal: `last_outcome` is an additional column written by the same transition that lands the node in `fresh` or `failed`. The cascade-firing predicate is `last_outcome == fresh_changed`; under the default `on_executor_complete: by_changed` resolution this is functionally identical to the prior `t.Changed` bool gate.

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

- 2026-05-14: values become filter predicates on `state` subscriptions (`outcome:` filter on `SubscriptionEntry`). Subscription validation cross-checks `outcome:` against the enum. See `concept:node-subscription`. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- [2026-05-18] Folded content from former `docs/concepts/node-state.md` (now retired) — the cascade-gate-relevant residue from that doc (the `last_outcome` exposition and 5-state truth table) moved here rather than to `concept:node-state`, since the cascade-gate semantics naturally co-locate with `last-outcome`. The five-node-state vocabulary itself remains the canonical responsibility of `concept:node-state`.
