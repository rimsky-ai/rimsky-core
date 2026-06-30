-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 015-frame-owns-root-run-scope.sql
--
-- Frames own their root RunScope. See the postgres sibling migration for
-- rationale.

ALTER TABLE rimsky_frames
    ADD COLUMN root_run_scope_id TEXT NOT NULL DEFAULT '';

ALTER TABLE rimsky_instances DROP COLUMN main_run_scope_id;
