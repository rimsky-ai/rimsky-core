---
story: host-agent-anonymous-mode
status: as-is
---

# Late-bind works under anonymous mode, safely with multiple agents

## Story

As an operator running a fresh anonymous-mode rimsky deployment (no api-keys minted yet) with one or more host-agents connected to it, I can register and dispatch to late-bound services from an anonymous-mode instance, targeting a specific connected agent, so that the dev-loop works without minting credentials AND multiple developers can share the same anonymous deployment without their runs interfering.

Anonymous-mode-compatible host-agent routing that stays isolated across concurrent anonymous agents: an anonymous-mode instance's dispatches reach its target agent, and no other, with no agent-not-connected error caused by the instance having no owner api-key. Two concurrent anonymous agents on the same proxy never interfere — dispatches for one never reach the other.

Operators dev-loop with host-agent late-binding without minting credentials first; multiple developers on a shared anonymous deployment can each run their own instances against their own agents without displacement or cross-talk.
