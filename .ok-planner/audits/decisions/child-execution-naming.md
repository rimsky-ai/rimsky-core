---
audit: child-execution-naming
artifact: decision:child-execution-naming
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:34Z
---

# The child-execution primitives carry plain names

Supported on the decidable substance: there is one shared dispatch
primitive, `DispatchChildren` (`lib/runtime/child_execution.go`, annotated
`@decision: child-execution-naming`), used by both the fan-out dispatch
path and the sub-graph delegation dispatch path, and its name does not
reuse "delegation." Settlement is genuinely split into two distinct
primitives rather than one merged settle-children function:
`SettleFromDelegate` (sub-graph delegation's settle, called only from
`subgraph_dispatch.go`) and `SettleFromFanoutChild` (fan-out's settle,
called only from `terminal_decision.go`) — no code path merges the two.
The "carry" and "aggregate" vocabulary the decision and
`concept:child-execution` use for these two settle shapes is present and
consistently applied in the implementation as the operation names (the
`subgraph.exit_carry` event kind, the "carry exit writeback" log line, and
the `aggregateParentOutcome`/`Aggregate` helpers that compute the fan-out
verdict), even though the top-level exported Go function names describe
"settle from which mechanism" rather than reusing "Carry"/"Aggregate"
literally as the identifiers.
