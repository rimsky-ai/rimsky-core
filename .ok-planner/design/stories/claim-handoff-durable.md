---
story: claim-handoff-durable
status: as-is
---

# Template author wires a durable held claim that survives across instance dispatches

## Role

As a template author wiring an asset-producing topology — or any workflow whose claim must outlive a single instance dispatch — I can declare durable lifetime on the acquirer's claim, optionally have co-holders share it via the co-holdership directive within the producing dispatch, and trust that the claim handle row persists past auto-terminal (promoted to committed rather than reaped), so that future dispatches in the same instance can co-hold the same durable row by alias, the producer still occupies the scope, and release happens only on explicit operator action or instance termination.

## Capability

Durable lifetime on a claim causes the claim handle row to be promoted to state committed at holding-subgraph completion AND to be exempted from the retention sweep. The row stays present past the dispatch that produced it; conflict detection includes it (the producer still occupies the scope); future dispatches can declare co-holdership for the same alias against the upstream durable claim and read the claim's address, payload fields, or claim-scope through the alias-keyed substitution against the persisted handle. Released only by the asset-release control endpoint or by instance termination's held-durable-release path.

## Business value

Workflows whose data outputs are consumed by future dispatches — assets, re-materializable artifacts, "build once, co-hold many times" patterns — compose naturally. The author chooses lifetime in the template; rimsky's persistence layer enforces survival without per-template bookkeeping. Paired with `story:asset-management`, the durable claim becomes the writable counterpart to the operator's readable asset surface.

