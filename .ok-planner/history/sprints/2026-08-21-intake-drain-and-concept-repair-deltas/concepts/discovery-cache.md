---
concept: discovery-cache
---

# Discovery cache

## What it is

The discovery cache is an in-memory record of what each peer service declares about itself, held by the process that checks those declarations. Two paths fill it. The observability handshake reaches out-of-process services at startup, and the bundled registration path writes an entry directly for a service running in this process, bypassing the handshake. Both paths land on the same surface, and rimsky-side capability queries read that surface. The cache holds one index per peer kind, one for executors and one for claim producers, each keyed by service name within its kind. An entry carries what its service declares, and whether the service answered at the last look. The cache lives in memory alone.

## Purpose

The discovery cache decouples a capability check from a service being reachable at the moment of the check. Rimsky consults peer declarations at template registration — for the payload-tag cross-check against a node subscription's payload predicate, the error-class vocabulary check, and the expected-attributes schema gate — and again at dispatch, to resolve the expected-attributes schema. Reading a cached declaration keeps those checks off the network, and the refresh loop keeps the record close to what the peers declare now.

## Boundaries

The discovery cache owns the cached structure, the shape of a per-service entry, the path registration consults, and the reachability status it records. It does not own the handshake that fills it, nor the peer observability protocols that handshake speaks (see `concept:observability`).

see also: `observability`, `node-subscription`, `executor`, `claim-producer`
