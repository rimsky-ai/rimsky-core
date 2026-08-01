---
story: host-agent-control-plane
status: as-is
---

# Operator manages agent lifecycle via CLI

## Role

As an operator running rimsky-dispatched workflows on a dev machine, I can start the host-agent locally, check its connection status, and stop it cleanly (children reaped) through the host-agent control-plane CLI surface, so that I manage the agent's lifecycle from the same CLI that drives the rimsky stack.

## Capability

Host-agent CLI control plane: start / status / stop verbs. The start verb launches connected to the configured proxy or refuses with a diagnostic; the status verb reports connection state, configured proxy, and spawned children; the stop verb politely terminates the agent and reaps all children with the documented grace period.

## Business value

Operators manage the host-agent's lifecycle from the same CLI that drives the rimsky stack — no separate process-management ceremony.

