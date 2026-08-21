---
concept: write-semantics
---

# Write semantics

## What it is

Write semantics is a per-claim value from a closed family that tells the coexistence matrix how to treat two concurrent claims on byte-equal claim scope (see `concept:claim-scope`). Three levels settle it: the producer advertises through its capabilities the set of values it supports, the operator declares a narrowing set per producer in deployment configuration, and each open verb returns one realized value in its acquisition result (see `decision:write-semantics-three-level-structure`).

The coexistence predicate is always asked with a single realized value, so each value defines its own holder-intent by candidate-intent sub-matrix and no cell crosses two values. On a byte-equal-scope conflict, holder and candidate share the producer's realization for that scope, because a producer realizes one value for one scope. On a producer-defined scope-overlap conflict (see `concept:claim-scope`), the candidate has realized no value of its own yet, so the predicate reads the holder's realized value alone.

The family covers four points on the concurrency-versus-consistency spectrum. A synchronous in-place mode keeps no staging area: two readers coexist, while reader against writer, writer against reader, and writer against writer all conflict. A staged-asynchronous mode sends writes to a producer-internal staging area and shows readers the pre-stage snapshot: two readers coexist, reader and writer coexist in either order, and only writer against writer conflicts (see `decision:write-semantics-reader-lease-forbidden`). A blocking-asynchronous mode stages writes but blocks readers until commit, so its gate verdict matches the synchronous mode; it is the answer where a producer can stage but cannot offer readers a point-in-time snapshot. A read-only mode rejects any write at open, so every claim on such a scope realizes read-only, only reader against reader is ever reachable, and that pair coexists trivially.

## Purpose

Write semantics lets one producer serve resources with different concurrency guarantees without misdescribing any of them. A producer that stages writes for one resource and writes another in place says so per claim; the operator narrows what the deployment accepts from that producer; and the supervisor reads one realized value off each acquisition and asks the coexistence predicate with it (see `decision:write-semantics-three-level-structure`).

## Boundaries

Write semantics owns the value family, the handshake that carries the advertised set and the operator's narrowing, the realized per-claim value, and the input that value gives the conflict matrix. Comparing claim scopes for conflict is out (see also `concept:claim-scope`). The claim's disposition is out (see also `concept:claim-producer`), and so is the per-claim payload (see also `concept:claim`). What a producer must do internally to support the staged-asynchronous mode honestly binds producer authors rather than rimsky's own code (see `decision:write-semantics-reader-lease-forbidden`).

See also `concept:claim-handle`, `concept:atomic-staging`.
