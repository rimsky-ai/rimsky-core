-- =====  rimsky_instances.main_run_scope_id  =====
-- Persist the main RunScope id on the instance row so handlers
-- (operator queries, callback resolution, etc.) can look up the
-- main scope without scanning rimsky_run_scopes. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §Lifecycle / Main RunScope.
ALTER TABLE rimsky_instances
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_run_scopes.instance_id can be created in a single tx
    -- (see 007-run-scopes.sql for the matching directive).
    ADD COLUMN main_run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id) DEFERRABLE INITIALLY DEFERRED;
