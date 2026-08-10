---
experiment: claude-agent
commit: PENDING
---

# An agentic node with per-node declarations, a sign-off gate and error classes

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which
registers the bundled claude-agent executor in-process once
`CLAUDE_CODE_OAUTH_TOKEN` is set. The run points
`RIMSKY_EXECUTOR_CLAUDE_BINARY` at a stand-in agent binary the run compiles from
`probe-agent.go.txt` and mounts into the container; the stand-in speaks the same
CLI contract the executor drives — it reads `--session-id`, `--resume` and
`--mcp-config`, and calls the executor's callback tools over
`RIMSKY_CALLBACK_URL` and `RIMSKY_CALLBACK_TOKEN`. The operator allowlists
`RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` and
`RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST` are set to one entry each, and one
secret is set in the container's own environment.

The template declares seven nodes: a worker declaring an inline MCP server, an
inline expose-env name and a required ed25519 sign-off over the value it writes;
two nodes whose agent signs the wrong binding or a value it never writes; a node
reporting a declared error class routed by policy; a node reporting a declared
class with no policy; a node reporting a class outside the executor's declared
vocabulary; and a node subscribing to `terminal/error/agent/*`.

## What was observed

The executor advertises thirteen declared error classes over the control API.
Each agent node's work was handed off asynchronously (`transient/await_async`)
and settled later by callback. The worker's agent saw exactly its own declared
MCP server plus the callback server, and exactly its own declared environment
variable at the operator-set value. Its sign-off passed the gate, and the value
the signature covered is the value the node committed. The node signing another
dispatch's binding and the node signing a value it never wrote both failed
`agent/signoff_unobtained`. The class routed to `pass` settled the run fresh
while the settling signal still named `agent/context_exceeded`; the class with no
policy failed the run as `terminal/error/agent/refused`; the wildcard subscriber
ran on that failure. The class outside the declared vocabulary was refused at the
callback surface and the dispatch failed under a declared class instead.

Twelve checks, none failing.
