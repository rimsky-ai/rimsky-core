---
concept: scope
status: as-is
aliases:
  - region (deprecated)
references:
  - _discover/2026-05-10-byte-equal-scope-conflict.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
---

# Scope

## What it is

Scope is the opaque byte stream a `ClaimProducer.Open` returns to identify "what was acquired." Persisted as `rimsky_claim_handle.scope_data`. Compared byte-equally via `ScopesByteEqual` (`foundation/locks/conflict.go:64-77`). The producer parses its own selector DSL and emits canonical bytes; rimsky has no producer-specific code in the conflict predicate.

## Purpose

Rimsky has to detect "these two claims target the same data" across producers it knows nothing about. Pushing canonicalization to the producer and using byte-equal comparison in rimsky keeps the conflict predicate uniform regardless of producer semantics.

## Boundaries

Owns: the conflict-check comparison, the schema column, opacity discipline at all rimsky-side sites. Does NOT own: canonicalization (producer's job), capacity counting (named-lock's job), claim payload/address (other opaque streams). Adjacent: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `opacity`.

## Invariants

- Scope comparison is `bytes.Equal`. Empty slices never conflict.
- Producers maintain the byte-equal-scope **uniformity invariant**: two `Open` calls with byte-equal scope MUST return the same `realized_write_semantics` (spec §2.5). Rimsky relies on this; does not verify it.
- The standard filesystem store is concrete-paths only (canonicalizes by requiring absolute paths so byte-equality holds).
- Scope content is inert in rimsky (`@blessed-invariant 20`).

## Aliases and historical names

`region` is a deprecated synonym; comments and prose still cite "v2's RegionsConflict" by historical name. The current canonical term is `scope`.

## Open within this concept

- `region` legacy synonym usage — see `tensions/region-vs-scope-legacy.md`.

