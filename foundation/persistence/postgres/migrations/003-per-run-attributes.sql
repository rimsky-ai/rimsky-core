-- =====  rimsky_node_attributes  (re-keyed per-run)  =====
-- Pre-v1 destructive rekeying: drop the existing per-node table and
-- recreate keyed by node_run_id, with node_id denormalized for
-- forensic queries.  Per spec
-- .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
-- §"Data model — re-key rimsky_node_attributes".
DROP TABLE IF EXISTS rimsky_node_attributes;

CREATE TABLE rimsky_node_attributes (
    node_run_id          UUID PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);
