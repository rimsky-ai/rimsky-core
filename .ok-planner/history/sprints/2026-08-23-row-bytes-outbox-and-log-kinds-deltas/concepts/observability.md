---
concept: observability
---

# Observability

## What it is

Observability is the pair of optional service protocols a service may implement, together with the startup handshake that probes them. One protocol belongs to each service kind: an executor exposes its trace surface, and a claim producer exposes its claim and inventory surfaces. Each protocol answers a capabilities query, so rimsky asks a service what it supports instead of assuming. A service implements at most the one protocol its kind matches. The startup handshake probes every declared service (see `concept:discovery-cache`). Observability is also where an executor declares the attribute keys it reads, as a closed contract (see `decision:expected-attributes-schema-closed`).

## Purpose

Observability lets a service describe itself. Rimsky learns a service's capabilities and its trace surface from the service, rather than carrying a fixed picture of every service it talks to. Rimsky learns this once and consults what it learned at later validation gates. An operator reaches a running service's traces, claims, and inventory through rimsky, without opening a separate connection to each service.

## Boundaries

Observability owns the two optional service protocols, the startup handshake, the policy for refreshing what the handshake learned, and the executor's declaration of the attribute keys it reads. It does not own the store the handshake fills: that is the discovery cache (see `discovery-cache`). It does not own the operator-facing view of a running graph (see `cascade-graph`), and it does not own the durable record of what happened (see `event-log`). An executor and a claim producer each carry their own definition (see `executor`, `claim-producer`). See also: `terminal-tag`.
