-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: parked-state
--
-- 025-retire-park-reason-and-watchdog.sql
--
-- Retire the park-reason taxonomy and the park-timeout watchdog. Park
-- collapses to resume_at + scratch + tags: parked_reason (and its CHECK),
-- parked_reason_label, and parked_reason_note are erased; the watchdog's
-- max_park_duration_seconds cap is erased with the parked->failed sweep.
-- parked_resume_at was an unread legacy column; resume_at is the wake
-- clock.

ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason_label;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_reason_note;
ALTER TABLE rimsky_node_runs DROP COLUMN parked_resume_at;
ALTER TABLE rimsky_node_runs DROP COLUMN max_park_duration_seconds;
