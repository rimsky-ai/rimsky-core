-- =====  rimsky_node_runs.prior_dispatch_id / prior_dispatch_disposition  =====
-- SQLite parallel of postgres migration 012. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §Recovery-aware executor protocol.
ALTER TABLE rimsky_node_runs
    ADD COLUMN prior_dispatch_id TEXT NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL;
ALTER TABLE rimsky_node_runs
    ADD COLUMN prior_dispatch_disposition TEXT NULL
        CHECK (prior_dispatch_disposition IS NULL
               OR prior_dispatch_disposition IN ('heartbeat_stale', 'retry_after_error', 'recalculate'));
