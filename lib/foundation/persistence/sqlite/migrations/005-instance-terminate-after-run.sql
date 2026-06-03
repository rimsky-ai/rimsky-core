-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 005-instance-terminate-after-run.sql — parallel to postgres 005. Per spec
-- 2026-06-03-instance-lifecycle-durable-by-default, add the per-instance
-- terminate_after_run flag. Instances are durable by default; this opt-in
-- boolean is the only path by which an instance self-terminates, and then
-- only after its next frame ends.
--
-- Unlike 004 (which had to no-op because SQLite cannot ALTER COLUMN ... SET
-- DEFAULT), this is a plain additive ADD COLUMN, which SQLite supports with a
-- literal NOT NULL DEFAULT. The storage form mirrors the `paused` column in
-- 001-schema.sql (INTEGER NOT NULL DEFAULT 0 — SQLite's boolean storage
-- form). Default 0 preserves durable-by-default for every existing row.

ALTER TABLE rimsky_instances
    ADD COLUMN terminate_after_run INTEGER NOT NULL DEFAULT 0;
