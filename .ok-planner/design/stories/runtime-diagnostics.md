---
story: runtime-diagnostics
status: as-is
---

# Operator inspects runtime wedge state

## Role

As an operator, I can inspect the parked nodes, the live wait-set edges, the frames a holding-subgraph is gripping, and the current holders of a claim, so that I see why the runtime is wedged when an instance isn't progressing.

## Capability

Runtime-diagnostics read surfaces: parked nodes, wait-set edges, held frames, claim holders, through the control-api or MCP.

## Business value

Operators see why the runtime is wedged when an instance isn't progressing — without ad-hoc database spelunking.

## Acceptance

With an instance whose nodes are parked, gated on senders in the wait-set, and holding a claim, the operator queries the parked-node, wait-set, held-frames, and claim-holders surfaces through the control-api or MCP and sees the parked nodes with resume reason, the receiver-waiting-for-sender edges the supervisor is actually consulting, the held frames, and the current holders.

## Falsifier

A parked node that's really parked isn't on the parked surface, OR a wait-set edge the supervisor is consulting is missing from the wait-set surface.

## Proof

Executable proof.
