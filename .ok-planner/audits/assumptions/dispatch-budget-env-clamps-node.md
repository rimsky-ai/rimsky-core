---
assumption: dispatch-budget-env-clamps-node
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `RIMSKY_DISPATCH_MAX_USD` is a deployment ceiling that clamps any node's `cli.max_budget_usd`, so a template author cannot exceed the operator's limit.

As operator controlling agent spend, I would take it that `RIMSKY_DISPATCH_MAX_USD` is a deployment ceiling that clamps any node's `cli.max_budget_usd`, so a template author cannot exceed the operator's limit.

## Source

sibling-symmetry — an operator-side env budget beside a template-side per-node budget, mirroring how the MCP and expose-env allowlists bound per-node declarations

## What a run would observe

set the env below a node's declared `cli.max_budget_usd` and observe which value the dispatch enforces.

## Measured

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
