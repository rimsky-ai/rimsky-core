-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: parked-state
--
-- 025-retire-park-reason-and-watchdog.sql
--
-- Retire the park-reason taxonomy and the park-timeout watchdog. See the
-- postgres sibling migration for rationale. No CHECK or index references
-- these columns on SQLite (parked_reason was app-layer constrained), so
-- plain DROP COLUMN suffices — no table rebuild needed.

ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason_label;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason_note;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_resume_at;
ALTER TABLE rimsky_node_runs DROP COLUMN max_park_duration_seconds;
