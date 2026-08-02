---
story: publisher-protocol
---

# Service author writes custom publisher

## Story

As a service author writing a custom publisher (or sensor), I can implement the `concept:publisher` protocol — a capabilities advertisement plus the subscribe / unsubscribe / list-subscriptions verbs — advertise the message kinds I send with their per-kind config schemas, accept rimsky's subscribe call carrying resolved per-instance config, and send messages into rimsky through the universal message-send surface with the mandatory dedup header, so that a custom publisher plugs into a rimsky stack and rimsky reconciles my subscriptions on restart through the list-subscriptions verb.

Public publisher protocol surface — a capabilities advertisement plus the subscribe / unsubscribe / list-subscriptions verbs (see `concept:publisher`); message send via the universal message-send surface with a mandatory idempotency-key header (see `concept:message`); rimsky reconciles via the list-subscriptions verb on restart.

A custom publisher plugs into a rimsky stack; on rimsky restart, the publisher's already-active subscriptions are not re-issued — the list-subscriptions verb lets rimsky reconcile back to steady state.
