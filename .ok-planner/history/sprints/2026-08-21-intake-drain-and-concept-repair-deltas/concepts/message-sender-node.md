---
concept: message-sender-node
---

# Message-sender-node

## What it is

A message-sender-node is a node kind whose dispatch builds a message envelope from the node's own attributes and inserts it into the message ledger. A template declares one by naming a destination message type in the node's dispatch field, in place of an executor or a delegate reference. The node carries the same subscription and attribute blocks as any other node; the dispatch field is what makes it a sender.

## Purpose

A message-sender node makes cascade-driven message send a first-class graph object, visible in topology, in the audit log, and on the operator dashboard. Aggregation across senders needs nothing new: the node subscribes to several upstreams, its attributes pull from each, and the resolved attribute set becomes the message body. The kind adds no validation, substitution, or routing primitive of its own, and the existing attribute and subscription validators cover it.

## Boundaries

A message-sender node owns its dispatch mode, the exact-shape match of its attribute schema against the destination type's body shape, the dispatch behavior that turns the resolved attributes into an envelope in the message ledger, and the registration-time check that the named message type is declared in the template's registry. It does not own the envelope shape (see `concept:message`), the type registry (see `concept:message-schema`), substitution into the attributes themselves (see `concept:attribute`), or subscription declaration (see `concept:node-subscription`).

Cross-frame coupling expressed from inside the graph runs solely through these nodes, and no other in-graph path opens a frame (see `decision:send-as-node-kind`). An operator send and a publisher send open frames too, but from outside the graph (see `concept:message`).

see also: `concept:node`, `concept:message`, `concept:message-schema`, `concept:attribute`, `concept:node-subscription`, `concept:frame`.
