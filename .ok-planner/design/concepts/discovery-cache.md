---
concept: discovery-cache
status: as-is
aliases:
  - capabilities cache
references:
  - _discover/observability-handshake-discovery-cache.md
---

# Discovery cache

## What it is

An in-memory per-peer `Capabilities` cache populated by the observability handshake at startup. Lives in `modeling/observability/discovery.go` (and the handshake fill path in `handshake.go`). Indexed by peer name; entry shape includes the peer's `declared_events`, observability-protocol availability, and a status enum (`Reachable | Unreachable`).

## Purpose

The capabilities each peer declares are needed at template registration (for the `on_event` declared-events cross-check) and at runtime fallback decisions (unknown event names treated as no-ops if peer was unreachable at registration). Probing peers synchronously at every check would couple registration latency to peer availability. The discovery cache decouples them: probe at startup, cache, refresh on a loop, check against cache.

## Boundaries

Owns: the in-memory cache structure, the per-peer entry shape, the registration-time consult path, the reachability status. Does NOT own: the handshake invocation (see `observability`), the executor/store observability protocols themselves (see `observability`), the runtime unknown-event-as-no-op fallback (see `on-event-handler`). Adjacent: `observability`, `on-event-handler`, `executor`, `claim-producer`.

## Invariants

- Best-effort fill: unreachable peers are recorded as `Unreachable` and never abort startup.
- Reads are eventually-consistent; the refresh loop updates entries on its own cadence.
- The cache is in-memory only; restart resets state to a fresh handshake pass.

## Aliases and historical names

The cache and its handshake population were previously documented inside `observability`; promoted to its own concept under the `2026-05-11-design-log-convergence` spec.

## Open within this concept

(no live tensions.)
