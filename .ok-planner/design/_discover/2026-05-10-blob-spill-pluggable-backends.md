---
topic: blob-spill-pluggable-backends
kind: choice
---

# Blob backend abstraction: four impls (inline, pg-largeobject, filesystem, memory); inline default, memory rejected outside unified

## Description

Three byte-stream surfaces in rimsky can grow large enough that inlining them in JSONB columns is impractical: attribute values (`rimsky_node_attributes.data`), parked-node payloads (`rimsky_worker_request.parked_payload_*`), and named-event payloads (`rimsky_node_events.payload_*`). Each surface has a paired `*_inline` (small bytes column) and `*_handle` / `*_backend` (handle pointer when spilled). The choice between inline and spilled is gated by `BlobConfig.SpillThresholdBytes` (default 64KB).

`persistence.BlobBackend` (`foundation/persistence/blob.go:30-65`) is the abstraction: five methods (`Write`, `Read`, `ReadRange`, `Delete`, `Name`) plus a `BlobHandle` opaque string. Four implementations ship:

- **`inline`** (`foundation/persistence/blob_inline.go`) — degenerate; spill is disabled. The default; operators who don't configure a backend get this and bytes stay inline regardless of size.
- **`pg-largeobject`** (`foundation/persistence/postgres/blob_largeobject.go`) — Postgres large objects via `lo_*` functions; the pgx driver handles the streaming.
- **`filesystem`** (`foundation/persistence/blob_filesystem.go`) — content-addressable directory on a shared volume; handles look like `fs:<sha256>`.
- **`memory`** (`foundation/persistence/blob_memory.go`) — in-process map; handles like `mem:<uuid>`. **Dev-only**; rejected outside unified mode.

`BlobConfig.Backend` (`foundation/persistence/blob_config.go:20-42`) selects the backend at startup. `ValidateBlobConfig` (line 103-130) enforces:

- A backend can be selected only once per `BlobConfig`.
- `memory` is rejected unless `RIMSKY_PROCESS_ROLE=unified` (set only by `rimsky-entrypoint`) — because the per-process binaries can't share an in-process map.

Handles are self-describing: each backend prefixes its handles (`inline:`, `pglo:`, `fs:`, `mem:`). The reader at `foundation/persistence/blob_spill.go` dispatches to the right backend by parsing the prefix. This supports a future migration story where two backends coexist transitionally, though currently only one backend is constructed per process (`blob.go:23-24`).

Orphan-blob tracking lives in `rimsky_blob_orphans` (`migrations/006-platform-extensions-park-blob-events.sql`). When a row that references a blob handle is deleted, the handle is moved to the orphan table; a sweep (`foundation/integration/orphan_blobs.go`) calls `BlobBackend.Delete` after a retention window. This decoupling lets the foreign-key-cascading rows commit without producing a slow blob-delete inline.

The blob-inertness rule (annotated at `foundation/persistence/blob.go:25-50`) is the opacity discipline: blob bytes are read only via `walkPath` substitution (the modeling-attribute substitution leaf) and the persistence-layer fetch on attribute/parked-payload/event read. Rimsky never logs, normalizes, hashes, indexes, pattern-matches, or includes blob bytes in error messages. This makes the spill mechanism transparent to substitution — a 50MB attribute and a 50-byte attribute substitute identically.

## Code surface

- `foundation/persistence/blob.go` — interface declaration + invariant-21 annotation.
- `foundation/persistence/blob_config.go` — config struct + `ValidateBlobConfig` startup gate.
- `foundation/persistence/blob_inline.go`, `blob_filesystem.go`, `blob_memory.go`, `postgres/blob_largeobject.go` — four impls.
- `foundation/persistence/blob_spill.go` — handle-prefix-routing read/write helpers.
- `foundation/persistence/blob_orphans.go` — orphan ledger CRUD.
- `foundation/integration/orphan_blobs.go` — orphan sweep.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql` — `rimsky_blob_orphans` plus the per-surface `_handle` / `_backend` columns.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §21 — blob content inert.
- `CLAUDE.md` "Non-obvious gotchas" — "Memory blob backend is dev-only."
- `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md` — the addition of spill across the three surfaces.

## Adjacent topics

- `2026-05-10-opacity-of-userdata-claim-blob` — blob-inertness, userdata-opacity, and claim-inertness collectively.
- `2026-05-10-postgres-only-runtime-state` — the three-process topology that rejects `memory`.
- `2026-05-10-event-log-append-only-jsonb` — `rimsky_node_events` is one of the three spill-capable surfaces.
- `2026-05-10-parked-state-and-resume` — parked payloads are another spill-capable surface.

## Observations

- The `memory` backend has its own startup-reject gate; SQLite (which has analogous "cannot share state across processes" semantics) does not. CLAUDE.md "Non-obvious gotchas" notes the SQLite-replicas-broken case as "no startup gate rejects this configuration today, parallel to the (enforced) memory-blob-backend rejection."
- Handles are self-describing strings, but the backend constructed at startup is single. Reading a `fs:`-prefixed handle on a process configured with `pg-largeobject` will return an error from `blob_spill.go::Read`. A future mixed-backend deployment would require routing logic that walks the prefix to dispatch to the right backend instance.
- The orphan sweep's retention window is set in `BlobConfig` (configurable). The default value is in `blob_config.go` and merits a documentation cross-check.
- `BlobBackend.Name()` returns a string identifier used in handle prefixing and in the orphan-table `backend` column; if a custom backend impl shares a prefix with a bundled backend, handles become ambiguous. Not enforced.
