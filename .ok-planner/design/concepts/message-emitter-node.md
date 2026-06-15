---
concept: message-emitter-node
status: as-is
aliases: []
---

# Message-emitter-node

## Definition

A message-emitter-node is a node-type whose dispatch mode is "build a message envelope from the node's attributes and insert it into the message ledger." Declared on a node-type by `emits_message: <type>` instead of `executor: <name>` or `delegate: <graph-name>`. The node carries `subscribes:` and `attributes:` blocks like any other node; what makes it an emit-node is its dispatch field.

## Purpose

Make cascade-driven message emission a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute substitution machinery: the emit-node subscribes to multiple upstreams; its attributes pull from each; the resolved attribute set is the message body. No new validation, substitution, or routing primitive is introduced; every check reuses the existing attribute and subscription validators.

## Boundaries

Owns: the `emits_message:` node-type field, the exact-shape-match validation of attribute schema against the destination message type's body schema, the dispatch behavior (substitution resolves attributes, the runtime constructs the envelope with `sender_kind: "instance"` and a runtime-generated `Idempotency-Key`, the envelope inserts into the message ledger inside the node's terminal-resolution tx), the registration-time check that the named message type exists in the template's `messages:` registry. Does NOT own: the message envelope shape (see `concept:message`), the message-schema registry (see `concept:message-schema`), substitution into the attributes themselves (see `concept:attribute`), subscription declaration (see `concept:node-subscription`). Adjacent: `concept:node`, `concept:message`, `concept:message-schema`, `concept:attribute`, `concept:node-subscription`.

## Invariants

- The node's attribute schema matches the destination message type's `body_schema` exactly: same field set, same types. Supersets are rejected at registration; the emit-node exists to produce the message, hidden state is rejected.
- At dispatch, the runtime constructs the envelope with `sender_kind: "instance"`, sender `instance:<id>`, payload = resolved attribute set serialized, `Idempotency-Key` deterministic on the dispatching node-run's `node_run_id`. The envelope inserts in the same tx as the node's terminal-resolution; if the tx rolls back, the emit does not occur.
- Cross-frame coupling is expressed solely through these nodes; no other path opens a frame.
