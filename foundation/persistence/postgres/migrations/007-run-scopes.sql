-- =====  rimsky_run_scopes  =====
-- First-class execution context per concept:run-scope. Hosts the set
-- of rimsky_node_runs rows for one graph instantiation (main /
-- subgraph / fanout_partition). Tree shape via parent_run_scope_id.
-- Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §"Schema / rimsky_run_scopes".
CREATE TABLE rimsky_run_scopes (
    id                  UUID PRIMARY KEY,
    -- ON DELETE CASCADE on both parent_* FKs so dropping the parent
    -- RunScope (or its parent node_run row) walks the run-scope tree
    -- automatically. Without CASCADE, Instances.Delete had to issue a
    -- topological walk per-scope; with CASCADE, a single DELETE on the
    -- instance row (or any scope) drops the whole subtree.
    parent_run_scope_id UUID NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    parent_run_id       UUID NULL REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    graph_name          TEXT NOT NULL,
    partition_key       TEXT NOT NULL DEFAULT '',
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_instances.main_run_scope_id can be created in a single tx
    -- (the instance and main RunScope must each reference the other's id;
    -- the FK can't be satisfied until both rows are inserted).
    -- ON DELETE CASCADE so deleting the instance walks the run-scope tree.
    instance_id         UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at           TIMESTAMPTZ NULL,

    CONSTRAINT run_scope_main_has_no_parents CHECK (
      (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
      OR
      (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
    )
);

-- At most one open fan-out partition RunScope per (parent_run_id, partition_key).
CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
    ON rimsky_run_scopes (parent_run_id, partition_key)
    WHERE partition_key != '' AND closed_at IS NULL;

-- Tree-walk index: parent_chain navigation for depth-gating + aggregation.
CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);
