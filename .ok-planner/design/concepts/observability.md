---
concept: observability
status: as-is
aliases: []
references:
  - _discover/2026-05-10-observability-optional-protocols.md
---

# Observability

## What it is

The peer-facing optional observability protocols and the startup handshake that probes them. Two optional gRPC protocols per peer (`ExecutorObservability`, `StoreObservability`) exposing `Capabilities` / `GetTrace` / `StreamTrace`. The handshake (`modeling/observability/handshake.go`) probes each declared peer in parallel at rimsky startup, populating the `discovery-cache`. Also the canonical site for the per-peer `userdata_schema` declaration (read from the handshake, applied at template registration and at dispatch post-merge/post-substitution).

## Purpose

Peers declare their own capabilities and trace surfaces; rimsky should learn them once, cache the result, and consult the cache at validation gates. Keeping the protocol-side concept separate from the cache it populates (`discovery-cache`) and the operator-dashboard backplane (`cascade-graph`) keeps each concept's boundary sharp.

## Boundaries

Owns: the optional peer protocols, the handshake mechanism, the refresh-loop policy, the per-peer `userdata_schema` validation surface. Does NOT own: the cache the handshake populates (see `discovery-cache`), the operator-dashboard HTTP routes (see `cascade-graph`), the per-event audit log (see `event-log`). Adjacent: `discovery-cache`, `cascade-graph`, `executor`, `claim-producer`, `event-log`, `named-event`.

## Invariants

- The handshake is best-effort: unreachable peers recorded as `Unreachable` in `discovery-cache`; never aborts startup.
- Per-peer `userdata_schema` validates at template registration AND at dispatch post-merge/post-substitution.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered the cascade-graph HTTP routes and the discovery cache; those are now `cascade-graph` and `discovery-cache` respectively.

## Open within this concept

- `userdata_schema` placement on the observability protocol (read by rimsky to validate userdata bytes at template-registration and dispatch time) sits in tension with `@blessed-invariant 11` opacity — see `tensions/userdata-schema-as-opacity-exception.md`.
