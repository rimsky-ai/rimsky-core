-- =====  rimsky_run_scopes  =====
-- SQLite parallel of postgres migration 007. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
CREATE TABLE rimsky_run_scopes (
    id                  TEXT PRIMARY KEY,
    -- ON DELETE CASCADE on both parent_* FKs (mirror of postgres 007).
    parent_run_scope_id TEXT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    parent_run_id       TEXT NULL REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    graph_name          TEXT NOT NULL,
    partition_key       TEXT NOT NULL DEFAULT '',
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_instances.main_run_scope_id can be created in a single tx
    -- (mirror of the postgres migration 007 directive). SQLite has
    -- supported deferrable FKs since 3.6.19.
    -- ON DELETE CASCADE so deleting the instance walks the scope tree.
    instance_id         TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    closed_at           TEXT NULL,
    CHECK (
      (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
      OR
      (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
    ON rimsky_run_scopes (parent_run_id, partition_key)
    WHERE partition_key != '' AND closed_at IS NULL;

CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);
