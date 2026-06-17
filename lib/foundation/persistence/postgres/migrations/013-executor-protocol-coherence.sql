-- 013-executor-protocol-coherence.sql
--
-- Reshape rimsky_node_runs for the post-streaming executor protocol:
-- drop the rimsky_node_events ledger; drop the parked-payload / session-token / wake-reason
-- columns; add async-callback-registry columns + a liveness timestamp + a tags column;
-- rewrite the prior_dispatch_disposition CHECK to use 'stale_recovery'; drop 'event' from
-- the wait-set topic_kind CHECK.

-- Drop the named-event ledger entirely.
DROP TABLE IF EXISTS rimsky_node_events;

-- Drop parked-state columns no longer carried on the dispatch row,
-- the heartbeat timestamp column, and its supporting index.
DROP INDEX IF EXISTS rimsky_node_runs_heartbeat_idx;
ALTER TABLE rimsky_node_runs
    DROP COLUMN IF EXISTS parked_payload_inline,
    DROP COLUMN IF EXISTS parked_payload_handle,
    DROP COLUMN IF EXISTS parked_payload_handle_backend,
    DROP COLUMN IF EXISTS session_token,
    DROP COLUMN IF EXISTS wake_reason,
    DROP COLUMN IF EXISTS last_heartbeat_at;

-- The supervisor and claim-handle ledgers also carry heartbeat timestamps;
-- drop those columns (and any indexes on them) now that orphan detection
-- keys on last_progress_at + RPC connection state instead.
DROP INDEX IF EXISTS rimsky_supervisors_last_heartbeat_at_idx;
ALTER TABLE rimsky_supervisors DROP COLUMN IF EXISTS last_heartbeat_at;
ALTER TABLE rimsky_claim_handles DROP COLUMN IF EXISTS last_heartbeat_at;

-- Add async-callback registry + liveness + tags + denormalized dispatch deadlines.
-- effective_max_quiet_period_seconds / effective_max_runtime_seconds are
-- denormalized at AwaitAsyncCallback-registration time from the per-node
-- TemplateNodeDef + deployment defaults so SweepExecutorDeadlines does
-- not need a per-row template join. NULL = disabled (operator default).
ALTER TABLE rimsky_node_runs
    ADD COLUMN async_ack_id                       TEXT NULL,
    ADD COLUMN async_ack_registered_at            TIMESTAMPTZ NULL,
    ADD COLUMN last_progress_at                   TIMESTAMPTZ NULL,
    ADD COLUMN tags                               TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    ADD COLUMN effective_max_quiet_period_seconds INTEGER NULL,
    ADD COLUMN effective_max_runtime_seconds      INTEGER NULL;

-- Indexed lookup for the callback handler.
CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
    ON rimsky_node_runs (async_ack_id)
    WHERE async_ack_id IS NOT NULL;

-- Rewrite prior_dispatch_disposition CHECK: heartbeat_stale -> stale_recovery.
-- Constraint name follows the postgres auto-generated convention for
-- inline CHECKs on table+column: rimsky_node_runs_prior_dispatch_disposition_check.
ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT IF EXISTS rimsky_node_runs_prior_dispatch_disposition_check;
ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_prior_dispatch_disposition_check
    CHECK (prior_dispatch_disposition IS NULL
           OR prior_dispatch_disposition IN ('stale_recovery', 'retry_after_error', 'recalculate'));

-- Drop 'event' from wait-set topic_kind CHECK.
ALTER TABLE rimsky_wait_set
    DROP CONSTRAINT IF EXISTS rimsky_wait_set_topic_kind_check;
ALTER TABLE rimsky_wait_set
    ADD CONSTRAINT rimsky_wait_set_topic_kind_check
    CHECK (topic_kind IN ('state', 'attribute', 'transient', 'terminal'));
