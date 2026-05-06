-- 2026-05-05 last_outcome + last_progress_at: support for the
-- reactive-loops + lifecycle-handlers spec.
-- See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.4.
--
-- Postgres uses TIMESTAMPTZ; SQLite stores ISO-8601 strings via the
-- existing time-handling pattern (per persistence-pluggable spec §6.3).
--
-- The DEFAULT below uses strftime('%f') which only yields millisecond
-- precision (e.g. 2026-05-05T12:34:56.123Z) — runtime writes from Go
-- (sqlite/types.go::nowUTC + sqlite/frames.go EnqueueSerial/Coalesce)
-- explicitly write RFC3339Nano values so all rows seeded post-migration
-- carry uniform nano precision. The DEFAULT is only consumed by rows
-- that exist at migration time (none in dev workflows; all subsequent
-- INSERTs include last_progress_at explicitly). The frame-timeout
-- check uses Go-side time.Parse on the value, so mixed precision does
-- not affect correctness; the documented contract is "do not perform
-- SQL-level string comparison on this column — drive comparisons in
-- Go after parseTime()."

ALTER TABLE rimsky_nodes
    ADD COLUMN last_outcome TEXT;

ALTER TABLE rimsky_frames
    ADD COLUMN last_progress_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
