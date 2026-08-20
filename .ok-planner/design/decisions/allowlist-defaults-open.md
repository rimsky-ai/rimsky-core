---
decision: allowlist-defaults-open
---

# Bundled-service reference allowlists default open when operator config is absent

## Choice

A bundled service's reference allowlist defaults open. When the operator env var carrying it is unset, the service accepts every name the template declares. A reference allowlist bounds the names a template may declare, such as the claude-agent executor's MCP servers and its exposed environment variables. A set-but-empty allowlist is an explicit closed boundary. A destination allowlist carries the opposite default (see `decision:destination-allowlists-default-closed`).

## Rationale

Zero-config local dev works out of the box; operators wanting policy set the env explicitly. A reference allowlist bounds what a template may declare inside the operator's own deployment. It does not bound where a service sends traffic, so an unset one opens no network boundary.

## Alternatives

- Default closed — rejected: breaks zero-config local use, where no operator config exists at all.
