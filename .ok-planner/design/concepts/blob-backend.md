---
concept: blob-backend
status: as-is
aliases: []
references:
  - _discover/2026-05-10-blob-spill-pluggable-backends.md
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
  - _discover/2026-05-10-postgres-only-runtime-state.md
---

# Blob backend

## What it is

`persistence.BlobBackend` (`foundation/persistence/blob.go`) is the abstraction that backs spilled byte streams from three surfaces: attribute values (`rimsky_node_attributes.data`), parked-node payloads (`rimsky_worker_request.parked_payload_*`), and named-event payloads (`rimsky_node_events.payload_*`). Five methods (`Write`, `Read`, `ReadRange`, `Delete`, `Name`). Four impls: `inline` (default; spill disabled), `pg-largeobject`, `filesystem`, `memory` (dev-only).

## Purpose

A 50KB attribute value, a 200MB parked payload, and a 10-byte event payload all need to behave the same to substitution consumers. Spilling above a threshold (`SpillThresholdBytes`, default 64KB) keeps inline JSONB columns small; a pluggable backend lets operators pick the storage shape (postgres LO, shared filesystem, etc.).

## Boundaries

Owns: the abstraction, the four impls, the spill threshold, the orphan-blob ledger and sweep. Does NOT own: substitution (see `attribute`), claim-payload bytes (those are claim-handle-owned), userdata (always inline). Adjacent: `attribute`, `parked-state`, `named-event`, `opacity`, `persistence-driver`.

## Invariants

- Blob content is inert in rimsky (`@blessed-invariant 21`). Read only at the `walkPath` substitution leaf and the persistence-layer fetch on read.
- `memory` backend rejected at startup unless `RIMSKY_PROCESS_ROLE=unified`; the per-process binaries cannot share an in-process map.
- Handles are self-describing strings (`inline:`, `pglo:`, `fs:`, `mem:`); current single-backend-per-process means cross-prefix reads fail.
- Orphan blobs go to `rimsky_blob_orphans` and are swept after a retention window.

## Aliases and historical names

None live.

## Open within this concept

- SQLite has analogous "cross-process broken" semantics but is NOT gate-rejected; only `memory` is — see `tensions/sqlite-vs-memory-reject-asymmetry.md`.

