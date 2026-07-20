-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: frame
-- @concept: run-scope
-- @decision: run-scope-is-per-frame

-- 015-frame-owns-root-run-scope.sql
--
-- This ordinal was previously assigned to a different, now-deleted
-- migration; see the postgres sibling's header for the reuse caveat.
--
-- Frames own their root RunScope. See the postgres sibling migration for
-- rationale.

ALTER TABLE rimsky_frames
    ADD COLUMN root_run_scope_id TEXT NOT NULL DEFAULT '';

ALTER TABLE rimsky_instances DROP COLUMN main_run_scope_id;
