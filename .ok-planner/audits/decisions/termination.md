---
audit: termination
artifact: decision:termination
determination: supported
commit: b767a27d
audited: 2026-08-02T09:43:58Z
---

# Run-to-terminal verbs poll every declared instance to idle before exiting

Supported. Both run-to-terminal verbs (`rimsky compose run` and `rimsky run` self-host mode) share the single call site `WaitForInstancesTerminal` (`cmd/rimsky/cli/compose/wait.go`, invoked from `run.go`), which polls every declared instance ID until each has no running frame and no pending message and every node's run-summary has settled (fresh or failed, none active or pending) before emitting its instance-terminal progress line and letting the verb exit — matching the codebase's established use of "instance-terminal" as this idle condition (the same usage recorded in the existing `story:live-progress` audit and in the `ProgressPrinter.InstanceTerminal` callback). Parked nodes get no special-cased handling in this loop; they simply count as not-yet-settled until the supervisor's own park time-wake resumes them, consistent with the decision's rationale that park needs no verb-level handling.
