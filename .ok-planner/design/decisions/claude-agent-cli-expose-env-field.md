---
decision: claude-agent-cli-expose-env-field
---

# Per-node expose-env is a node-config field

## Choice

Claude-agent node config carries an expose-env field: a list of env-var names that node's CLI child should see. The handler intersects each node's declared list with the operator allowlist (per `decision:claude-agent-env-passthrough-allowlist`) and injects the allowed names' values from the handler process's environment into the spawned child's environment; a declaration outside the operator allowlist fails the dispatch with an error naming the variable, the template instance, and the node. There is no container-wide exposure knob; exposure is always per-node.

## Rationale

Moves the per-node concern (what secrets this node needs) into the node config, where it belongs; keeps the operator concern (what's permitted at all) separate.

## Alternatives

- An operator-only allowlist without a per-node knob — rejected: forces every operator-permitted env var into every dispatched CLI child, giving nodes no way to scope what they actually need.
- Per-node declaration without an operator allowlist safeguard — rejected: template authors would gain unilateral control over which secrets the CLI child sees; the intersection preserves operator control while letting template authors scope their per-node needs.
