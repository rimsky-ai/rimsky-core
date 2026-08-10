---
experiment: claude-agent-expose-env-per-node
commit: PENDING
---

# Per-node expose-env declarations bounded by an operator allowlist

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which
registers the bundled claude-agent executor in-process once
`CLAUDE_CODE_OAUTH_TOKEN` is set, and points
`RIMSKY_EXECUTOR_CLAUDE_BINARY` at a stand-in agent binary the run compiles from
`probe-agent.go.txt` and mounts into the container. Three distinct secrets are
set in the container's own environment as `VALIDATOR_TOKEN`, `REVIEWER_SEED` and
`OPERATOR_ONLY_SECRET`. The stand-in reads all three names from its own
environment and reports which were present, each as a SHA-256 digest, never the
value.

One template declares three agent nodes, each with a different single
`cli.expose_env` entry. The run drives that template twice: once with
`RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST=VALIDATOR_TOKEN,REVIEWER_SEED`, and
once with the variable unset.

## What was observed

With the allowlist set, the node declaring `VALIDATOR_TOKEN` read exactly that
variable at the operator-set value, and the node declaring `REVIEWER_SEED` read
exactly that one; neither read the other's variable. The node declaring
`OPERATOR_ONLY_SECRET` failed its dispatch with `agent/attribute_invalid`, and
the error payload named the variable, the instance, the node and the allowlist
variable. None of the three plaintext secrets appears anywhere in the instance's
event log, node-run rows, instance record, audit log, or per-node attributes.

With the allowlist unset, the same template's `OPERATOR_ONLY_SECRET` node read
that variable, and the other two nodes still read only their own declarations.

Ten checks, none failing.
