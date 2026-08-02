---
concept: observability
---

# Observability

## What it is

The service-facing optional observability protocols and the startup handshake that probes them. Two optional gRPC protocols total, one per service kind, each exposing a capabilities query: the executor-observability protocol, exposing single-trace fetch and trace-stream methods, and the claim-producer-observability protocol, exposing claim-detail fetch, claim-state streaming, claim-inventory pagination, and producer-declared admin views. A given service implements at most one, matching its kind. The handshake probes each declared service in parallel at rimsky startup, populating the discovery cache (see `concept:discovery-cache`). Also the canonical site for the executor-side `expected_attributes_schema` declaration (read from the handshake, applied at template registration and at dispatch post-merge/post-substitution).

## Purpose

Services declare their own capabilities and trace surfaces; rimsky should learn them once, cache the result, and consult the cache at validation gates. Keeping the protocol-side concept separate from the cache it populates (`discovery-cache`) and the operator-dashboard backplane (`cascade-graph`) keeps each concept's boundary sharp.

## Boundaries

Owns: the optional service protocols, the handshake mechanism, the refresh-loop policy, the executor-side `expected_attributes_schema` validation surface. Does NOT own: the cache the handshake populates (see `discovery-cache`), the operator-dashboard HTTP routes (see `cascade-graph`), the per-event audit log (see `event-log`). Adjacent: `discovery-cache`, `cascade-graph`, `executor`, `claim-producer`, `event-log`, `terminal-tag`.

## Invariants

- The handshake is best-effort: unreachable services are recorded with an unreachable status in `discovery-cache`; never aborts startup.
- The capabilities query is named uniformly across both observability protocols.
- The executor-side `expected_attributes_schema` validates at template registration AND at dispatch post-merge/post-substitution.
