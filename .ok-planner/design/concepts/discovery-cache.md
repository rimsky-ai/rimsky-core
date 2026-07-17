---
concept: discovery-cache
status: as-is
aliases:
  - capabilities cache
---

# Discovery cache

## What it is

An in-memory capabilities cache, populated by two paths: the observability handshake at startup for out-of-process services, and the bundled registration entrypoint for in-process bundled services (bypassing the handshake). Both paths converge on the same cache surface consumed by rimsky-side capability queries. The cache holds two kind-partitioned indexes, one for executors and one for claim producers, each keyed by service name within its kind. An entry's shape includes its declared tags, declared error classes, its expected-attributes schema, observability-protocol availability, and a reachability status (reachable / unreachable). The bundled registration path advertises an in-proc executor handler's schema, tags, and declared error classes; an in-proc claim-producer handler advertises declared error classes only. Entries written by the registration path are marked static: the refresh loop skips them, since there is no endpoint to probe and an in-process handler cannot become unreachable within its own process.

## Purpose

The capabilities each service declares are needed at template registration — for the payload-tag cross-check against a node-subscription's payload predicate, the error-class vocabulary check, and the expected-attributes schema gate — and at dispatch time, to resolve the expected-attributes schema. Probing services synchronously at every check would couple registration latency to service availability. The discovery cache decouples them: probe at startup, cache, refresh on a loop, check against cache.

## Boundaries

Owns: the in-memory cache structure, the per-service entry shape, the registration-time consult path, the reachability status. Does NOT own: the handshake invocation (see `observability`), the executor/store observability protocols themselves (see `observability`). Adjacent: `observability`, `node-subscription`, `executor`, `claim-producer`.

## Invariants

- Best-effort fill: unreachable services are recorded with an unreachable status and never abort startup.
- Reads are eventually-consistent; the refresh loop updates entries on its own cadence.
- The cache is in-memory only; restart resets state to a fresh handshake pass.
