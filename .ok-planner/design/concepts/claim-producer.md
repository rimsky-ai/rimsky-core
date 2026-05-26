---
concept: claim-producer
status: as-is
aliases:
  - store (legacy / colloquial)
  - claim-store
references:
  - _discover/2026-05-10-out-of-process-claim-producers.md
  - _discover/2026-05-10-atomic-acquisition-decoupled-tx.md
  - _discover/2026-05-10-byte-equal-scope-conflict.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
  - _discover/2026-05-10-lock-state-in-rimsky-not-producer.md
---

# Claim producer

## What it is

A claim producer is an out-of-process service that implements the gRPC claim-producer protocol — 4 verbs (open / commit / abandon / release) plus the capabilities startup handshake. Production-side reference implementations (filesystem, postgres) ship as standalone binaries on the consumption side, outside the platform; an in-rimsky stub carve-out stays as test infrastructure. The only in-rimsky concrete implementation of the claim-producer protocol is the gRPC peer client.

Post-2026-05-15 the protocol gains three optional methods, each advertised in the capabilities handshake:

- **Split-scope** — partitions a claim's claim scope into sub-scopes for fan-out, returning one sub-scope descriptor per partition. Advertised via a split-scope capability flag. Rimsky opens one sub-claim per sub-scope at parent-acquisition time.
- **Scopes-conflict** — a producer-aware overlap predicate over two claim scopes. Advertised via a scopes-conflict capability flag. Producers that don't advertise default to byte-equal comparison (`@blessed-invariant 4b`).
- **Validation (mix-in)** — the same validate request/response RPC any service can advertise via the validation protocol. Validates a node's userdata at template-registration time against the producer's domain (claim bindings, scopes). Inert `userdata` per `@blessed-invariant 11` — rimsky forwards opaque bytes; receives a verdict. See `concept:validation`.

A fourth optional mix-in, the **data-processing protocol**, is the control-plane surface for typed-data version lifecycle: begin / commit / abandon a candidate, plus list-versions / list-partitions / get-version-schema. Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only. See `concept:data-processing`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical claim scope bytes; rimsky's conflict predicate is byte-equal. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state (filesystem stagings, items-table flips, MVCC transactions), the canonical claim-scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `claim-scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same service).

The bundled SQL-based postgres store additionally registers the executor protocol to support verification of its own staged content; see `concept:executor`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. The pattern is open to future SQL-substrate stores adopting the same fusion.

## Invariants

- The 4-verb protocol (open / commit / abandon / release) plus the capabilities startup handshake is the only contract. Type assertions to a concrete producer from any rimsky package are forbidden — rimsky depends on the protocol only.
- Producers do not persist lock state (`@blessed-invariant 9a`) and do not internally serialize on lock-shaped predicates (`@blessed-invariant 9b`).
- Producers MUST satisfy byte-equal-claim-scope uniformity: two open calls returning byte-equal claim scope MUST also return the same realized write semantics.
- Terminal verbs (commit/abandon/release) must be idempotent in the claim identifier so the verb-then-transaction-fail leak path is recoverable.

## Aliases and historical names

"store" is the colloquial bundled-services term for the production-side reference implementations (which live on the consumption side, outside the platform); the stub store stays in rimsky as test infrastructure. "claim producer" is the protocol-level canonical name. The two coexist; the project's vocabulary notes record the split. The current YAML config key for declaring producers aliases the legacy stores key.

**Naming discipline.** In protocol-level prose — wire protocols, conformance suites, the protocol's canonical name — the canonical term is **claim producer**. In casual operator parlance and in the reference-implementation tree (the filesystem store, the postgres store, the stub store), the colloquial **store** survives ("the filesystem store," "the postgres store"). Use "claim producer" in protocol-level discussion (someone implementing the protocol; someone reading the proto sources); "store" is acceptable in casual contexts about the bundled reference impls. The colloquial "store" term is stale in protocol-level prose — the protocol's canonical name is "claim producer."

**Rimsky's "store" is not a JS-framework store.** A Rimsky bundled-services-layer "store" is a data-backed claim-producer reference impl. Nothing to do with Redux / Vue / Svelte / Pinia state-management stores.

The claim-producer protocol carries a sixth method, a name accessor, alongside the 4 verbs plus the capabilities handshake. The name is a rimsky-side identifier (used for logging, metrics labels, and registry lookup); it is not transported on the wire and not part of the cross-language gRPC protocol. Test doubles must implement it to satisfy the protocol.

## Resolved within this concept

- The "store" vs claim-producer vocabulary split was resolved — see `tension:_resolved/store-vs-claim-producer-vocabulary`.
- The legacy stores YAML key as an alias of the producer key was resolved — see `tension:_resolved/yaml-stores-alias`.


## Notes

- 2026-05-14: atomic-staging pattern documented with a reference filesystem implementation. Pattern is producer-side discipline; no rimsky-level surface change. Per Piece 3 of `spec:2026-05-14-subscription-cascade-and-quality-of-life-design`.
- [2026-05-18] Folded content from a former, now-retired claim-producer doc — store-vs-claim-producer naming discipline + JS-framework-store disambiguation appended to Aliases section.
- 2026-05-19 — the postgres store extends to the executor role per `spec:2026-05-19-multi-instance-template-ergonomics-design`.
- 2026-05-22 — Updated for the ClaimScope rename per `spec:2026-05-22-fan-out-safety-scope-first-design`: "scope bytes" references qualified to "claim scope bytes"; the scope adjacency rewritten to `concept:claim-scope`. The corresponding proto field and message renames ride alongside.
- 2026-05-24: production-side bundled claim-producer reference impls (filesystem and postgres) moved out of rimsky to the consumption side, outside the platform. Test-infrastructure carve-outs (the stub store for test-double plus quickstart, and the filesystem/postgres test-fixture packages) stay in rimsky. Boundary statement updated to reflect the new home. Also: the calling-side gRPC client moved to the peer package per the P2 rename. See `spec:2026-05-24-repo-reorganization-design` phases P2 and P3.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
