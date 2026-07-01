---
story: claude-agent
status: as-is
---

# Operator wires agentic node with full controls

## Role

As an operator wiring an agentic node, I can use the bundled claude-agent executor to dispatch work to the agent CLI with async-handoff callbacks, configure available MCP servers through a startup catalog (with named-reference declarations in node config across the four supported MCP transports — HTTP, stdio, in-process module, and HTTP loopback — plus an inline-allow policy), declare an env-var pass-through allowlist that exposes specific names from the executor container's environment to the agent CLI child (so secrets sit on the executor container and the agent reads them directly, without rimsky ever handling the plaintext), gate the run with a cryptographic sign-off over the real bound output, and observe four declared error classes (rate-limited, context-exceeded, refused, tool-use-failed) routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.

## Capability

Bundled claude-agent executor: async-handoff CLI dispatch; MCP catalog with named-reference declarations across four transports plus an inline-allow policy; env-var pass-through allowlist that forwards a declared set of variable names from the executor container's environment to the CLI child; cryptographic sign-off over bound output; four declared error classes routable through error-policy.

## Business value

Operators run controllable, secure, observable agentic dispatches: the executor's controls (sign-off, MCP catalog, env pass-through, error-class routing) wire into rimsky's existing observability and error-routing infrastructure. Secrets stay on the executor container as env vars and reach the agent directly through the CLI child's inherited env; rimsky's substitution grammar (`decision:env-as-substitution-source-kind`) is for non-secret template configuration, not for shuttling credentials.

## Acceptance

With a deployed template referencing the claude-agent executor, an operator drives a real dispatch end-to-end (CLI spawned, agent does real work, async-callback returns); the gate accepts a signature over the run's actual accumulated bound output and rejects an empty-output signature; the MCP catalog resolves named-reference declarations to declared transports and refuses inline servers when the inline-allow policy is off; env vars named in the executor container's expose-env allowlist appear in the CLI child's own environment (available to the agent via its normal env-access mechanism); env vars NOT on the allowlist do not appear in the CLI child; plaintext credential values never appear in rimsky's persisted attribute bag; each of the four declared error classes routes through error-policy or subscriber matching when the agent surfaces the corresponding condition.

## Falsifier

The sign-off accepts a signature over stale output, OR the inline-allow policy is off but an inline server definition is silently accepted, OR a declared error class fires but the policy router treats it as generic, OR a value from the executor container's env leaks to the CLI child without appearing on the allowlist, OR a plaintext credential appears in rimsky's persisted attribute bag.

## Proof

Executable proof.
