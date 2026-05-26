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

The transition reason is the closed enum carried on every node-state transition. It is a closed set of ~18 named values, each a reason value carrying a kind discriminator (handler-complete, handler-error, pure-cascade, infra-reenqueue, acquire-pass, handler-park, handler-resume, park-timeout, etc.). Written by the state-transition apply path that drives the state machine.

## Purpose

The transition reason exists for two narrow roles:

1. **State-machine validation in the next-state function.** Every transition consults the next-state function, which switches on `(current state, reason)` and returns either the next state or the illegal-transition sentinel. The reason is the load-bearing input to the state machine — without it the machine couldn't reject double-execute or other illegal sequences.
2. **Audit-event kind for non-signal transitions.** A subset of transitions (`dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`, etc.) write rows to the persisted audit-event ledger with the event kind set to the reason's kind discriminator. These are administrative-shaped transitions that don't carry a `concept:signal` envelope; the reason kind is their audit identity.

Signal-bearing transitions (the handler-complete, handler-park, policy-retry, policy-give-up, and handler-pass reasons) no longer use the reason's kind discriminator as the audit kind — they write audit-event rows whose kind is the canonical signal type-path per `concept:signal`. The state-machine validation role of the transition reason is unchanged for those transitions.

## Boundaries

Owns:
- The closed enum.
- The per-state validation switch in the next-state function (the state machine's load-bearing rejection of illegal transitions).
- The audit-event-log payload field carrying the reason **for non-signal transitions**.

Does NOT own:
- The audit kind for signal-bearing transitions (those use signal type-paths from `concept:signal`).
- Dispatch eligibility (`concept:node-run`).
- The cascade-fire gate (now subscriber-driven per `concept:signal` and `concept:cascade`).
- Event-log table mechanics (`concept:event-log`).

Adjacent: `concept:signal`, `concept:cascade`, `concept:event-log`.

## Invariants

- The handler-error reason is a deliberate dead-end sentinel: legal in audit but rejected as a transition reason by the next-state function.
- Reason values are enumerated as named values, each a reason value carrying a kind discriminator; the form is not a closed type-system enum (a caller could in principle construct a reason value with an arbitrary kind string), but the next-state function rejects any reason whose kind is not in the known per-state switch with the illegal-transition sentinel. The runtime guard, not the type system, enforces the closed set.
- Reason is written at every state transition; absence from the audit row for non-signal transitions is a defect. Signal-bearing transitions emit their signal type-path as the audit kind instead.

## Aliases and historical names

None live. Pre-migration-004 code used a boolean changed flag for the cascade-fire decision and a smaller reason vocabulary for audit; both surfaces were sharpened under the reactive-loops design (`spec:2026-05-05-reactive-loops-and-lifecycle-handlers`), then further reshaped under the signal-taxonomy design (`spec:2026-05-23-signal-taxonomy-and-policy-decoupling`) which narrowed the audit-write role for signal-bearing transitions.

## Notes

- 2026-05-23 — Scope narrowed per spec:2026-05-23-signal-taxonomy-and-policy-decoupling. The enum stays for state-machine validation in the next-state function; the audit-write role retires for signal-bearing transitions (which use signal type-paths as the audit-event kind). Non-signal transitions (`dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`, etc.) continue to write the transition reason's kind discriminator as the audit kind — part of the un-taxonomized audit-kind set left open by `tension:events-kind-no-enum`. The last-outcome concept is retired; the relationship table is dropped as the sibling no longer exists.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
