---
concept: claim-producer
status: as-is
aliases:
  - claim-store
---

# Claim producer

## What it is

A claim producer is an out-of-process service implementing the claim-producer protocol — four verbs plus a capabilities startup handshake.

The protocol carries three optional methods, each advertised in the capabilities handshake:

- **Split-scope** — partitions a claim's claim scope into sub-scopes for fan-out. Advertised via a split-scope capability flag. Rimsky opens one sub-claim per sub-scope at parent-acquisition time. Each sub-scope descriptor carries the same substrate-meaningful claim content a regular open returns (scope, address, payload) plus per-partition discriminators identifying the sub-scope within its parent. A sub-claim is a claim; substitution paths over a sub-claim resolve identically to those over a regular claim.
- **Scopes-conflict** — a producer-aware overlap predicate over two claim scopes. Advertised via a scopes-conflict capability flag. Producers that don't advertise default to byte-equal comparison (invariant 4b).
- **Validation (mix-in)** — the same validate request/response any service can advertise via the validation protocol. Validates a node's userdata at template-registration time against the producer's domain (claim bindings, scopes). Inert userdata per invariant 11 — rimsky forwards opaque bytes; receives a verdict. See `concept:validation`.

A fourth optional mix-in, the **data-processing protocol**, is the control-plane surface for typed-data version lifecycle: begin / commit / abandon a candidate, plus list-versions / list-partitions / get-version-schema. Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only. See `concept:data-processing`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical claim scope bytes; rimsky's conflict predicate is byte-equal. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state, the canonical claim-scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `claim-scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same service).

A producer may register the executor protocol alongside the claim-producer protocol on the same endpoint to support verification of its own staged content; see `concept:executor`.

## Invariants

- The claim-producer protocol — its verbs and capabilities handshake — is the only contract. Rimsky depends on the protocol; no concrete-producer dependency is permitted.
- Producers do not persist lock state (invariant 9a) and do not internally serialize on lock-shaped predicates (invariant 9b).
- Producers MUST satisfy byte-equal-claim-scope uniformity: two open calls returning byte-equal claim scope MUST also return the same realized write semantics.
- Terminal verbs (commit/abandon/release) must be idempotent in the claim identifier so the verb-then-transaction-fail leak path is recoverable.
