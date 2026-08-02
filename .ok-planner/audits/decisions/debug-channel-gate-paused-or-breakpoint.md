---
audit: debug-channel-gate-paused-or-breakpoint
artifact: decision:debug-channel-gate-paused-or-breakpoint
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:41:29Z
---

# Debug-override gate: instance paused OR an unresumed pause-mode breakpoint hit blocking a runner

Supported. `handleDebugOverride` (`lib/control/controlapi/debug_override.go`) computes `gatePaused := inst.Paused` and, only if that is false, `gateHit` from `BreakpointHits().HasUnresumedPauseHitForInstance`; if neither holds it returns `errInstanceNotDebuggable` as an HTTP 409 naming both `paused` and `breakpoint` as the legal states. The persistence-layer query backing `HasUnresumedPauseHitForInstance` (checked in both the postgres and sqlite implementations) matches the decision's predicate exactly — it filters hits by `mode = pause`, `resumed_at IS NULL`, and joins to a node-run row whose state is one of the in-flight states, i.e. a hit "blocking a runner." `test/scenarios/story_debug_channel_e2e_test.go` exercises all three branches: healthy → 409, paused → 200 with `gate_state=paused`, breakpoint-held → 200 with `gate_state=breakpoint`. The alternative rejected in the decision (parked-node-held frames as a third legal gate state) is confirmed absent — the gate reads only `inst.Paused` and the pause-mode-hit predicate, nothing else.
