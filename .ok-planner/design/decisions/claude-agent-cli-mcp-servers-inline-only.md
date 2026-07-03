---
decision: claude-agent-cli-mcp-servers-inline-only
status: as-is
aliases: []
---

# Per-node MCP servers are declared inline only, across three transports

## Choice

The per-node MCP server list in claude-agent node config carries inline entries only, each with a required transport discriminator: an HTTP entry (name, url, optional headers and allowed-tools), a stdio entry (name, command, optional args, env, allowed-tools), or a module entry (name, module specifier, optional allowed-tools) — where "http-loopback" is accepted as a transport alias for module, both resolving to the same module-loopback runtime path. The named-reference shape pointing into an operator-side startup catalog is deleted, and with it the catalog file env and the inline-allow policy env; the handler validates transport-appropriate fields per entry.

## Rationale

The previous inline schema was HTTP-only; the catalog reachable via named references was the only way to declare stdio or module transports. Extending inline declarations to cover all three transports (with the module alias preserved) closes that surface asymmetry, and per-node declarations put the MCP surface where it belongs — in the template, guarded by the operator's allowlist (per `decision:policies-service-side-enforcement`).

## Alternatives

Keep both shapes with an operator catalog — rejected: pre-v1 break-freely; one idiom per job.
