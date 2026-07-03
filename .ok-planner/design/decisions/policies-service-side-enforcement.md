---
decision: policies-service-side-enforcement
status: as-is
aliases: []
---

# Bundled-service policy enforcement lives entirely inside the service

## Choice

Each bundled service enforces its own operator allowlists inside its handler code — for claude-agent, the MCP server-name allowlist and the exposable env-var-name allowlist. When enforcement rejects a dispatch, the service returns an executor-protocol error outcome naming the specific violation (the disallowed entry, the template instance, and the node). Rimsky does not read, validate, or acknowledge policy content.

## Rationale

Rimsky remains inert to service-specific policy shape; the dispatch payload and protocol carry no policy fields.

## Alternatives

Rimsky-side pre-filter at registration or dispatch — rejected: couples orchestration to service-specific concerns.
