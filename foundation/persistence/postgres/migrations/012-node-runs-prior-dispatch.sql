-- =====  rimsky_node_runs.prior_dispatch_id / prior_dispatch_disposition  =====
-- Recovery-aware executor protocol: when a dispatch supersedes a
-- failed / abandoned / stale-recovered predecessor for the same
-- (run_scope_id, node_id), the supervisor stamps the predecessor's
-- dispatch_id (rimsky_node_runs.id) plus a classifier on the new row
-- so the executor can read it back on
-- proto:executor.proto::ExecuteRequest.prior_dispatch_id /
-- prior_dispatch_disposition.
--
-- Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §Recovery-aware executor protocol.
--
-- Both columns nullable; unset on the initial dispatch of a node
-- within a RunScope. The disposition is lower_snake_case text
-- mirroring the proto enum symbols.
ALTER TABLE rimsky_node_runs
    ADD COLUMN prior_dispatch_id        UUID NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    ADD COLUMN prior_dispatch_disposition TEXT NULL
        CHECK (prior_dispatch_disposition IS NULL
               OR prior_dispatch_disposition IN ('heartbeat_stale', 'retry_after_error', 'recalculate'));
