---
concept: transition-reason
status: as-is
aliases: []
---

# Transition reason

## What it is

The transition reason is the closed vocabulary carried on every node-state transition: a set of named values, each carrying a kind discriminator. Membership of the set is owned by the state-machine code, not enumerated here. Written by the state-transition apply path that drives the state machine; consumed only by the next-state function — the reason is never an audit identity.

`instance_killed` is the forced-instance-teardown reason: it drives every in-flight node-run (pending, stale, running, held, parked — see `concept:node-run`'s in-flight set) to failed, and is accepted by the next-state function from exactly those five states. A killed instance leaves nothing eligible to dispatch. The terminal states fresh and failed are already settled, so the next-state function rejects `instance_killed` from them as an illegal transition. It is **state-machine-validation-only** — it is NOT emitted as an audit-event kind. When the force-terminate control path tears an instance down, the teardown's auditable cause is the single administrative `instance_terminated` event-log row, not the per-node reason kind (the per-node state update writes run-row state and the settling-signal-type field, with no audit row). It is distinct from `policy_give_up` (policy-chain-driven) and from the creation-reason field's `operator_invalidate` value per `concept:node-run`, a separate vocabulary for why a node-run was created rather than why it transitioned.

## Purpose

The transition reason exists for one narrow role: **state-machine validation in the next-state function.** Every transition consults the next-state function, which switches on `(current state, reason)` and returns either the next state or the illegal-transition sentinel. The reason is the load-bearing input to the state machine — without it the machine couldn't reject double-execute or other illegal sequences.

Audit identity lives elsewhere: audit-event rows carry either a canonical signal type-path or an operational kind, per `concept:signal` and `concept:event-log` — never the transition reason's kind discriminator.

## Boundaries

Owns:
- The closed reason vocabulary.
- The per-state validation switch in the next-state function (the state machine's load-bearing rejection of illegal transitions).

Does NOT own:
- Audit-event kinds (signal type-paths and operational kinds — see `concept:signal`, `concept:event-log`).
- Dispatch eligibility (`concept:node-run`).
- The cascade-fire gate (subscriber-driven per `concept:signal` and `concept:cascade`).
- Event-log table mechanics (`concept:event-log`).

Adjacent: `concept:signal`, `concept:cascade`, `concept:event-log`.

## Invariants

- Reason values are enumerated as named values, each a reason value carrying a kind discriminator; the form is not a closed type-system enum (a caller could in principle construct a reason value with an arbitrary kind string), but the next-state function rejects any reason whose kind is not in the known per-state switch with the illegal-transition sentinel. The runtime guard, not the type system, enforces the closed set.
- A reason accompanies every state transition and is consulted by the next-state function; no transition bypasses the validation switch. The reason is never written as an audit-event kind.
- `instance_killed` is a state-machine-validation-only reason: the next-state function accepts it from every in-flight state (pending, stale, running, held, parked), driving each to failed, and rejects it from the terminal states fresh and failed as an illegal transition. It is never written as an audit-event kind. Forced instance teardown records its auditable cause once via the administrative `instance_terminated` event-log row, not per node-run.
