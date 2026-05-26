---
concept: observability
status: as-is
aliases: []
references:
  - _discover/2026-05-10-observability-optional-protocols.md
---

# Observability

## What it is

The service-facing optional observability protocols and the startup handshake that probes them. Two optional gRPC protocols per service — the executor-observability protocol and the claim-producer-observability protocol — each exposing a capabilities query plus single-trace fetch and trace-stream methods. The handshake probes each declared service in parallel at rimsky startup, populating the discovery cache (see `concept:discovery-cache`). Also the canonical site for the per-service `userdata_schema` declaration (read from the handshake, applied at template registration and at dispatch post-merge/post-substitution).

## Purpose

Services declare their own capabilities and trace surfaces; rimsky should learn them once, cache the result, and consult the cache at validation gates. Keeping the protocol-side concept separate from the cache it populates (`discovery-cache`) and the operator-dashboard backplane (`cascade-graph`) keeps each concept's boundary sharp.

## Boundaries

Owns: the optional service protocols, the handshake mechanism, the refresh-loop policy, the per-service `userdata_schema` validation surface. Does NOT own: the cache the handshake populates (see `discovery-cache`), the operator-dashboard HTTP routes (see `cascade-graph`), the per-event audit log (see `event-log`). Adjacent: `discovery-cache`, `cascade-graph`, `executor`, `claim-producer`, `event-log`, `named-event`.

## Invariants

- The handshake is best-effort: unreachable services are recorded with an unreachable status in `discovery-cache`; never aborts startup.
- The capabilities query is named uniformly across both observability protocols (per `spec:2026-05-12-nomenclature-resolution` Group E.11 / B.4); pre-2026-05-12 the executor side and the store side used divergent names.
- Per-service `userdata_schema` validates at template registration AND at dispatch post-merge/post-substitution.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered the cascade-graph HTTP routes and the discovery cache; those are now `cascade-graph` and `discovery-cache` respectively.

## Resolved within this concept

- The `userdata_schema` placement on the observability protocol (read by rimsky to validate userdata bytes at template-registration and dispatch time), which sat in tension with `@blessed-invariant 11` opacity, was resolved — see `tension:_resolved/userdata-schema-as-opacity-exception`.

## Notes

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
