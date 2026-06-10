---
concept: discovery-cache
status: as-is
aliases:
  - capabilities cache
---

# Discovery cache

## What it is

An in-memory per-service capabilities cache populated by the observability handshake at startup. Indexed by service name; entry shape includes the service's declared events, observability-protocol availability, and a reachability status (reachable / unreachable).

## Purpose

The capabilities each service declares are needed at template registration (for the subscription declared-events cross-check) and at runtime fallback decisions (unknown event names treated as no-ops if the service was unreachable at registration). Probing services synchronously at every check would couple registration latency to service availability. The discovery cache decouples them: probe at startup, cache, refresh on a loop, check against cache.

## Boundaries

Owns: the in-memory cache structure, the per-service entry shape, the registration-time consult path, the reachability status. Does NOT own: the handshake invocation (see `observability`), the executor/store observability protocols themselves (see `observability`), the runtime unknown-event-as-no-op fallback (see `node-subscription`). Adjacent: `observability`, `node-subscription`, `executor`, `claim-producer`.

## Invariants

- Best-effort fill: unreachable services are recorded with an unreachable status and never abort startup.
- Reads are eventually-consistent; the refresh loop updates entries on its own cadence.
- The cache is in-memory only; restart resets state to a fresh handshake pass.
