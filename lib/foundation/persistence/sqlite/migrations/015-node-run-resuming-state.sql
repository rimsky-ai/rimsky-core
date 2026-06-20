-- 015-node-run-resuming-state.sql (SQLite mirror of postgres 015).
--
-- Adds the 'resuming' value to the rimsky_node_runs.state CHECK constraint and
-- extends the dispatch-eligibility partial index to cover it. The 'resuming'
-- state distinguishes "this node-run is waking from a deadline-driven park and
-- must re-use its dispatch-time substitution snapshot" from "this node-run
-- transitioned to stale via cascade and needs fresh substitution at dispatch."
-- The dispatcher branches on the state to decide whether to rebuild
-- substitution or load the persisted attributes bag verbatim.
--
-- SQLite can't ALTER CHECK constraints in place, so this is a table rebuild.
-- DROP TABLE in SQLite does NOT fire FK cascade actions (per SQLite docs),
-- so dependent rows (rimsky_wait_set, rimsky_node_attributes,
-- rimsky_claim_handles, rimsky_claim_holders) survive intact and re-bind to
-- the renamed table by name. Indexes are dropped first and recreated after.

DROP INDEX IF EXISTS rimsky_node_runs_pending_idx;
DROP INDEX IF EXISTS rimsky_node_runs_claimed_idx;
DROP INDEX IF EXISTS rimsky_node_runs_phase_idx;
DROP INDEX IF EXISTS idx_rimsky_node_runs_frame;
DROP INDEX IF EXISTS idx_rimsky_node_runs_frame_claimed;
DROP INDEX IF EXISTS idx_node_run_parked_resume;
DROP INDEX IF EXISTS idx_node_runs_node_frame;
DROP INDEX IF EXISTS idx_node_runs_state;
DROP INDEX IF EXISTS uq_node_runs_in_flight_per_run_scope;
DROP INDEX IF EXISTS idx_node_runs_run_scope;
DROP INDEX IF EXISTS rimsky_node_runs_async_ack_id_idx;

CREATE TABLE rimsky_node_runs_resuming (
    id                                  TEXT PRIMARY KEY,
    node_id                             TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT NOT NULL DEFAULT '[]',
    enqueued_at                         TEXT NOT NULL DEFAULT (datetime('now')),
    claimed_by                          TEXT,
    claimed_at                          TEXT,
    phase                               TEXT NOT NULL DEFAULT 'pending'
                                        CHECK (phase IN ('pending','active','held','parked','completed','failed')),
    active_terminal_at                  TEXT,
    frame_id                            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TEXT,
    resume_at                           TEXT,
    parked_reason                       TEXT,
    parked_reason_label                 TEXT,
    parked_reason_note                  TEXT,
    parked_resume_at                    TIMESTAMP,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds           INTEGER,
    max_retries_without_progress        INTEGER,
    aggregation_policy                  TEXT,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked','resuming')),
    run_scope_id                        TEXT NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    prior_dispatch_id                   TEXT NULL REFERENCES rimsky_node_runs_resuming(id) ON DELETE SET NULL,
    prior_dispatch_disposition          TEXT NULL
                                        CHECK (prior_dispatch_disposition IS NULL
                                               OR prior_dispatch_disposition IN ('stale_recovery', 'retry_after_error', 'recalculate')),
    settling_signal_type                TEXT,
    scratch_inline                      BLOB,
    scratch_handle                      TEXT,
    scratch_handle_backend              TEXT,
    async_ack_id                        TEXT,
    async_ack_registered_at             TEXT,
    last_progress_at                    TEXT,
    tags                                TEXT NOT NULL DEFAULT '[]',
    effective_max_quiet_period_seconds  INTEGER,
    effective_max_runtime_seconds       INTEGER
);

INSERT INTO rimsky_node_runs_resuming
    (id, node_id, executor_name, required_stores, enqueued_at, claimed_by,
     claimed_at, phase, active_terminal_at, frame_id, parked_at, resume_at,
     parked_reason, parked_reason_label, parked_reason_note, parked_resume_at,
     consecutive_retries_no_progress, max_park_duration_seconds,
     max_retries_without_progress, aggregation_policy, state, run_scope_id,
     prior_dispatch_id, prior_dispatch_disposition, settling_signal_type,
     scratch_inline, scratch_handle, scratch_handle_backend,
     async_ack_id, async_ack_registered_at, last_progress_at, tags,
     effective_max_quiet_period_seconds, effective_max_runtime_seconds)
SELECT
    id, node_id, executor_name, required_stores, enqueued_at, claimed_by,
    claimed_at, phase, active_terminal_at, frame_id, parked_at, resume_at,
    parked_reason, parked_reason_label, parked_reason_note, parked_resume_at,
    consecutive_retries_no_progress, max_park_duration_seconds,
    max_retries_without_progress, aggregation_policy, state, run_scope_id,
    prior_dispatch_id, prior_dispatch_disposition, settling_signal_type,
    scratch_inline, scratch_handle, scratch_handle_backend,
    async_ack_id, async_ack_registered_at, last_progress_at, tags,
    effective_max_quiet_period_seconds, effective_max_runtime_seconds
FROM rimsky_node_runs;

DROP TABLE rimsky_node_runs;

ALTER TABLE rimsky_node_runs_resuming RENAME TO rimsky_node_runs;

CREATE INDEX rimsky_node_runs_pending_idx   ON rimsky_node_runs (enqueued_at) WHERE phase = 'pending';
CREATE INDEX rimsky_node_runs_claimed_idx   ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX rimsky_node_runs_phase_idx     ON rimsky_node_runs (phase);
CREATE INDEX idx_rimsky_node_runs_frame
    ON rimsky_node_runs (frame_id);
CREATE INDEX idx_rimsky_node_runs_frame_claimed
    ON rimsky_node_runs (frame_id) WHERE claimed_by IS NOT NULL;
CREATE INDEX idx_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;
CREATE INDEX idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running','resuming');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
    ON rimsky_node_runs (async_ack_id)
    WHERE async_ack_id IS NOT NULL;
