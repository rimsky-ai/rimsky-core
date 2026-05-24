-- =====  rimsky_instances.main_run_scope_id  =====
-- SQLite parallel of postgres migration 010. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §Lifecycle / Main RunScope.
--
-- SQLite's `ALTER TABLE ADD COLUMN NOT NULL` (without DEFAULT) fails on
-- a populated table. Pre-v1 break-freely covers this — the dev SQLite
-- database is dropped/recreated before re-running migrations, so the
-- populated-table-ADD-NOT-NULL case does not arise here.
ALTER TABLE rimsky_instances
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_run_scopes.instance_id can be created in a single tx
    -- (mirror of the postgres migration 010 directive).
    ADD COLUMN main_run_scope_id TEXT NOT NULL REFERENCES rimsky_run_scopes(id) DEFERRABLE INITIALLY DEFERRED;
