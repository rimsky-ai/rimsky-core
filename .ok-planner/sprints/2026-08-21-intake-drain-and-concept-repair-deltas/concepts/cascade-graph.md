---
concept: cascade-graph
aliases:
  - operator dashboard backplane
---

# Cascade graph

## What it is

Cascade graph is the read-only side of the control API: a family of routes that shows an operator rimsky's own persisted runtime state, and, for the status of a discovered peer, the discovery cache that the observability handshake fills (see `concept:observability`, `concept:discovery-cache`). No handler in the family mutates anything. This concept lists no route: the code settles which routes belong to the family. The per-instance read carries the cascade graph the concept is named for: the instance's nodes joined to their subscription edges, each node's run summary, and its last terminal event.

## Purpose

An operator, and a dashboard built on rimsky, needs to see what runs, what is wedged, which events have fired, and how cascade is propagating. Cascade graph exposes that state without coupling the consumer to rimsky's storage or to the per-service observability protocols.

## Boundaries

Cascade graph owns every handler in the read-only family; no neighbouring concept implements one. The per-service protocols that executors and stores answer belong to `concept:observability`. Writes to the audit log belong to `concept:event-log`. The mutating control-plane surface belongs to `concept:control-api`.

see also: `observability`, `control-api`, `event-log`, `discovery-cache`, `frame`, `node`

## Aliases

- operator dashboard backplane
