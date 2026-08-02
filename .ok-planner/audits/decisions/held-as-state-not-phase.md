---
audit: held-as-state-not-phase
artifact: decision:held-as-state-not-phase
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# Held is a first-class node-run state, and non-member cascade is deferred to auto-terminal resolution

Supported. `lib/foundation/cascade/state.go` defines `NodeStateHeld` as one of the node-run state machine's seven states (`pending, stale, running, held, parked, fresh, failed`), a member of `InFlightStates`/`IsInFlight`, reachable only from `running` (`ReasonHandlerHeld`, `ReasonFanoutDispatched`) and exiting only via `ReasonAutoTerminalCommit`→`fresh` or `ReasonAutoTerminalAbandon`→`failed` — matching the claimed uniform acquirer/co-holder, success/error transition shape. `lib/runtime/held_cascade_defer.go` implements the member-filtered immediate cascade at the held transition and the deferred non-member cascade fired only at auto-terminal resolution, exactly as the decision specifies; roughly two dozen call sites across `runner_terminal.go`, `runner_error_policy.go`, `fanout_dispatch.go`, `gate_evaluator.go`, `instance_kill.go`, `signal_emit.go`, and `terminal_decision.go` carry the `@decision: held-as-state-not-phase` citation. The two sibling scenario tests (`held_abandon_cascades_abandoned_test.go`, `held_commit_cascades_success_test.go`) prove the deferred-cascade timing end-to-end.
