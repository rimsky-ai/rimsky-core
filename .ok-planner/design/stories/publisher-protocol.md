---
story: publisher-protocol
status: as-is
---

# Service author writes custom publisher

## Role

As a service author writing a custom publisher (or sensor), I can implement the gRPC `Publisher` server (`Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`), advertise the message kinds I emit with their per-kind config schemas, accept rimsky's `Subscribe` carrying resolved per-instance config, and emit messages into rimsky through `POST /instances/{id}/messages` with the mandatory dedup header, so that a custom publisher plugs into a rimsky stack and rimsky reconciles my subscriptions on restart through `ListSubscriptions`.

## Capability

Public publisher protocol surface (`Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`); message emit via the universal `POST /instances/{id}/messages` endpoint with mandatory `Idempotency-Key`; rimsky reconciles via `ListSubscriptions` on restart.

## Business value

A custom publisher plugs into a rimsky stack; on rimsky restart, the publisher's already-active subscriptions are not re-issued — `ListSubscriptions` lets rimsky reconcile back to steady state.

## Acceptance

A custom publisher implementing the protocol, registered with rimsky's publisher catalog, is referenced from a template's publisher binding; rimsky issues a `Subscribe` with resolved config; the publisher acknowledges and begins emitting messages to the rimsky message endpoint; the messages reach the targeted instance and downstream nodes consume them. After a simulated rimsky restart, rimsky calls `ListSubscriptions` on the publisher and reconciles back to the steady state without re-subscribing what's already there.

## Falsifier

Subscribe is acknowledged but messages never reach the message endpoint, OR the post-restart reconcile re-subscribes already-active subscriptions, OR the publisher emits without the dedup header and is silently accepted.

## Proof

Example — the examples module's publisher reference extended with a worked walkthrough that drives a real subscribe / publish / reconcile sequence against a running rimsky.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
