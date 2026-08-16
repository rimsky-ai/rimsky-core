---
experiment: claude-agent-mcp-servers-per-node
commit: d977250c
---

# Per-node MCP declarations bounded by an operator allowlist

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which
registers the bundled claude-agent executor in-process once
`CLAUDE_CODE_OAUTH_TOKEN` is set, and points
`RIMSKY_EXECUTOR_CLAUDE_BINARY` at a stand-in agent binary the run compiles from
`probe-agent.go.txt` and mounts into the container. The stand-in reads the
`--mcp-config` file the executor writes for it and reports the server names and
transports it was given.

One template declares three agent nodes, each with a different single inline
`cli.mcp_servers` entry: `validator`, `local-tool` and `forbidden-tool`. The
run drives that template twice: once with
`RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST=validator,local-tool`, and once with the
variable unset.

## What was observed

Seven checks, none failing. With the allowlist set, the node declaring
`validator` saw exactly `validator` plus the executor's own callback server, and
the node declaring `local-tool` saw exactly `local-tool` plus the callback
server; neither saw the other's server. The node declaring `forbidden-tool`
failed its dispatch with `agent/attribute_invalid`, and the error payload named
the server, the instance, the node and the allowlist variable.

With the allowlist unset, the same template's `forbidden-tool` node ran and its
agent saw `forbidden-tool`, while the other two nodes still saw only their own
declarations.
