---
story: mcp-transport
status: as-is
---

# Operator/agent drives rimsky entirely via MCP

## Role

As an operator or AI agent using rimsky through an MCP client, I can perform every read and mutation through the MCP tool surface that the HTTP surface offers, with the same auth and permission semantics, so that an agent can drive rimsky deployments without a custom client.

## Capability

MCP tool catalog providing parity with the HTTP control-api surface, with identical auth gates and observable state.

## Business value

An agent can drive rimsky deployments without a custom client, with no auth or permission bypass relative to the HTTP surface.

