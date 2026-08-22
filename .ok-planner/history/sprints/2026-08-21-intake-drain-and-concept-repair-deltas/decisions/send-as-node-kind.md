---
decision: send-as-node-kind
---

# Emit-as-node-kind

## Choice

Cascade-driven message send lives on a dedicated node-kind: a node-type declares it with a message-send directive in place of an executor binding. A per-node-type emissions block is not introduced. The node-kind is the only in-graph path that opens a frame: a graph expresses cross-frame coupling through a message-sender node and through nothing else.

## Rationale

The emit becomes a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute machinery (the send-node subscribes to multiple senders; its attributes pull from each). No new validation or substitution machinery; every check reuses the existing attribute and subscription validators.

Because the node-kind is the one in-graph frame-opening path, a reader finds every frame-opening edge on the topology (see `concept:frame`, `concept:message-sender-node`). Operators and publishers also send messages, and those sends open frames, but they arrive from send sites outside the graph rather than from anything a template declares.

## Alternatives

- A per-node-type emissions block letting any node embed an emission directive — rejected: aggregation is unnatural (the only node where a multi-source body composes is one that coincidentally already had all sources as upstreams), the emit hides inside the sender's settle behavior rather than standing visible as a graph object, and any node could then open a frame, so the topology would no longer show every frame-opening edge.
