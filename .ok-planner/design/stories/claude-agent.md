---
story: claude-agent
status: as-is
---

# Operator wires agentic node with full controls

## Role

As an operator wiring an agentic node, I can use the bundled `claude-agent` executor to dispatch work to the Claude CLI with async-handoff callbacks, configure available MCP servers through a startup catalog (with `{ref:<name>}` references in node config across http / stdio / module / http-loopback transports plus an `allow_inline` policy), resolve `${env:VAR}` references in validator MCP server headers at spawn time without leaking secrets into persisted attributes, gate the run with a cryptographic sign-off over the real bound output, and observe four declared error classes (rate-limited, context-exceeded, refused, tool-use-failed) routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.

## Capability

Bundled `claude-agent` executor: async-handoff CLI dispatch; MCP catalog with `{ref:}` references across four transports plus inline policy; `${env:VAR}` resolution in validator headers without leaking to persisted attributes; cryptographic sign-off over bound output; four declared error classes routable through error-policy.

## Business value

Operators run controllable, secure, observable agentic dispatches: the executor's controls (sign-off, MCP catalog, env-var redaction, error-class routing) wire into rimsky's existing observability and error-routing infrastructure.

## Acceptance

With a deployed template referencing `claude-agent`, an operator drives a real dispatch end-to-end (CLI spawned, agent does real work, async-callback returns); the gate accepts a signature over the run's actual accumulated bound output and rejects an empty-output signature; the MCP catalog resolves `{ref:}` references to declared transports and refuses inline servers when `allow_inline=false`; validator MCP headers carry resolved env-var values on the wire without the plaintext appearing in persisted attributes; each of the four declared error classes routes through error-policy / subscriber matching when the agent surfaces the corresponding condition.

## Falsifier

The sign-off accepts a signature over stale output, OR `allow_inline=false` is silently accepted alongside an inline server definition, OR a declared error class fires but the policy router treats it as generic, OR an env-var-referenced credential persists in plaintext attributes.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
