-- 015-node-run-resuming-state.sql
--
-- Adds the 'resuming' value to the rimsky_node_runs.state CHECK constraint and
-- extends the dispatch-eligibility partial index to cover it. The 'resuming'
-- state distinguishes "this node-run is waking from a deadline-driven park and
-- must re-use its dispatch-time substitution snapshot" from "this node-run
-- transitioned to stale via cascade and needs fresh substitution at dispatch."
-- The dispatcher branches on the state to decide whether to rebuild
-- substitution or load the persisted attributes bag verbatim.

ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT rimsky_node_runs_state_check;

ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_state_check
    CHECK (state IN ('fresh','stale','running','failed','parked','resuming'));

DROP INDEX IF EXISTS idx_node_runs_state;
CREATE INDEX idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running','resuming');
