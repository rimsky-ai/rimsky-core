---
concept: transition-reason
status: as-is
aliases: []
references:
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
---

# Transition reason

## What it is

`TransitionReason` is the audit-vocabulary enum carried on every node-state transition. Defined in `foundation/cascade/state.go` as a closed set of ~18 exported `var` values of type `TransitionReason{Kind string}` (`ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonAcquirePass`, `ReasonHandlerPark`, `ReasonHandlerResume`, `ReasonParkTimeout`, etc.). Written by the state-transition apply path (`NextState` callers) and persisted into the audit event-log payload.

## Purpose

`TransitionReason` answers "why did the state machine move?" — for audit consumers. `last_outcome` answers the same question for the cascade-firing predicate. Same row, same transition, different vocabulary, different consumer. Splitting the vocabularies keeps the cascade gate one-column-simple while preserving rich audit detail.

## Boundaries

Owns: the closed enum, the write site at each state transition, the audit-event-log payload field carrying the reason. Does NOT own: dispatch eligibility (`node-state`), cascade-fire gate (`last-outcome`), event-log table mechanics (see `event-log` for audit-log mechanics). Adjacent: `node-state`, `last-outcome`, `cascade`, `event-log`.

## Invariants

- `ReasonHandlerError` is a deliberate dead-end sentinel: legal in audit but rejected as a transition reason by `NextState`.
- Reason values are enumerated as exported `var` values of type `TransitionReason{Kind string}` in `foundation/cascade/state.go`; the type is not a Go enum (a caller could in principle construct `TransitionReason{Kind: "anything"}`), but `NextState` rejects any reason whose `Kind` is not in the known per-state switch with `ErrIllegalTransition`. The runtime guard, not the type system, enforces the closed set.
- Reason is written at every state transition; absence from the audit row is a defect.

## Relationship to sibling concept

`concept:transition-reason` is the audit-grade "why did this transition happen" enum carried on every node-state transition. Sibling concept `concept:last-outcome` is the cascade-firing gate enum carried on the row in `col:rimsky_nodes.last_outcome`. The two are complementary, not duplicative — they record different facets of the same transition.

| transition_reason | typical last_outcome |
|---|---|
| `HandlerComplete` + handler resolved `by_changed` | `fresh_changed` or `fresh_unchanged` (depending on executor's `changed` verdict) |
| `HandlerComplete` + handler resolved `always_propagate` | `fresh_changed` (forced) |
| `HandlerComplete` + handler resolved `never_propagate` | `fresh_unchanged` (forced) |
| `OperatorReset` | unchanged from prior run |
| `Invalidate` (graph-level message) | no write (stale state has no outcome) |
| `HeartbeatLost` | no write (transition is administrative) |
| `AppErrorTerminal` (failed) | `failed` |

See `concept:last-outcome` for the symmetric section on the sibling.

## Aliases and historical names

None live. Pre-migration-004 code used `t.Changed` for the cascade-fire decision and a smaller reason vocabulary for audit; both surfaces were sharpened under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).

## Open within this concept

- `TransitionReason` (audit) and `last_outcome` (cascade-fire gate) carry overlapping but distinct vocabularies — see `tensions/transition-reason-vs-last-outcome.md`.
