---
concept: publisher
---

# Publisher

## What it is

A publisher is a service that publishes messages into rimsky. It implements the publisher protocol, and it sends message envelopes to the one message-send surface rimsky offers, naming itself a publisher and presenting the capability token its subscription carries. A publisher is a service in the same trust perimeter as an executor or a claim-producer: it runs out of process, rimsky addresses it at the address the deployment declares for it, and it answers for its own state and its own availability. One publisher process serves many instances: each subscription provisions a logical, per-instance broadcaster inside it, parameterized by that instance's resolved configuration, the way an executor provides per-run execution. Rimsky coordinates nothing across whatever processes stand behind the declared address; whatever answers there is the publisher.

## Purpose

A publisher gives rimsky one uniform way to accept inbound messages from services, so no implementation needs a deposit surface of its own. The publisher protocol is the single message-send surface for services, and an operator sends a message through that same surface.

## Boundaries

A publisher owns the protocol surface, the client rimsky dials it with, the rimsky-side call path that drives that client, and the capability check rimsky applies when a message arrives on the message-send surface. A publisher names the message type it stamps on every envelope it sends, and never a receiver: delivery routes by message type against the receiver-side subscription edges (see `concept:node-subscription`).

A publisher does not own its own substrate — the clock, listener, or store its work rests on — nor the persistence of that substrate's state, which stays with the implementation (see `concept:sensor`). It does not own the envelope's shape, which belongs to `concept:message`, or the binding between itself and one instance and its progress from mounting to active, which belongs to `concept:publisher-subscription`. Payload bytes pass from the publisher through the envelope to the consumer's substitution leaf uninspected outside the sanctioned read sites (see `concept:inertness`).

see also: `publisher-subscription`, `sensor`, `message`, `node-subscription`, `claim-producer`, `executor`, `rimsky-yml`, `inertness`
