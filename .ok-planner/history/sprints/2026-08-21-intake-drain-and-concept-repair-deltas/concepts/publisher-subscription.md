---
concept: publisher-subscription
aliases:
  - sensor-watch
---

# Publisher-subscription

## What it is

A publisher-subscription is the binding between rimsky and one publisher for one instance and one message type. Rimsky creates the row when it creates an instance whose template declares that publisher, keeps it in a persisted ledger, and gives it an opaque identifier. The row moves to the stopped state when the instance terminates, and rimsky keeps it. The row is rimsky's half of the binding: the publisher holds whatever substrate state its own work needs, and rimsky holds which publisher serves which instance on which message type.

## Purpose

A publisher-subscription states that one publisher is committed to publish messages for one instance on one message type. The set of rows is desired state, so rimsky knows at any moment which publishers should be running and can drive publisher-side state toward that set (see `decision:subscription-reconciler`). The instance surface reports each subscription's state, so an operator watches a binding come up instead of inferring it from the instance's creation succeeding.

## Boundaries

A publisher-subscription owns the persisted row and everything on it: the identity, the lifecycle state, the reason a failed row carries, the message type, and the resolved configuration the publisher needs, which may include a secret. The identity is composite over the owning publisher's name and the subscription's own identifier, so each publisher's identifiers are scoped to that publisher rather than drawn from one namespace. The lifecycle state is one of mounting, active, failed, or stopped. A subscription names a publisher and a message type, never a receiver: delivery routes by message type against the receiver-side edges. A subscription is per publisher name, not per process. Its binding is fixed when rimsky creates it — a changed publisher declaration takes effect through a new instance, which mints new rows.

A publisher-subscription does not own the publisher's substrate state, which stays entirely the publisher's concern, nor the publisher's own persistence of what it is watching (see `concept:sensor`). It does not own the messages sent under its authority, which belong to `concept:message`, or the protocol the binding is negotiated over, which belongs to `concept:publisher`. The name distinguishes it from `concept:node-subscription`, which owns the receiver-side declaration one template node makes about a sibling's signal; the two are orthogonal.

see also: `publisher`, `sensor`, `message`, `node-subscription`, `instance`

## Aliases

- sensor-watch
