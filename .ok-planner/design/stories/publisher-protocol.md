---
story: publisher-protocol
status: as-is
---

# Service author writes custom publisher

## Role

As a service author writing a custom publisher (or sensor), I can implement the `concept:publisher` protocol — a capabilities advertisement plus the subscribe / unsubscribe / list-subscriptions verbs — advertise the message kinds I emit with their per-kind config schemas, accept rimsky's subscribe call carrying resolved per-instance config, and emit messages into rimsky through the universal message-emit surface with the mandatory dedup header, so that a custom publisher plugs into a rimsky stack and rimsky reconciles my subscriptions on restart through the list-subscriptions verb.

## Capability

Public publisher protocol surface — a capabilities advertisement plus the subscribe / unsubscribe / list-subscriptions verbs (see `concept:publisher`); message emit via the universal message-emit surface with a mandatory idempotency-key header (see `concept:message`); rimsky reconciles via the list-subscriptions verb on restart.

## Business value

A custom publisher plugs into a rimsky stack; on rimsky restart, the publisher's already-active subscriptions are not re-issued — `ListSubscriptions` lets rimsky reconcile back to steady state.

## Acceptance

A custom publisher implementing the protocol, registered with rimsky's publisher catalog, is referenced from a template's publisher binding; rimsky issues a subscribe call with resolved config; the publisher acknowledges and begins emitting messages to the rimsky message-emit surface; the messages reach the targeted instance and downstream nodes consume them. After a simulated rimsky restart, rimsky calls the publisher's list-subscriptions verb and reconciles back to the steady state without re-subscribing what's already there.

## Falsifier

Subscribe is acknowledged but messages never reach the message-emit surface, OR the post-restart reconcile re-subscribes already-active subscriptions, OR the publisher emits without the dedup header and is silently accepted.

## Proof

Example — a shipped publisher reference paired with a worked walkthrough that drives a real subscribe / publish / reconcile sequence against a running rimsky.
