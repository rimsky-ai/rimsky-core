-- =====  rimsky_node_runs.run_scope_id  =====
-- Replace inline (parent_run_id, child_key) with non-null FK to
-- rimsky_run_scopes. Collapse the two partial-unique in-flight
-- indexes to one keyed on (node_id, run_scope_id). Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
--
-- Pre-v1 break-freely (per submodules/rimsky/.claude/rules/rules.md):
-- drop and recreate; no data preservation.

ALTER TABLE rimsky_node_runs DROP CONSTRAINT IF EXISTS rimsky_node_runs_child_key_check;

DROP INDEX IF EXISTS uq_node_runs_in_flight_per_root_node;
DROP INDEX IF EXISTS uq_node_runs_in_flight_per_child;
DROP INDEX IF EXISTS idx_node_runs_parent_run_id;

ALTER TABLE rimsky_node_runs DROP COLUMN parent_run_id;
ALTER TABLE rimsky_node_runs DROP COLUMN child_key;

-- ON DELETE CASCADE so dropping a RunScope (which cascades from the
-- instance row) walks the dispatch rows it owns automatically.
ALTER TABLE rimsky_node_runs
    ADD COLUMN run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE;

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);
