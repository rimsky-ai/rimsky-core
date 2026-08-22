---
decision: multi-protocol-service-distinct-handler-per-protocol
---

# A multi-protocol service binary uses one handler per protocol

## Choice

A binary implementing several rimsky protocols implements a distinct handler per protocol and registers each separately with its serving stack. Rimsky defines no shared capabilities-provider abstraction across protocols. A method-name collision across two protocols resolves at the composition site (see `concept:service`).

## Rationale

Each protocol's capabilities response has its own shape, and the code that consumes it is already protocol-specific. A shared provider would widen to the union of those shapes and then narrow again at every consumer, which adds a hop and buys no reuse. Separate handlers also let a binary add or drop one protocol without touching the others.

## Alternatives

- One capabilities provider serving every protocol a binary implements — rejected: the response shapes differ per protocol, so the provider widens to their union and each consumer narrows it back.
