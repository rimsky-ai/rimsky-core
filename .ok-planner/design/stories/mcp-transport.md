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

## Acceptance

An MCP client connecting to a running rimsky's MCP endpoint discovers a tool catalog covering the templates / tags / instances / nodes / messages / events / audit / breakpoints / assets / lineage / diagnostics / auth surfaces; invoking a tool mirrors the equivalent HTTP route — the same auth gate fires, the same observable state results, and the same response is returned through the MCP wire.

## Falsifier

An MCP tool gate is weaker than the equivalent HTTP route's gate (bypasses auth), OR an MCP tool returns a canned response without invoking the real handler.

## Proof

Executable proof.
