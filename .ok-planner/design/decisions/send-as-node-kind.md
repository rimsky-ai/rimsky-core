---
decision: send-as-node-kind
---

# Emit-as-node-kind

## Choice

Cascade-driven message send lives on a dedicated node-kind: a node-type declares it with a message-send directive in place of an executor binding. A per-node-type emissions block is not introduced.

## Rationale

The emit becomes a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute machinery (the send-node subscribes to multiple senders; its attributes pull from each). No new validation or substitution machinery; every check reuses the existing attribute and subscription validators.

## Alternatives

- A per-node-type emissions block letting any node embed an emission directive — rejected: aggregation is unnatural (the only node where a multi-source body composes is one that coincidentally already had all sources as upstreams), and the emit hides inside the sender's settle behavior rather than standing visible as a graph object.
