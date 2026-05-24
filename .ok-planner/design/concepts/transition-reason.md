---
concept: transition-reason
status: as-is
aliases: []
references:
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - ../../specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
---

# Transition reason

## What it is

`TransitionReason` is the closed enum carried on every node-state transition. Defined in `foundation/cascade/state.go` as a closed set of ~18 exported `var` values of type `TransitionReason{Kind string}` (`ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonAcquirePass`, `ReasonHandlerPark`, `ReasonHandlerResume`, `ReasonParkTimeout`, etc.). Written by the state-transition apply path (`NextState` callers).

## Purpose

`TransitionReason` exists for two narrow roles:

1. **State-machine validation in `NextState`.** Every transition consults `NextState(current, reason)` which switches on the reason and returns either the next state or `ErrIllegalTransition`. The reason is the load-bearing input to the state machine — without it the machine couldn't reject double-execute or other illegal sequences.
2. **Audit-event kind for non-signal transitions.** A subset of transitions (`dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`, etc.) write `rimsky_events` rows with `kind = TransitionReason.Kind`. These are administrative-shaped transitions that don't carry a `concept:signal` envelope; the reason kind is their audit identity.

Signal-bearing transitions (`HandlerComplete`, `HandlerPark`, `PolicyRetry`, `PolicyGiveUp`, `HandlerPass`) no longer use `TransitionReason.Kind` as the audit kind — they write `rimsky_events` rows whose `kind` is the canonical signal type-path per `concept:signal`. The state-machine validation role of `TransitionReason` is unchanged for those transitions.

## Boundaries

Owns:
- The closed enum.
- The per-state validation switch in `NextState` (the state machine's load-bearing rejection of illegal transitions).
- The audit-event-log payload field carrying the reason **for non-signal transitions**.

Does NOT own:
- The audit kind for signal-bearing transitions (those use signal type-paths from `concept:signal`).
- Dispatch eligibility (`concept:node-state`).
- The cascade-fire gate (now subscriber-driven per `concept:signal` and `concept:cascade`).
- Event-log table mechanics (`concept:event-log`).

Adjacent: `concept:signal`, `concept:cascade`, `concept:event-log`.

## Invariants

- `ReasonHandlerError` is a deliberate dead-end sentinel: legal in audit but rejected as a transition reason by `NextState`.
- Reason values are enumerated as exported `var` values of type `TransitionReason{Kind string}` in `foundation/cascade/state.go`; the type is not a Go enum (a caller could in principle construct `TransitionReason{Kind: "anything"}`), but `NextState` rejects any reason whose `Kind` is not in the known per-state switch with `ErrIllegalTransition`. The runtime guard, not the type system, enforces the closed set.
- Reason is written at every state transition; absence from the audit row for non-signal transitions is a defect. Signal-bearing transitions emit their signal type-path as the audit kind instead.

## Aliases and historical names

None live. Pre-migration-004 code used `t.Changed` for the cascade-fire decision and a smaller reason vocabulary for audit; both surfaces were sharpened under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`), then further reshaped under the signal-taxonomy design (`.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`) which narrowed the audit-write role for signal-bearing transitions.

## Notes

- 2026-05-23 — Scope narrowed per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. The enum stays for state-machine validation in NextState; the audit-write role retires for signal-bearing transitions (which use signal type-paths in `rimsky_events.kind`). Non-signal transitions (`dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`, etc.) continue to write `TransitionReason.Kind` as the audit kind — part of the un-taxonomized audit-kind set left open by `tension:events-kind-no-enum`. `concept:last-outcome` retires; the relationship table is dropped as the sibling no longer exists.
