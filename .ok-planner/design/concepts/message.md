---
concept: message
---

# Message

## What it is

A message is a typed envelope whose arrival at an instance enqueues it on that instance's message queue. The frame engine picks a message up when the instance is idle, or once its running frame settles, and that message opens the next frame. The envelope's type selects an entry from the instance's template registry (see `concept:message-schema`); an undeclared type is refused at receipt. Rimsky persists the envelope on receipt and delivers it at frame open. A cascade-sent, an operator-sent, and a publisher-sent message all travel the same enqueue-then-pickup path.

The envelope carries an identity, the target instance, the typed body, a receipt timestamp, and sender attribution — the sender kind, the sender, and the sender-subject (see `decision:message-sender-kind-discriminator`). It carries no routing field; subscription to the message type as a node type decides the receivers. Every message type materializes one message-receiver node at instance creation, the author-declared types and the runtime's implicit empty type alike. Each delivery creates a run for that node whose attribute bag holds the message body. That run settles on cascade alone, and the standard cascade walk marks its subscribers stale. A structural root reaches the empty-type receiver through runtime-injected subscription edges.

## Purpose

A message is how work enters a running instance. The typed envelope makes the receiver an ordinary graph node, so an operator, a publisher, and a sending node all reach the graph the same way, and a receiver reads a message body with the substitution it already uses for any upstream attribute.

## Boundaries

A message owns the envelope shape and the message ledger, the one-message-per-frame delivery rule, the materialization of one message-receiver node per type at instance creation, the creation of that node's run at delivery, the dead-letter path for a message whose receiver node is missing, the idempotency dedup ledger, and the receipt-time lookup against the type registry. Every send surface requires an idempotency key (see `decision:idempotency-key-header-universal`). A message does not own the type registry (see `concept:message-schema`), the instance-scoped queue and its coalesce mode (see `concept:instance`), cascade within a frame (see `concept:cascade`), frame creation (see `concept:frame`), a publisher's substrate state (see `concept:publisher`, `concept:publisher-subscription`), a sender node's dispatch (see `concept:message-sender-node`), or the dispatch and settlement of the receiver node itself (see `concept:node-run`).

A message body is inert (see `concept:inertness`). Rimsky reads it at the substitution leaf, at the persistence fetches that surface stored messages, when it fills the receiver node's attribute bag at delivery, and at the receipt-time body-shape validation that gates every insertion into the ledger.

see also: `concept:frame`, `concept:instance`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:message-schema`, `concept:message-sender-node`, `concept:node`, `concept:node-run`, `concept:cascade`, `concept:inertness`.
