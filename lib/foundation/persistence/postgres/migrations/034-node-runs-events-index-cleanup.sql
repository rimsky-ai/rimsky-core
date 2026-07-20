-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: node-run
-- @concept: retention-sweep
--
-- 034-node-runs-events-index-cleanup.sql
--
-- rimsky_node_runs_state_idx (state) already covers every row
-- idx_node_runs_state_eligible (state) WHERE state IN ('stale','running')
-- would serve, and idx_rimsky_node_runs_frame (frame_id) already covers
-- every row idx_rimsky_node_runs_frame_claimed (frame_id) WHERE claimed_by
-- IS NOT NULL would serve; both partials pay write-maintenance cost on
-- rimsky_node_runs's hottest path for no read benefit the full index
-- doesn't already give. rimsky_events has no index leading with
-- occurred_at, so SweepRunTreeRetention's DeleteOlderThan seq-scans the
-- event log on every sweep.

DROP INDEX IF EXISTS idx_node_runs_state_eligible;
DROP INDEX IF EXISTS idx_rimsky_node_runs_frame_claimed;

CREATE INDEX idx_rimsky_events_occurred_at ON rimsky_events (occurred_at);
