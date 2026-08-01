---
decision: claude-agent-cli-mcp-servers-inline-only
status: as-is
aliases: []
---

# Per-node MCP servers are declared inline only, across three transports

## Choice

The per-node MCP server list in claude-agent node config carries inline entries only, each with a required transport discriminator covering three transports — http, stdio, and module, with "http-loopback" accepted as a transport alias for module, both resolving to the same module-loopback runtime path. The handler validates transport-appropriate fields per entry. There is no named-reference shape and no operator-side startup catalog.

## Rationale

Inline declarations covering all three transports keep the whole MCP surface in one shape and one place — the template, guarded by the operator's allowlist (per `decision:policies-service-side-enforcement`). A catalog reachable by named reference would split one job across two declaration shapes and add an operator-side registration step for what is a template-author concern.

## Alternatives

- Inline entries plus an operator-side catalog reachable by named reference — rejected: pre-v1 break-freely; one idiom per job.
- Inline entries for HTTP only, with other transports catalog-only — rejected: a template author's transport choice would dictate which declaration shape they use.
