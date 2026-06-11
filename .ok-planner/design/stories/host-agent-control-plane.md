---
story: host-agent-control-plane
status: as-is
---

# Operator manages agent lifecycle via CLI

## Role

As an operator running rimsky-dispatched workflows on a dev machine, I can start the host-agent locally with `rimsky agent start`, check its connection status with `rimsky agent status`, and stop it cleanly with `rimsky agent stop` (children reaped), so that I manage the agent's lifecycle from the same CLI that drives the rimsky stack.

## Capability

Host-agent CLI control plane: `start` / `status` / `stop`. `start` launches connected to the configured proxy or refuses with a diagnostic; `status` reports connection state, configured proxy, and spawned children; `stop` SIGTERMs and reaps all children with the documented grace period.

## Business value

Operators manage the host-agent's lifecycle from the same CLI that drives the rimsky stack — no separate process-management ceremony.

## Acceptance

Through the `rimsky agent` CLI: `start` launches the agent connected to the configured proxy (or refuses with a clear diagnostic if proxy/auth aren't reachable); `status` reports the connection state, the configured proxy endpoint, and the list of currently-spawned children (per run-scope, per binding); `stop` SIGTERMs the agent, the agent reaps all spawned children with the documented grace period, and the agent exits cleanly.

## Falsifier

`stop` exits cleanly but leaves zombie children, OR `status` reports `connected` when the bidi stream is actually down, OR `start` silently succeeds with a misconfigured proxy URL.

## Proof

Demo.
