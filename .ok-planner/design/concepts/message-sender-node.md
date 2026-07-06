---
concept: message-sender-node
status: as-is
aliases: []
---

# Message-sender-node

## Definition

A message-sender-node is a node-type whose dispatch mode is "build a message envelope from the node's attributes and insert it into the message ledger." Declared on a node-type as a send-node by naming the destination message type, in place of an executor or delegate reference. The node carries the standard subscription and attribute blocks like any other node; what makes it an send-node is its dispatch field.

## Purpose

Make cascade-driven message send a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute substitution machinery: the send-node subscribes to multiple upstreams; its attributes pull from each; the resolved attribute set is the message body. No new validation, substitution, or routing primitive is introduced; every check reuses the existing attribute and subscription validators.

## Boundaries

Owns: the send-node dispatch mode, the exact-shape-match validation of attribute schema against the destination message type's body schema, the dispatch behavior (substitution resolves attributes, the runtime constructs the envelope with instance-origin sender attribution and a deterministic idempotency key keyed on the dispatching node and frame, the envelope inserts into the message ledger in its own transaction during the handler call), the registration-time check that the named message type exists in the template's message-schema registry. Does NOT own: the message envelope shape (see `concept:message`), the message-schema registry (see `concept:message-schema`), substitution into the attributes themselves (see `concept:attribute`), subscription declaration (see `concept:node-subscription`). Adjacent: `concept:node`, `concept:message`, `concept:message-schema`, `concept:attribute`, `concept:node-subscription`.

## Invariants

- The node's attribute schema matches the destination message type's body schema exactly: same field set, same types. Supersets are rejected at registration; the send-node exists to produce the message, hidden state is rejected.
- At dispatch, the runtime constructs the envelope with instance-origin sender attribution naming the dispatching instance, payload = resolved attribute set serialized, and a deterministic idempotency key keyed on `(node-id, frame-id)`. The envelope inserts in its own transaction during the handler call (envelope row + frame enqueue, atomic with each other); if that send-tx rolls back, no envelope lands. At-most-once delivery across the dispatch lifetime is preserved by the deterministic key, not by tx coupling with terminal-resolution: if the handler commits the envelope but the subsequent terminal-resolution tx fails and the dispatch retries, the next handler call hits the idempotency dedup and returns the original `message_id`, after which terminal-resolution proceeds normally.
- Cross-frame coupling is expressed solely through these nodes; no other path opens a frame.
