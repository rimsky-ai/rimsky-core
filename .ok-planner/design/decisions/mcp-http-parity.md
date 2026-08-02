---
decision: mcp-http-parity
---

# The MCP tool surface mirrors the HTTP control surface

## Choice

The MCP tool catalog is a full-parity projection of the HTTP control-api surface: every read and mutation the HTTP surface offers has an MCP tool, and both surfaces pass through the same auth and permission gates. MCP is a transport over the one control surface, never a second API with its own semantics.

## Rationale

Agents driving rimsky through MCP need the whole surface or they fall back to a custom HTTP client for the gaps, which defeats the transport. Sharing the auth gates keeps the permission model single-sourced — an MCP caller can do exactly what the same api-key could do over HTTP, no more and no less, so no reasoning about a second security surface is ever needed.

## Alternatives

- A curated agent-oriented subset of tools — rejected: every gap forces a custom client fallback, and the subset boundary would need perpetual re-litigation.
- A separate agent API with its own auth model — rejected: two permission models to keep in agreement, with divergence failing toward silent privilege differences.
