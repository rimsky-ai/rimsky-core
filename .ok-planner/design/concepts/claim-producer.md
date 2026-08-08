---
concept: claim-producer
aliases:
  - claim-store
---

# Claim producer

## What it is

A claim producer is an implementation of the claim-producer protocol — four verbs plus capability advertisement — running either as an out-of-process service (capabilities via the startup handshake) or as an in-process bundled handler registered inside the rimsky all-in-one process (capabilities declared at registration). Both shapes are dispatched through the same protocol surface.

The protocol carries two optional methods on the claim-producer service itself, each gated by its own capability flag:

- **Split-scope** — partitions a claim's claim scope into sub-scopes for fan-out. Advertised via a split-scope capability flag. Rimsky opens one sub-claim per sub-scope at parent-acquisition time. Each sub-scope descriptor carries the same substrate-meaningful claim content a regular open returns (scope, address, payload) plus per-partition discriminators identifying the sub-scope within its parent. A sub-claim is a claim; substitution paths over a sub-claim resolve identically to those over a regular claim. Where a producer partitions by picking items from a shared pool, each pick policy declares the item's disposition at the claim's terminal — one action applied at commit, another at abandon — so rimsky's terminal verbs, not producer-side timing, decide when a picked item is consumed or returned.
- **Scopes-conflict** — a producer-aware overlap predicate over two claim scopes. Advertised via a scopes-conflict capability flag. Producers that don't advertise default to byte-equal comparison.

Two further optional mix-ins are separate sibling protocols advertised through the capabilities list rather than a dedicated flag:

- **Validation** — the same validate request/response any service can advertise via the validation protocol. Validates claim bindings at template-registration time against the producer's domain (selector, intent, lifetime, scope). See `concept:validation`.
- **Data-processing** — the control-plane surface for typed-data version lifecycle: begin / commit / abandon a candidate, plus list-versions / list-partitions / get-version-schema. Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only. See `concept:data-processing`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical claim scope bytes; rimsky's default conflict predicate is byte-equal, and a producer that needs richer overlap semantics supplies its own predicate via the scopes-conflict capability. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state, the canonical claim-scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `claim-scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same service).

For the in-process shape, the handler exposes the full protocol — the four core verbs, any implemented mix-ins (split-scope, scopes-conflict, validation, data-processing), and capability advertisement — through a handler-side interface consumed by the in-process dispatch path. For the out-of-process shape, the same surface is served over gRPC. See `decision:parallel-inproc-claim-producer-registry`.

A producer may register the executor protocol alongside the claim-producer protocol on the same endpoint to support verification of its own staged content; see `concept:executor`.

## Invariants

- The claim-producer protocol — its verbs and capabilities handshake — is the only contract. Rimsky depends on the protocol; no concrete-producer dependency is permitted.
- Producers do not persist lock state (invariant 9a) and do not internally serialize on lock-shaped predicates (invariant 9b).
- Producers MUST satisfy byte-equal-claim-scope uniformity: two open calls returning byte-equal claim scope MUST also return the same realized write semantics.
- Terminal verbs (commit/abandon/release) must be idempotent in the claim identifier: rimsky delivers them at-least-once from a durable, ordered, per-producer outbox (redelivery after a crash between delivery and acknowledgment bookkeeping is legal), and a recovering producer receives each scope's undelivered terminals in order before any new open against that scope.
- The two shapes exhibit protocol equivalence: an in-process handler and its gRPC-wrapped counterpart share the same underlying implementation, so any capability advertised by one is advertised by the other, and the capability envelope (realized write semantics within the advertised set, split-scope and scopes-conflict gated on their flags) is enforced identically on both dispatch paths.
- A terminal event in the event log records rimsky's settlement decision, not the producer's acknowledgement of it. The event and the outbox row that will carry the verb are written in the same transaction, and delivery happens after that transaction commits, so an event may stand before — or without — the producer having heard. What guarantees the producer eventually hears is the outbox's at-least-once delivery, not the event.
