-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Stage-3 of the run-row lifecycle cutover: drop the state-machine
-- columns from rimsky_nodes. Post-stage-2, every dispatch-readiness
-- reader sources state from rimsky_node_runs.state (per the partial-
-- unique-on-in-flight-phase invariant from migration 003). The Go-side
-- enforceAndUpdate scaffold no longer dual-writes to rimsky_nodes; the
-- state machine lives entirely on rimsky_node_runs.
--
-- The rimsky_nodes row keeps identity + scheduling metadata:
--   id, instance_id, node_type, executor, schedule_cron,
--   current_error_class, retry_counter, action_index, frame_id,
--   created_at, updated_at.
--
-- Pre-v1 break-freely (per .claude/rules/rules.md): no compat shim — the
-- dropped columns are gone for good.
--
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
-- Plan: .ok-planner/plans/2026-05-15-data-platform-extensions-plan.md

-- Drop indexes that referenced the doomed columns first; postgres tolerates
-- the column drop with a CASCADE, but explicit DROP INDEX statements keep
-- the migration log readable.
DROP INDEX IF EXISTS rimsky_nodes_state_updated_at_idx;
DROP INDEX IF EXISTS idx_rimsky_nodes_frame_state;

ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS state;
ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS last_outcome;
ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS last_heartbeat_at;
ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS assigned_supervisor_id;

-- Replacement covering index for the (frame_id, state) lookup that the
-- engine queries used pre-cutover. The same logical query lives on
-- rimsky_node_runs now (idx_node_runs_state from migration 003 covers it).
