---
concept: claim
status: as-is
aliases: []
references:
  - _discover/2026-05-10-out-of-process-claim-producers.md
  - _discover/2026-05-10-byte-equal-scope-conflict.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
  - _discover/2026-05-10-lock-state-in-rimsky-not-producer.md
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
---

# Claim

## What it is

`claim` is the protocol-layer noun returned by `ClaimProducer.Open`; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — `@blessed-invariant 20` (claim content inert) gates content; `@blessed-invariant 4` (claimant-guarded release) gates the persistence row.

A claim is a node's request to access a producer-managed resource: an items-table row, a filesystem path, a queue head, an MVCC snapshot. Declared in templates as a `proto:claim_producer.proto::ClaimSpec` (fields: `producer_name`, `selector`, `intent`, `alias` — post-`spec:2026-05-12-nomenclature-resolution` Group B.3 the field is `producer_name`, formerly `store_name`). At runtime, the producer's `Open` returns an `Acquired{address, payload, claim_scope, realized_write_semantics}` (or `Unavailable`). The resolved claim scope bytes are persisted as `lock_kind='claim_scope'` rows in `table:rimsky_claim_handles`.

## Purpose

Claims are how a graph node says "I need exclusive (or coexisting) access to this thing while I run." The producer parses the selector from its own DSL and emits canonical claim scope bytes; rimsky enforces the conflict matrix byte-equally.

Per the 2026-05-15 data-platform-extensions, claims gain three orthogonal extensions:

- **Lifetime** (`subgraph | durable`; default `subgraph`): governs auto-terminal behavior. A `durable` claim's row persists past holding-subgraph completion as `state = 'committed' AND lifetime = 'durable'`, released only by explicit operator action or instance termination. See `concept:claim-lifetime`, `concept:asset`.
- **Sub-claim chains**: a claim's claim scope may be partitioned via `ClaimProducer.SplitScope` into sub-claims that hold sub-scopes. Persisted via `parent_claim_handle_id` on `rimsky_claim_handles`. Auto-terminal walks bottom-up: a parent claim resolves only after all sub-claims have terminal. See `concept:fan-out`, `concept:claim-handle`.
- **Co-holdership**: multiple node-runs may hold the same `claim_handle` via the `holds:` template directive. Each co-holder gets a row in `rimsky_claim_holders` keyed by `holder_run_id`. The holding subgraph extends to all co-holders; auto-terminal fires only after every co-holder reaches a non-active state. See `concept:claim-co-holdership`.

## Boundaries

Owns: the claim declaration, the address/payload/claim-scope returned at `Open`, the post-terminal verb (`Commit | Abandon | Release`). Does NOT own: lock state ledger (lives in `claim-handle`), capacity counting (that's `named-lock`), producer-internal state (lives in the producer). Adjacent: `claim-handle` (including its `### Held variant` subsection — the dropped `held-claim` concept's content lives there), `claim-producer`, `claim-scope`, `write-semantics`, `auto-terminal`, `inertness`.

## Invariants

- Claim content (payload, address, claim scope) is inert in rimsky: read only at the sanctioned substitution sites (`walkPath`, `stringifyRaw`) and one wire-encoding site (`makeStoreHandle`). Never logged, formatted with `%v`, attached to traces, validated beyond schema gates, or included in error messages (`@blessed-invariant 20`).
- Producers do not persist lock state internally; the `rimsky_claim_handles` table is the sole authority (`@blessed-invariant 9a`).
- Producers must not internally serialize on lock-shaped predicates (reader-lease serialization is forbidden for `staged_async`) — `@blessed-invariant 9b`.

## Aliases and historical names

`region` is a deprecated synonym for `claim scope` and still appears in older sketches and comments (`foundation/locks/conflict.go:14-18` references "v2's per-store RegionsConflict").

## Open within this concept

- `Store` vs `ClaimProducer` vocabulary split (see `tensions/store-vs-claim-producer-vocabulary.md`).
- `region` legacy synonym (see `tensions/_resolved/region-vs-scope-legacy.md`).

## Notes

- 2026-05-22 — Updated for ClaimScope rename per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`: bare "scope" references in the claim-identity-bytes sense qualified to "claim scope"; `concept:scope` adjacency rewritten to `concept:claim-scope`; `lock_kind='scope'` → `lock_kind='claim_scope'`; byte-equal references retain meaning but read "byte-equal claim scope".

