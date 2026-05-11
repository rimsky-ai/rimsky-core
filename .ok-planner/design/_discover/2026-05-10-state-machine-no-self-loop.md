---
topic: state-machine-no-self-loop
kind: invariant
---

# `NextState` rejects `current == requested` under `dispatch_claimed`; no idempotency shortcut

## Description

A state machine of the form `NextState(current, reason) → (next, error)` admits an "ergonomic" optimization: if `current == requested`, return `current` without error. That makes `running → running` a no-op rather than an error. It also creates a double-execute hazard: if two supervisors both believe they've claimed the same node and both call the transition, the no-op branch silently approves the second one.

`@blessed-invariant 1` (and `@blessed-invariant (§17)` at `foundation/cascade/state.go:103-108`) is documented as never short-circuiting when `current == requested`. Specifically `running → running` under `dispatch_claimed` returns `ErrIllegalTransition`. The annotation:

> This is the load-bearing guard against double-execute. Any Go implementation that adds an idempotency optimization for "ergonomics" breaks the invariant.

The five legal node states are `fresh`, `stale`, `running`, `failed`, `parked` (`foundation/cascade/state.go:110-117`). The complete transition table is the body of `NextState` — every legal edge is one explicit `if reason.Kind == "..." { return ... }` branch.

The `UpdateState` driver impls call `NextState` and reject the row update on illegal transition rather than short-circuiting:

- `foundation/persistence/postgres/nodes.go:9` — invariant annotation at the file head.
- `foundation/persistence/postgres/nodes.go:302` — `UpdateState` body, comment "no short-circuit; state machine alone decides."
- `foundation/persistence/sqlite/nodes.go:7` — same annotation.

The reserved `ReasonHandlerError` sentinel (`foundation/cascade/state.go:55-71`) is a deliberate dead-end value: it exists to fail-closed in tests rather than silently in production. Handlers must route through the policy chain and emit `policy_retry` / `policy_invalidate` / `policy_give_up` instead. `TestNextState_HandlerErrorIsAuditOnly` (named at the file) verifies the dead-end behavior.

`docs/concepts/node-state.md` puts the rule in operator terms: "The state-machine transitions are explicit and exhaustive — no implicit transitions. Any transition that would violate the matrix above is rejected; the system does not silently coerce same-state transitions to no-ops." The four operator-visible states (`fresh`, `stale`, `running`, `failed`) are documented as the "named runtime states a node can occupy"; `parked` is the fifth and was added with the platform-extensions design.

CLAUDE.md "Blessed invariants" §1: "State machine rejects illegal transitions. `running → running` under reason `dispatch_claimed` errors — it is **not** silently idempotent."

A future cleanup that "normalizes" the state machine to be idempotent is automatically a breaking change of the invariant test. The verify-before-run guard (`@blessed-invariant 5`) is the runtime-side complement: it catches the double-execute case at the row-read level before the state-machine transition is even attempted.

## Code surface

- `foundation/cascade/state.go:103-108, 110-117, 55-71` — `NextState`, legal states, `ReasonHandlerError` sentinel.
- `foundation/cascade/state_test.go` — `TestNextState_HandlerErrorIsAuditOnly` and friends.
- `foundation/persistence/postgres/nodes.go:9, 302` — `UpdateState` postgres impl.
- `foundation/persistence/sqlite/nodes.go:7` — sqlite impl mirror.
- `foundation/integration/runner.go:31-39` — verify-before-run, the runtime-side complement.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §1.
- `docs/concepts/node-state.md` — operator-facing rendering of the rule.
- `docs/concepts/parked.md` — `parked → fresh` explicitly rejected.

## Adjacent topics

- `2026-05-10-verify-before-run-guard` — runtime complement that catches the same race.
- `2026-05-10-claimant-guarded-release` — sibling load-bearing guard for the holder column.
- `2026-05-10-cascade-fires-on-last-outcome` — `last_outcome` is observability, not gate; complements state-machine.
- `2026-05-10-parked-state-and-resume` — `parked` is the fifth state.

## Observations

- The negative-test naming (`TestNextState_HandlerErrorIsAuditOnly`) is precise: `ReasonHandlerError` is allowed in audit logs but not as a state-transition reason. A handler that tries to use it gets an explicit failure; this preserves the distinction between "the supervisor wants to react to this error" (legitimate handler chain) and "the supervisor wants to claim a state transition with a sentinel reason" (forbidden).
- The 5-state vocabulary is mirrored in dashboards, control-api responses, and event log entries. The `parked` state's addition (per `2026-05-10-parked-state-and-resume`) extended the original 4-state ("fresh, stale, running, failed") vocabulary.
- The transition table is `NextState`'s body; there is no external table or YAML. A reader who wants to enumerate edges reads the function. `foundation/cascade/state_test.go` provides exhaustive coverage.
- The rule's value is precisely at the double-execute race: a supervisor's verify-before-run guard catches "I lost the row to another supervisor"; the state-machine guard catches "I'm about to write a transition that's already in flight."
