---
story: cascade-emit
status: as-is
---

# Template author declares a message-emitter node-type

## Role

As a template author,

## Capability

I can declare a node-type whose dispatch is to emit a message of a given type,

## Business value

so that cross-frame coupling is explicit as a graph object I can point at.

## Acceptance

I write a node-type carrying `emits_message: <type>` (rather than `executor:` or `delegate:`). The node has `subscribes:` and `attributes:` blocks like any other node; its attribute schema matches the destination message type's body schema exactly. When its `subscribes:` entries fire, the node dispatches: substitution resolves its attributes from upstreams and instance params, the runtime constructs a message envelope with the resolved attribute set as the body, and inserts it into the message ledger. The next frame opens carrying that message.

## Falsifier

A subscribed condition triggers and the emit-node's dispatch produces no message in the ledger; OR the emit-node's attribute schema can declare fields the destination message type's body schema doesn't, without registration-time error; OR the body fails to reflect the resolved attribute values.

## Proof

Executable proof. Emit-node dispatches when its subscriptions fire; resulting message body contains the expected substituted values; mismatched schemas reject at registration.
