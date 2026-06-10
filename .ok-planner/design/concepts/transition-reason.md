---
concept: transition-reason
status: as-is
aliases: []
---

# Transition reason

## What it is

The transition reason is the closed enum carried on every node-state transition. It is a closed set of ~18 named values, each a reason value carrying a kind discriminator (handler-complete, handler-error, pure-cascade, infra-reenqueue, acquire-pass, handler-park, handler-resume, park-timeout, `instance_killed`, etc.). Written by the state-transition apply path that drives the state machine.

`instance_killed` is the forced-instance-teardown reason: it drives a resource-holding node-run (running or parked — the states that hold or await a claim) to failed, and is accepted by the next-state function only from those two states. Non-resource-holding non-terminal states (fresh, stale) are not force-failed — a terminated instance's pending node-runs are left inert — so the next-state function rejects `instance_killed` from them as an illegal transition. It is **state-machine-validation-only** — it is NOT emitted as an audit-event kind. When the force-terminate control path tears an instance down, the teardown's auditable cause is the single administrative `instance_terminated` event-log row, not the per-node reason kind (the per-node state update writes run-row state only, with no audit row). It is distinct from `policy_give_up` (policy-chain-driven) and the operator reset/invalidate reasons.

## Purpose

The transition reason exists for two narrow roles:

1. **State-machine validation in the next-state function.** Every transition consults the next-state function, which switches on `(current state, reason)` and returns either the next state or the illegal-transition sentinel. The reason is the load-bearing input to the state machine — without it the machine couldn't reject double-execute or other illegal sequences.
2. **Audit-event kind for non-signal transitions.** A subset of transitions (`dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`, etc.) write rows to the persisted audit-event ledger with the event kind set to the reason's kind discriminator. These are administrative-shaped transitions that don't carry a `concept:signal` envelope; the reason kind is their audit identity.

Signal-bearing transitions (the handler-complete, handler-park, policy-retry, policy-give-up, and handler-pass reasons) write audit-event rows whose kind is the canonical signal type-path per `concept:signal`, not the reason's kind discriminator. The state-machine validation role of the transition reason holds for those transitions.

## Boundaries

Owns:
- The closed enum.
- The per-state validation switch in the next-state function (the state machine's load-bearing rejection of illegal transitions).
- The audit-event-log payload field carrying the reason **for non-signal transitions**.

Does NOT own:
- The audit kind for signal-bearing transitions (those use signal type-paths from `concept:signal`).
- Dispatch eligibility (`concept:node-run`).
- The cascade-fire gate (subscriber-driven per `concept:signal` and `concept:cascade`).
- Event-log table mechanics (`concept:event-log`).

Adjacent: `concept:signal`, `concept:cascade`, `concept:event-log`.

## Invariants

- The handler-error reason is a deliberate dead-end sentinel: legal in audit but rejected as a transition reason by the next-state function.
- Reason values are enumerated as named values, each a reason value carrying a kind discriminator; the form is not a closed type-system enum (a caller could in principle construct a reason value with an arbitrary kind string), but the next-state function rejects any reason whose kind is not in the known per-state switch with the illegal-transition sentinel. The runtime guard, not the type system, enforces the closed set.
- Reason is written at every state transition; absence from the audit row for non-signal transitions is a defect. Signal-bearing transitions emit their signal type-path as the audit kind instead.
- `instance_killed` is a state-machine-validation-only reason: the next-state function accepts it from the resource-holding states (running, parked), driving each to failed, and rejects it from the non-resource-holding non-terminal states (fresh, stale) as an illegal transition. It is never written as an audit-event kind. Forced instance teardown records its auditable cause once via the administrative `instance_terminated` event-log row, not per node-run.
