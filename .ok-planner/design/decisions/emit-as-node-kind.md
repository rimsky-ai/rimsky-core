---
decision: emit-as-node-kind
status: as-is
---

# Emit-as-node-kind

## Choice

Cascade-driven message emission lives on a dedicated node-kind, declared on a node-type by `emits_message: <type>` instead of `executor:`. A per-node-type `emits:` block is not introduced.

## Rationale

The emit becomes a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute machinery (the emit-node subscribes to multiple senders; its attributes pull from each). No new validation or substitution machinery; every check reuses the existing attribute and subscription validators.

## Alternatives considered

A per-node-type `emits:` block in which any node could embed an emission directive. Rejected: aggregation is unnatural (the only node where a multi-source body composes is one that already had all sources as upstreams, coincidental rather than designed); the emit is hidden inside the sender's settle behavior rather than visible as a graph object.
