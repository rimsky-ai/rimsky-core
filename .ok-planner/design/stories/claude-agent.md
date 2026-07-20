---
story: claude-agent
status: as-is
---

# Operator wires agentic node with full controls

## Role

As an operator wiring an agentic node, I can use the bundled claude-agent executor (a Go implementation that spawns the agent CLI as a subprocess) to dispatch work with async-handoff callbacks, let template authors declare each node's MCP servers inline in node config (across the three supported transports — HTTP, stdio, and in-process module, with HTTP-loopback as a module alias) while I hold an operator allowlist bounding which server names any template may use, let template authors declare each node's expose-env needs inline in node config while I hold an operator allowlist bounding which env-var names any template may expose (so secrets sit on the executor process and the agent reads them directly, without rimsky ever handling the plaintext), gate the run with a cryptographic sign-off over the real bound output, and observe the declared error classes routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.

## Capability

Bundled claude-agent executor: async-handoff CLI dispatch (gRPC plus a preserved HTTP-JSON bridge); per-node inline MCP server declarations covering three transports (http, stdio, module) intersected with an operator allowlist read from the executor process's environment; per-node expose-env declarations intersected with an operator allowlist, forwarding the named variables from the executor process's environment to that node's CLI child; cryptographic sign-off over bound output; thirteen declared error classes (blocked, internal-error, attribute-invalid, schema-violation, cli-spawn-failed, timeout, tool-use-timeout, subprocess-exit wildcard, rate-limited, context-exceeded, tool-use-failed wildcard, refused, signoff-unobtained) routable through error-policy. Operator allowlists use the same env-var names in containerized and all-in-one modes and default open when unset.

## Business value

Template authors own each node's MCP surface and secret needs in the template itself; operators own the boundary of what any template may reach, via two namespaced env allowlists on the process they run. The executor's controls (sign-off, per-node MCP declarations, per-node expose-env, error-class routing) wire into rimsky's existing observability and error-routing infrastructure. Secrets stay on the executor process as env vars and reach the agent directly through the CLI child's environment; rimsky's substitution grammar (`decision:env-as-substitution-source-kind`) is for non-secret template configuration, not for shuttling credentials.

## Acceptance

With a deployed template referencing the claude-agent executor, an operator drives a real dispatch end-to-end (a real CLI subprocess spawned by the handler, agent does real work, async-callback returns); two nodes in one template with different `cli.mcp_servers` declarations observably reach different MCP surfaces at spawn, and declarations across the http and module transports each resolve at the scenario level (the stdio transport resolves in the executor but is proven only at the unit/parse level, not yet dispatched by a scenario); two nodes with different `cli.expose_env` declarations observably see different env-var sets in their own CLI children; a node declaring an MCP server or env-var name outside the operator's allowlist fails that dispatch with an error naming the disallowed entry, the template instance, and the node; the gate accepts a signature over the run's actual accumulated bound output and rejects an empty-output signature; plaintext exposed-env values never appear in rimsky's persisted attribute bag; declared error classes route through error-policy or subscriber matching when the agent surfaces the corresponding condition; the HTTP-JSON bridge accepts the same dispatch body it accepted before the Go port.

## Falsifier

Two nodes with different declared MCP server lists observably reach the same MCP surface at spawn, OR any of the three transports becomes unreachable via per-node declaration, OR a node declaring an entry outside an operator allowlist dispatches anyway, OR the allowlist rejection error is generic (doesn't name the disallowed entry, instance, and node), OR the sign-off accepts a signature over stale output, OR a declared error class fires but the policy router treats it as generic, OR a value from the executor's env leaks to a CLI child that did not declare it, OR a plaintext credential appears in rimsky's persisted attribute bag, OR rimsky's own dispatch payload gains an MCP- or expose-env-related field.

## Proof

Executable proof — a scenario test drives a template with claude-agent nodes through the bundled handler end-to-end. The proof exhibits: a real `claude` CLI subprocess is spawned by the handler; per-node MCP surfaces observably differ across nodes in the same template, covering the http and module transports reachable via per-node declaration (stdio is reachable via the same declaration mechanism but is proven only at the unit/parse level, not yet by a scenario dispatch); per-node expose-env sets observably differ in each spawned child's own environment; sign-off gates accept a signature over the run's actual accumulated bound output and reject an empty-output signature; declared error classes route through error-policy or subscriber matching when the agent surfaces the corresponding condition; a grep over rimsky's persisted attribute bag confirms no plaintext exposed-env value appears.
