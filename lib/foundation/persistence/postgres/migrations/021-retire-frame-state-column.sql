-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: frame
-- @concept: instance
-- @decision: frame-isolation-is-structural
--
-- 021-retire-frame-state-column.sql
--
-- Retire rimsky_frames.state.
--
-- The frame row's state was redundant with its owned node_runs: a
-- frame is unresolved iff any of its node_runs is in a non-terminal
-- state ('pending','stale','running','held','parked'); the 'failed'
-- vs 'completed' distinction was itself a summary of "does this frame
-- have any failed node_run?" derived at frame-end via HasFailedNode.
-- Both facts are pure functions of rimsky_node_runs.state and can be
-- computed at read time.
--
-- ended_at stays. It is not cascade state — cascade processing never
-- touches the frame row. The scheduler's frame-end reaper writes
-- ended_at exactly once via MarkFrameEnded, whose atomicity gates the
-- one-shot frame-end effect (log line, duration metric) and lets the
-- retention sweep identify resolved frames without joining node_runs.

ALTER TABLE rimsky_frames DROP CONSTRAINT chk_running_has_started;
ALTER TABLE rimsky_frames DROP CONSTRAINT chk_terminal_has_ended;

DROP INDEX IF EXISTS uq_rimsky_frames_running;

ALTER TABLE rimsky_frames DROP COLUMN state;

CREATE UNIQUE INDEX uq_rimsky_frames_open
    ON rimsky_frames (instance_id)
    WHERE ended_at IS NULL;
