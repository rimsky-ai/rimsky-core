-- 014-drop-last-outcome.sql
-- Drop the last_outcome column from rimsky_node_runs.
-- Per spec .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md Phase 5.
-- Cascade-fire is now subscriber-driven (signal-type-path match), not gated on this column.
-- The settling_signal_type column (added in migration 013) replaces last_outcome's
-- information role. Reader sites have been updated in the same plan pass.
ALTER TABLE rimsky_node_runs DROP COLUMN IF EXISTS last_outcome;
