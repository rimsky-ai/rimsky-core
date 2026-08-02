---
concept: write-semantics
---

# Write semantics

## What it is

A per-claim, four-value enum family that determines how the coexistence matrix treats concurrent claims on byte-equal claim scope (per `concept:claim-scope`). Three-level structure: the producer advertises an allowed-values set through its capabilities; the operator declares a narrowing allowed-values set per producer in deployment config; each open verb returns one realized value in its acquisition result.

### Per-value semantics

The coexistence predicate is always asked with a single realized value. For a byte-equal-scope conflict, the byte-equal-scope uniformity invariant (see below) guarantees holder and candidate share the producer's realization for that scope. For a producer-defined scope-overlap conflict (see `concept:claim-scope`), the candidate has not yet acquired a realization of its own, so the predicate is evaluated against the holder's already-realized value alone. So each value defines its own (holder-intent × candidate-intent) sub-matrix; there is no cross-value cell.

The four-value family covers four points on the concurrency-vs-consistency spectrum:

- A synchronous in-place mode with no staging area: reader × reader coexists; reader × writer, writer × reader, and writer × writer all conflict.
- A staged-asynchronous mode in which writes go to a producer-internal staging area and readers see the pre-stage snapshot: reader × reader, reader × writer, and writer × reader all coexist; only writer × writer conflicts. Honest support requires snapshot delegation or native MVCC pass-through — the reader-lease internal-serialization pattern is forbidden.
- A blocking-asynchronous mode that stages writes but blocks readers until commit. The gate verdict matches the synchronous mode — readers cannot dispatch concurrently with a writer. The right answer when the producer can stage but cannot offer point-in-time snapshots to readers.
- A read-only mode in which the producer rejects any write attempt at open. Under byte-equal-scope uniformity every claim on this scope realizes read-only, so only reader × reader is ever reachable, and it coexists trivially.

## Purpose

A single per-binary capability is too coarse (a relational-store producer might support synchronous in-place writes for some resources and staged-asynchronous writes for others); per-claim with no upper bound is unbounded. The three-level allowed-values structure pins what the producer claims to support, what the operator allows, and what each specific claim got.

## Boundaries

Owns: the enum values, the envelope handshake, the realized-per-claim value, the conflict-matrix input. Does NOT own: claim-scope conflict comparison (see `concept:claim-scope`), claim disposition (see `concept:claim-producer`), per-claim payload (see `concept:claim`). Adjacent: `concept:claim`, `concept:claim-producer`, `concept:claim-scope`, `concept:claim-handle`, `concept:atomic-staging`.

## Invariants

- The operator-declared allowed set ⊆ producer-advertised set (validated at startup; fails fast).
- Every realized value returned by a remote producer's Open is checked against both the producer-advertised set and the operator-declared narrowing — a producer realizing a value inside its own advertised envelope but outside the operator's declared narrowing is rejected per-claim, not just at startup.
- The wire zero value of this enum must not be returned by producers; the supervisor rejects any acquisition that yields it.
- Byte-equal-scope uniformity: two open-verb calls with byte-equal claim scope MUST return the same realized value.
- Reader-lease internal serialization is forbidden for the staged-asynchronous mode — honest support requires snapshot delegation or MVCC pass-through.
