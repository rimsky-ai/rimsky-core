---
concept: message
status: as-is
aliases: []
---

# Message

## Definition

A typed envelope whose arrival at an instance opens a frame. The envelope's type selects an entry from the instance's template message-schema registry; an undeclared type is refused at receipt with an unknown-type response. Persisted in the message ledger on receipt; delivered to subscribers at the next frame boundary, one message per frame. Cascade-emitted, operator-emitted, and publisher-emitted messages traverse the same delivery path.

The envelope carries an identity, the target instance, the typed body (inert), a receipt timestamp, and sender attribution (a sender identifier plus a sender-kind discriminator that distinguishes the three origin classes — operator, publisher, instance). Receivers are decided by subscription to the message type as a virtual node-type — there is no envelope-side routing field.

## Idempotency

The message-emit endpoint requires an idempotency key on every request; requests without one are refused. Rimsky computes a dedup tuple over the target instance, the requester's identity (distinct callers with the same key never replay each other), and the supplied key, then writes a dedup-ledger entry under uniqueness; on conflict the handler returns the original message identity, with the replay distinguished from a fresh insert at the transport-status layer (response body shape is identical). Dedup records expire on a configurable trailing window swept under the scheduler-tick advisory lock. See `decision:message-sender-kind-discriminator` for the relationship between the dedup-layer sender-kind discriminator and the envelope-side sender-kind.

The idempotency feature is universal — operator retries, publisher emissions, and lifecycle handlers all use the same idempotency-key surface.

## Boundaries

Owns: the envelope shape and the message ledger; the one-message-per-frame delivery rule; the subscription-walk-as-virtual-node at frame boundary (each message type is a virtual node-type emitting a success terminal on arrival); the dead-letter audit (no-subscriber landings still write a ledger row with a success-terminal emission); the universal idempotency-key dedup ledger; the registry lookup gate on receipt. Does NOT own: the type registry itself (see `concept:message-schema`); cascade walks within a frame (see `concept:cascade`); the frame creation mechanics (see `concept:frame`); the publisher's substrate state (see `concept:publisher` / `concept:publisher-subscription`); the emit-node's dispatch (see `concept:message-emitter-node`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:message-schema`, `concept:message-emitter-node`.

## Invariants

- Two external emit sites and one internal: operator API (the message-emit endpoint with operator-origin sender attribution), publisher emissions (the same endpoint with publisher-origin sender attribution plus a publisher-subscription capability token), and cascade-emit (a message-emitter node's dispatch, with instance-origin sender attribution naming the dispatching instance). All three paths land in the same ledger and follow the same delivery rules. The instance-origin sender attribution is unambiguously cascade-emit; the runtime synthesizes no envelopes.
- One message per frame. At each frame boundary, exactly one pending message delivers; the rest stay pending until the next frame.
- Type lookup at receipt: a message whose type is not declared in the target template's message-schema registry is refused with an unknown-type response; loud miss, not silent dead-letter. Every template's declared-types set carries an implicit empty-type entry seeded at registration, so empty-typed messages pass receipt under the same uniform check.
- Delivery at frame boundary: the message-virtual-node settles in the new frame and emits a success terminal; nodes subscribing to that virtual node-type stale-mark; the message's delivery timestamp and frame reference populate.
- Payload is inert (see invariant: 21). Read only at the substitution leaf and the persistence-layer fetch.
- Publisher requests are capability-checked at the existing publisher-subscription validation: rimsky validates that the publisher-subscription is a live, active binding for the target instance.
- The message-body substitution directive is sugar for the node-attribute substitution directive against the message's virtual node-type — both resolve through the same lookup. The only difference is a registration-time check that the named type is declared in the template's message-schema registry, where the node-form requires the name to be declared as a node-type. The substitution-ref coverage check of `decision:substitution-ref-coverage-required` treats the two directives identically.
