---
story: claude-agent
status: as-is
---

# Operator wires agentic node with full controls

## Story

As an operator wiring an agentic node, I can use the bundled claude-agent executor (a Go implementation that spawns the agent CLI as a subprocess) to dispatch work with async-handoff callbacks, let template authors declare each node's MCP servers inline in node config (across the three supported transports — HTTP, stdio, and in-process module, with HTTP-loopback as a module alias) while I hold an operator allowlist bounding which server names any template may use, let template authors declare each node's expose-env needs inline in node config while I hold an operator allowlist bounding which env-var names any template may expose (so secrets sit on the executor process and the agent reads them directly, without rimsky ever handling the plaintext), gate the run with a cryptographic sign-off over the real bound output, and observe the declared error classes routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.

Bundled claude-agent executor: async-handoff CLI dispatch (gRPC plus a preserved HTTP-JSON bridge); per-node inline MCP server declarations covering three transports (http, stdio, module) intersected with an operator allowlist read from the executor process's environment; per-node expose-env declarations intersected with an operator allowlist, forwarding the named variables from the executor process's environment to that node's CLI child; cryptographic sign-off over bound output; thirteen declared error classes (blocked, internal-error, attribute-invalid, schema-violation, cli-spawn-failed, timeout, tool-use-timeout, subprocess-exit wildcard, rate-limited, context-exceeded, tool-use-failed wildcard, refused, signoff-unobtained) routable through error-policy. Operator allowlists use the same env-var names in containerized and all-in-one modes and default open when unset.

Template authors own each node's MCP surface and secret needs in the template itself; operators own the boundary of what any template may reach, via two namespaced env allowlists on the process they run. The executor's controls (sign-off, per-node MCP declarations, per-node expose-env, error-class routing) wire into rimsky's existing observability and error-routing infrastructure. Secrets stay on the executor process as env vars and reach the agent directly through the CLI child's environment; rimsky's substitution grammar (`decision:env-as-substitution-source-kind`) is for non-secret template configuration, not for shuttling credentials.
