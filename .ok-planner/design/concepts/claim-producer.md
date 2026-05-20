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

A claim producer is an out-of-process service that implements the gRPC `ClaimProducer` protocol — 4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake. Bundled reference impls live under `stores/` (filesystem, postgres, stub) as standalone binaries. The only in-rimsky concrete implementation of the Go `ClaimProducer` interface is the gRPC client at `runtime/remote/`.

Post-2026-05-15 the protocol gains three optional methods, each advertised in `Capabilities`:

- **`SplitScope(parent_claim_handle, partition_request) → [sub_scope_descriptor]`** — partitions a claim's scope into sub-scopes for fan-out. Advertised by `SupportsSplitScope: true`. Rimsky opens one sub-claim per sub-scope at parent-acquisition time.
- **`ScopesConflict(scope_a, scope_b) → {conflicts: bool}`** — producer-aware overlap predicate. Advertised by `SupportsScopesConflict: true`. Producers that don't advertise default to byte-equal comparison (`@blessed-invariant 4b`).
- **`Validation` (mix-in)** — same `Validate(request) → response` RPC any service can advertise via `protocols: [..., validation]`. Validates a node's userdata at template-registration time against the producer's domain (claim bindings, scopes). Inert `userdata` per `@blessed-invariant 11` — rimsky forwards opaque bytes; receives a verdict.

A fourth optional mix-in, **`DataProcessing`** (`protocols: [..., data_processing]`), is the control-plane surface for typed-data version lifecycle: `BeginCandidate` / `CommitCandidate` / `AbandonCandidate` / `ListVersions` / `ListPartitions` / `GetVersionSchema`. Data motion stays substrate-direct via `ClaimResult.address`; the protocol carries control-plane only. See `concept:data-processing`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical scope bytes; rimsky's conflict predicate is byte-equal. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state (filesystem stagings, items-table flips, MVCC transactions), the canonical scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same service).

The bundled SQL-based store `stores/postgres/` additionally registers `proto:executor.proto::Executor` to support verification of its own staged content; see `concept:executor`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. The pattern is open to future SQL-substrate stores adopting the same fusion.

## Invariants

- The 4-verb protocol (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake is the only contract. Type assertions to a concrete producer from any rimsky package are forbidden (`foundation/locks/interface.go:9-13`).
- Producers do not persist lock state (`@blessed-invariant 9a`) and do not internally serialize on lock-shaped predicates (`@blessed-invariant 9b`).
- Producers MUST satisfy byte-equal-scope uniformity: two `Open` calls returning byte-equal scope MUST also return the same `realized_write_semantics`.
- Terminal verbs (`Commit`/`Abandon`/`Release`) must be idempotent in `claim_id` so the verb-then-tx-fail leak path is recoverable.

## Aliases and historical names

`store` is the colloquial bundled-services term and the directory name (`stores/`). `ClaimProducer` is the protocol-level canonical name. The two coexist; CLAUDE.md "Vocabulary" notes the split. YAML config key `claim_producers:` aliases the legacy `stores:` key.

**Naming discipline.** In protocol-level prose — wire protocols, conformance suites, the Go interface name — the canonical term is **claim producer** and the Go interface is `ClaimProducer`. In casual operator parlance and in the reference-implementation tree (`stores/filesystem/`, `stores/postgres/`, `stores/stub/`), the colloquial **store** survives ("the filesystem store," "the postgres store"). Use "claim producer" in protocol-level discussion (someone implementing the protocol; someone reading the proto sources); "store" is acceptable in casual contexts about the bundled reference impls. Backticked-`Store` references in protocol-level prose are stale — the legacy Go interface was renamed to `ClaimProducer`.

**Rimsky's "store" is not a JS-framework store.** A Rimsky bundled-services-layer "store" is a data-backed `ClaimProducer` reference impl. Nothing to do with Redux / Vue / Svelte / Pinia state-management stores.

The Go `ClaimProducer` interface (`foundation/locks/interface.go`) carries a sixth method, `Name()`, alongside the 4 verbs + `Capabilities()`. `Name()` is a rimsky-side identifier (used for logging, metrics labels, and registry lookup); it is not transported on the wire and not part of the cross-language gRPC protocol. Test doubles must implement it to satisfy the interface.

## Open within this concept

- `Store` vs `ClaimProducer` vocabulary split — see `tensions/store-vs-claim-producer-vocabulary.md`.
- YAML `stores:` legacy alias of `claim_producers:` — see `tensions/yaml-stores-alias.md`.


## Notes

- 2026-05-14: atomic-staging pattern documented at `docs/agents/examples/atomic-staging.md` with a reference filesystem implementation under `examples/atomic-staging-fs-producer/`. Pattern is producer-side discipline; no rimsky-level surface change. Per spec Piece 3 `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- [2026-05-18] Folded content from former `docs/concepts/claim-producer.md` (now retired) — store-vs-claim-producer naming discipline + JS-framework-store disambiguation appended to Aliases section.
- 2026-05-19 — `stores/postgres/` extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.
