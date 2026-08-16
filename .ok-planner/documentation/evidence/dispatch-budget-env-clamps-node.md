---
trap: dispatch-budget-env-clamps-node
release: d977250c
---
# Evidence set — `RIMSKY_DISPATCH_MAX_USD` is a deployment ceiling that clamps any node's `cli.max_budget_usd`, so a template author cannot exceed the operator's limit.

Source of the prior: sibling-symmetry — an operator-side env budget beside a template-side per-node budget, mirroring how the MCP and expose-env allowlists bound per-node declarations

## What the audit ran and observed (assumption record)

Experiment `assumption-dispatch-budget-env-clamps-node` (six checks, none
failing) drove two `rimsky-all-in-one` containers at this tree's tag with the
bundled claude-agent executor pointed at a stand-in agent binary that reports
the argv it was spawned with, so the figure the executor hands the CLI is
readable from the node's own attribute delta. The prior does not hold. Under
`RIMSKY_DISPATCH_MAX_USD=1.00`, the node declaring `cli.max_budget_usd`
`50.00` was spawned with `--max-budget-usd 50.00` and the node declaring `0.25`
with `--max-budget-usd 0.25`; only the node declaring nothing was spawned with
`--max-budget-usd 1.00`. With the variable unset, the declaring node still got
its own figure and the silent node got no budget flag at all. The deployment
variable is a fallback default consulted when the node is silent, not a ceiling:
it never clamps, and a template author can name any figure above it. An operator
who sets it believing spend is capped has set a default for nodes that decline
to choose, and the refusal an out-of-bounds MCP server or expose-env name gets
has no counterpart here — an over-budget node is not refused, it is honored.

## Experiment record (experiment:assumption-dispatch-budget-env-clamps-node)

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

Runnables: `src:.ok-planner/experiments/assumption-dispatch-budget-env-clamps-node/` at the stamped commit.
