---
story: host-agent-anonymous-mode
status: as-is
---

# Late-bind works under anonymous mode

## Role

As an operator running a fresh anonymous-mode rimsky deployment (no api-keys minted yet) and a `rimsky agent` connected to it, I can register and dispatch to late-bound services from an instance created in anonymous mode, so that the dev-loop with host-agent late-binding doesn't require me to mint credentials first.

## Capability

Anonymous-mode-compatible host-agent routing: late-bind dispatches from anonymous-mode instances reach the connected agent and spawned child, no `host_agent_not_connected` because the instance owner is NULL.

## Business value

Operators dev-loop with host-agent late-binding without minting credentials first; the dev-friendly anonymous mode applies end-to-end.

## Acceptance

With rimsky stack in anonymous mode, a `rimsky-host-agent-proxy` deployed, and `rimsky agent` connected: an anonymous-mode instance referencing a late-bound binding dispatches through the proxy and reaches the connected agent; the late-bound child runs and returns real dispatch outcome rather than the dispatch terminating with `host_agent_not_connected` because the instance owner is NULL.

## Falsifier

Dispatch terminates with `host_agent_not_connected` despite the agent being connected, OR the dispatch reaches a different agent (anonymous-mode routes mis-direct).

## Proof

Executable proof.
