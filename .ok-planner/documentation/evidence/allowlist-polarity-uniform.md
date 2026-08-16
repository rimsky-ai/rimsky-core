---
trap: allowlist-polarity-uniform
release: d977250c
---
# Evidence set — an unset `RIMSKY_*_ALLOWLIST` means "nothing allowed" everywhere, so leaving `RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` unset blocks MCP servers rather than permitting all of them.

Source of the prior: sibling-symmetry — two allowlist families named identically (`_EGRESS_ALLOWLIST`, `_MCP_ALLOWLIST`) in one env namespace

## What the audit ran and observed (assumption record)

Measured on four runs at this tree, covering all four `RIMSKY_*_ALLOWLIST`
variables the product reads. Experiment `claude-agent-mcp-servers-per-node`
re-run (seven checks, none failing) and `claude-agent-expose-env-per-node`
re-run (ten checks, none failing) drive the two claude-agent allowlists;
experiment `assumption-egress-guard-on-every-outbound-service` (seven checks,
none failing) and the `sensor-http` re-run (eleven checks, none failing) drive
the two egress allowlists. No twin was built: each existing experiment already
runs its variable both set and unset.

The prior does not hold, and the two families point opposite ways. Unset,
`RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` permits everything: the node declaring
`forbidden-tool` — the very server refused with `agent/attribute_invalid` when
the variable was set — ran, and its agent saw that server. Unset,
`RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST` behaves the same way: the node
declaring `OPERATOR_ONLY_SECRET` read that secret from the container's
environment. Unset, the two `_EGRESS_ALLOWLIST` variables permit nothing in the
blocked ranges: `http-node` refused a private-range URL with the guard's own
message, and `sensor-http` refused a private-network poll target — each opened
only once the operator named the range.

So the same suffix in the same namespace carries both polarities: two variables
whose absence is "everything is allowed" and two whose absence is "nothing
private is allowed". A reviewer who reads an unset allowlist as a closed door is
right for egress and wrong for the claude-agent executor, where an unset
variable is the open configuration a zero-config local development stack runs
on.

## Experiment record (none built)



Runnables: `src:.ok-planner/experiments/assumption-allowlist-polarity-uniform/` at the stamped commit.
