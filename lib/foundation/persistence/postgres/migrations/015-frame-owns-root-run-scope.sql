-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 015-frame-owns-root-run-scope.sql
--
-- Frames now own their root RunScope. Each frame's start creates a fresh
-- runscope row; cascade walks and message delivery read frame.root_run_scope_id
-- instead of the now-retired instance.main_run_scope_id singleton.
--
-- The instance-level singleton was an architectural mistake: a runscope lives
-- inside exactly one frame and never spans frames. With the singleton, every
-- frame's cascade reused the same runscope, allowing carry-forward (which is
-- runscope-scoped) to leak across frame boundaries and producing the
-- session-resume carry-forward race we just chased down.
--
-- Pre-v1: existing rimsky_frames rows get a zero-UUID placeholder, which is
-- meaningless but lets the column be NOT NULL. Nuke a dev Postgres if you
-- need a clean slate; testcontainers tests start fresh and are unaffected.

ALTER TABLE rimsky_frames
    ADD COLUMN root_run_scope_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid;
ALTER TABLE rimsky_frames
    ALTER COLUMN root_run_scope_id DROP DEFAULT;

ALTER TABLE rimsky_instances DROP COLUMN main_run_scope_id;
