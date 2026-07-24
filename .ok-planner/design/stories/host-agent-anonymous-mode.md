---
story: host-agent-anonymous-mode
status: as-is
---

# Late-bind works under anonymous mode, safely with multiple agents

## Role

As an operator running a fresh anonymous-mode rimsky deployment (no api-keys minted yet) with one or more host-agents connected to it, I can register and dispatch to late-bound services from an anonymous-mode instance, targeting a specific connected agent, so that the dev-loop works without minting credentials AND multiple developers can share the same anonymous deployment without their runs interfering.

## Capability

Anonymous-mode-compatible host-agent routing that stays isolated across concurrent anonymous agents: an anonymous-mode instance's dispatches reach its target agent, and no other, with no agent-not-connected error caused by the instance having no owner api-key. Two concurrent anonymous agents on the same proxy never interfere — dispatches for one never reach the other.

## Business value

Operators dev-loop with host-agent late-binding without minting credentials first; multiple developers on a shared anonymous deployment can each run their own instances against their own agents without displacement or cross-talk.

## Acceptance

With rimsky stack in anonymous mode, the host-agent-proxy deployed (see `concept:host-agent-proxy`), and one or more host-agents connected (see `concept:host-agent`): an anonymous-mode instance dispatches through the proxy and reaches only its target agent — the late-bound child runs and returns the real dispatch outcome. When two anonymous agents are connected concurrently, dispatches for one's instances never reach the other; each agent's dispatch stream is isolated.

## Falsifier

Dispatch terminates with an agent-not-connected error despite the stamped target agent being connected; OR the dispatch reaches a different agent than the stamped target (routing mis-direct); OR two concurrent anonymous agents interfere with each other (a second anonymous agent's registration silently displaces the first, OR a dispatch aimed at one anonymous agent is delivered to another).

## Proof

Executable proof — a deterministic scenario test starts an anonymous-mode rimsky stack, connects two host-agents concurrently as anonymous, asserts each is admitted with a distinct routing identity; creates two anonymous-mode instances each targeting a different one of those agents; dispatches from each instance and asserts each reaches its own target agent (never the other) and returns the real dispatch outcome; disconnects and reconnects one agent and asserts previously-created instances targeting it continue to reach it after the reconnect.
