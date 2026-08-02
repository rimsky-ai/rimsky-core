---
story: claim-handoff-durable
---

# Template author wires a durable held claim that survives across instance dispatches

## Story

As a template author wiring an asset-producing topology — or any workflow whose claim must outlive a single instance dispatch — I can declare durable lifetime on the acquirer's claim, optionally have co-holders share it via the co-holdership directive within the producing dispatch, and trust that the claim handle row persists past auto-terminal (promoted to committed rather than reaped), so that future dispatches in the same instance can co-hold the same durable row by alias, the producer still occupies the scope, and release happens only on explicit operator action or instance termination.
