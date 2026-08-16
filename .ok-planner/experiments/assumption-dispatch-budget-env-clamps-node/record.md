---
experiment: assumption-dispatch-budget-env-clamps-node
commit: PENDING
---

# Whether the operator's spend figure bounds the template author's

## What it ran against

Two `rimsky-all-in-one` containers from the tree's own image tag, each
registering the bundled claude-agent executor in-process once
`CLAUDE_CODE_OAUTH_TOKEN` is set, with `RIMSKY_EXECUTOR_CLAUDE_BINARY` pointed at
a stand-in agent the run compiles from `probe-agent.go.txt` and mounts in. The
stand-in reports the argv it was spawned with, so the figure the executor
actually hands the CLI is readable from the node's own attribute delta.

One template declares three agent nodes: one asking for `max_budget_usd`
`50.00`, one asking for `0.25`, and one asking for nothing. The first container
sets `RIMSKY_DISPATCH_MAX_USD=1.00`; the second sets no such variable.

## What was observed

Six checks, none failing.

Under a `1.00` deployment figure, the node asking for `50.00` was spawned with
`--max-budget-usd 50.00`, and the node asking for `0.25` with
`--max-budget-usd 0.25`. Only the node that asked for nothing was spawned with
`--max-budget-usd 1.00`. The deployment variable is the fallback the executor
uses when the node is silent, and it is consulted in no other case.

With the variable unset, the node asking for `50.00` was still spawned with that
figure, and the node asking for nothing was spawned with no budget flag at all.
