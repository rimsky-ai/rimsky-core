# Blob backends

When an attribute, parked-state payload, or named-event payload exceeds
the inline-spill threshold (default 64 KiB), rimsky stores the bytes in
a configured `BlobBackend` rather than inline in `rimsky_node_attributes`
/ `rimsky_node_runs` / `rimsky_node_events`.

Rimsky ships four reference backends:

- [`inline`](./inline.md) — degenerate (no spill); the default.
- [`pg-largeobject`](./pg-largeobject.md) — stores blobs in the same
  Postgres instance via the LO API.
- [`filesystem`](./filesystem.md) — stores blobs as files under a
  configured root.
- [`memory`](./memory.md) — in-process map; **dev-only**, rejected at
  startup unless `RIMSKY_PROCESS_ROLE=unified`.

Configuration: `persistence.blob.backend` plus per-backend sub-blocks.
See `deploy/rimsky.yml` for the full schema.

## Cross-backend invariants

- **Blob content is inert in Rimsky** (`@blessed-invariant 21`): bytes
  are read only via `walkPath` substitution and the persistence-layer
  fetch on attribute read. Rimsky never logs, formats with `%v`,
  validates beyond schema gates, transforms, normalizes, hashes,
  indexes, pattern-matches, attaches to traces, or includes blob bytes
  in error messages.
- Handles are self-describing: each backend prefixes with its name
  (`pglo:`, `fs:`, `mem:`) so a future migration tool can route by
  prefix.
- Orphan reaping: when an attribute's `value_handle` is overwritten or
  the row is deleted, the old handle is queued in
  `rimsky_blob_orphans`; the `SweepOrphanedBlobs` sweep deletes the
  bytes from the backend after `retention.retention_after_unreferenced`
  has passed (default 24h).

## Conformance

`go run ./cmd/rimsky-blob-backend-conformance --backend <name> [args]`
runs the in-process conformance suite (six checks: round-trip 1KB +
10MB, range read, delete-then-read, idempotent delete, concurrent
writes).
