# Attribute bytes live in the row — Design Sketch

**Date:** 2026-08-22
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

Remove spill. An attribute bag and a node-run's scratch are values in their own columns of the primary database, whatever their size, and the engine's own large-value handling — TOAST under Postgres, overflow pages under SQLite — does inside the row's transaction what the blob backend did outside it. Today a value over a 64 KiB threshold goes to a blob backend that is not transactional with the row: the filesystem and memory backends by construction, and the Postgres large-object backend because it opens its own transaction on the pool (`lib/foundation/persistence/postgres/blob_largeobject.go::Write`). That gap is the whole reason `rimsky_blob_orphans`, `QueueBlobOrphan`, `SweepOrphanedBlobs`, and a 24-hour retention window exist, and no timeout or stage-commit protocol on the backend side closes it, because an expired stage cannot tell a crash after the database committed from a crash after it rolled back. With the bytes in the row there is nothing to coordinate and nothing to clean up.

The ceilings without spill are large: Postgres caps one BYTEA value at 1 GB; SQLite caps any cell at `SQLITE_MAX_LENGTH`, 1 GB by default. An attribute that approaches either is a design problem in the template, not a storage problem in rimsky.

A pluggable attribute store for every attribute, with a formal record of failed cross-store transactions, is a later design. This sketch removes the ad hoc one so that design starts from nothing.

## Shape

### The columns are the storage

- `rimsky_node_attributes.data` holds the whole bag and becomes `BYTEA` under Postgres and `BLOB` under SQLite; `dispatch_input_bag` changes with it. No SQL reads inside either column — Go decodes them whole — so JSONB was buying a parse-and-reserialise on every write, a 255 MB cap in place of BYTEA's 1 GB, and a validity check the encoder already makes. The migration drops `value_handle` and `value_handle_backend`.
- `rimsky_node_runs.scratch_inline` (BYTEA) holds the scratch and is renamed `scratch`. The migration drops `scratch_handle` and `scratch_handle_backend`.
- One migration per backend drops the four columns and `rimsky_blob_orphans`.

### A write over the ceiling fails at the write

The engine refuses a value over its cap with its own error. The two writers (`node_attributes.Upsert`, `WriteScratch`) wrap it naming the node run, the attribute or scratch, and the byte count, and the failure reaches the executor or the route the way any persistence error does. No rimsky-side threshold precedes it: a configured limit below the engine's would be a second cap with a second error to explain, and a limit above it is unreachable.

### What goes

- `lib/foundation/persistence/blob.go` (`BlobBackend`, `BlobKey`, `Handle`, `ErrBlobNotFound`), `blob_config.go`, `blob_spill.go`, `blob_inline.go`, `blob_filesystem.go`, `blob_memory.go`, `blob_orphans.go`, and `postgres/blob_largeobject.go`; the orphan table and the sweep in both backends.
- `tablesImpl.blob`, `blobThreshold`, `blobRetention`, `SetBlobBackend`, `BlobRetention()` in both backends; the spill branch in `node_attributes.Upsert` and `WriteScratch`; the handle-resolving read branch in the attribute-row readers and `lib/runtime/runner_acquire_helpers.go::loadScratch`.
- `lib/runtime/orphan_blobs.go`, `SchedulerConfig.OrphanBlobSweepInterval`, the tick's hourly sweep.
- `RunArgs.Blob` and `CallbackServer.Blob` in `lib/runtime/callback.go`; the blob wiring in `lib/control/config/blob.go`, `scheduler.go`, `supervisor.go`, and the four `lib/control/launch` files.
- `cfg:persistence.blob` whole: `backend`, `spill_threshold_bytes`, `filesystem.root`, `retention`; the `memory`-only-under-`unified` topology gate; the `CLAUDE.md` gotcha that names it.
- `rimsky conformance blob-backend`, `cmd/rimsky/conformance_blob_backend.go`, and the `blob-backend` entry in the conformance verb list.
- `lib/foundation/persistence/conformance` cases that exercise spill or orphans.

### Corpus

- The sprint retires `concept:blob-backend`. `concept:persistence-database` gains one sentence: an attribute bag and a scratch buffer are byte-column values the engine never reads inside, bounded by its per-value cap.
- `decision:blob-backend`, `decision:blob-backends-pluggable`, `decision:blob-spill-threshold-config`, and `decision:blob-backend-mismatch-read-refused` retire together. One new decision replaces them: attribute bytes commit with the row, with the rationale that a store outside the transaction cannot be cleaned up without a durable record of intent, and the engine's cap as the stated ceiling. Alternatives recorded: a transactional large-object store (rejected: no caller streams, and it adds a transaction-scoped handle to every reader), an external blob store with a cleanup queue (rejected: the queue this sketch removes).
- `decision:artifact-layout`: "the run's state database and its blob store side by side" becomes the state database alone; under SQLite the one file is the whole record, which is the stronger form of the promise.
- `concept:conformance`: the blob-backend suite leaves the list.
- `concept:node-run`, `concept:instance`, `concept:inertness`, `concept:rimsky-yml`: the sentences that mention spill or a handle go.
- `story:audit-artifact` is unchanged in its promise.

## Open questions

- **Whether to state a rimsky-side ceiling.** The sketch states none and lets the engine refuse. A deployment that wants a lower bound for its own reasons — row bloat, backup size — has no knob. The sketch assumes that is acceptable pre-v1 and that the later attribute-store design is where a policy like that belongs.
- **Scratch column type under SQLite.** Postgres has `scratch_inline BYTEA`; the sketch assumes SQLite's is `BLOB` and that the rename is the only change.
- **The other JSON columns.** `params` and `attribute_overrides` also have no SQL reader but are small and stay JSONB so `psql` shows them readable. The event payload, frame message fields, and lineage columns have SQL readers inside them (`events.go`, `frames.go`, `lineage.go` in both backends) and stay JSONB. The sketch assumes that split and changes nothing outside the bag and scratch.
- **Migration of existing spilled values.** Pre-v1, the sketch assumes a deployment with a configured backend re-creates its instances. A one-shot importer that reads each handle and writes the bytes inline is the shape if one is needed, and it is not in this sketch.

## Risks / unknowns

- Every reader of an attribute bag loads the whole value. A deployment that today keeps a multi-megabyte value behind a handle and reads the bag often will read those bytes on every load. Nothing in the tree reads a spilled value partially (`ReadRange` has no caller outside the blob packages and the conformance verb), so the access pattern does not change; the row size does.
- `docs/` references blob backends, the spill threshold, and the conformance verb in 41 places; the next `/document` run revises them, and the sprint removes the protocol table rows by hand so the tree ships no doc for a backend that is gone.
- Removing `cfg:persistence.blob` is a config-shape break, legal pre-v1 (`.claude/rules/rules.md`); a deployment that sets it fails validation at startup naming the key, which is the existing behaviour for an unknown key.

## What this is not

- Not the pluggable attribute store. Moving every attribute behind a protocol a deployment points at a filesystem, Redis, or DynamoDB needs the formal failed-transaction record this sketch argues cannot be avoided for an external store. That is its own sketch, and this one clears the ground for it.
- Not a change to what an attribute or scratch is, or to who reads them.
- Not a change to the queue-consolidation question. Blob orphans leave that list; the rest of it stands.
