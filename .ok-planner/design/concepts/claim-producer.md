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

A claim producer is an out-of-process peer service that implements the gRPC `ClaimProducer` protocol — 4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake. Bundled reference impls live under `stores/` (filesystem, postgres, stub) as standalone binaries. The only in-rimsky concrete implementation of the Go `ClaimProducer` interface is the gRPC client at `foundation/integration/remote/`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical scope bytes; rimsky's conflict predicate is byte-equal. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state (filesystem stagings, items-table flips, MVCC transactions), the canonical scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same peer).

## Invariants

- The 4-verb protocol (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake is the only contract. Type assertions to a concrete producer from any rimsky package are forbidden (`foundation/locks/interface.go:9-13`).
- Producers do not persist lock state (`@blessed-invariant 9a`) and do not internally serialize on lock-shaped predicates (`@blessed-invariant 9b`).
- Producers MUST satisfy byte-equal-scope uniformity: two `Open` calls returning byte-equal scope MUST also return the same `realized_write_semantics`.
- Terminal verbs (`Commit`/`Abandon`/`Release`) must be idempotent in `claim_id` so the verb-then-tx-fail leak path is recoverable.

## Aliases and historical names

`store` is the colloquial bundled-services term and the directory name (`stores/`). `ClaimProducer` is the protocol-level canonical name. The two coexist; CLAUDE.md "Vocabulary" notes the split. YAML config key `claim_producers:` aliases the legacy `stores:` key.

The Go `ClaimProducer` interface (`foundation/locks/interface.go`) carries a sixth method, `Name()`, alongside the 4 verbs + `Capabilities()`. `Name()` is a rimsky-side identifier (used for logging, metrics labels, and registry lookup); it is not transported on the wire and not part of the cross-language gRPC protocol. Test doubles must implement it to satisfy the interface.

## Open within this concept

- `Store` vs `ClaimProducer` vocabulary split — see `tensions/store-vs-claim-producer-vocabulary.md`.
- YAML `stores:` legacy alias of `claim_producers:` — see `tensions/yaml-stores-alias.md`.

