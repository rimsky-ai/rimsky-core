-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: node-run
-- @concept: event-log
--
-- 034-node-runs-events-index-cleanup.sql
--
-- See the postgres sibling migration for rationale: two partial indexes on
-- rimsky_node_runs are subsumed by full indexes on the same column, and
-- rimsky_events has no index leading with occurred_at for the retention
-- sweep's DeleteOlderThan.

DROP INDEX IF EXISTS idx_node_runs_state_eligible;
DROP INDEX IF EXISTS idx_rimsky_node_runs_frame_claimed;

CREATE INDEX idx_rimsky_events_occurred_at ON rimsky_events (occurred_at);
