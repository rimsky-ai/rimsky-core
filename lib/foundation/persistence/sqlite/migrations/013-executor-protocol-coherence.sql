-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 013-executor-protocol-coherence.sql (SQLite mirror of postgres 013).
--
-- Reshape rimsky_node_runs for the post-streaming executor protocol:
--   - drop the rimsky_node_events ledger;
--   - drop parked-payload / session-token / wake-reason / last-heartbeat columns
--     from rimsky_node_runs (table rebuild because the prior_dispatch_disposition
--     CHECK also needs rewriting from 'heartbeat_stale' to 'stale_recovery');
--   - add async-callback-registry columns + a liveness timestamp + tags column;
--   - drop the supervisor and claim-handle last_heartbeat_at columns;
--   - drop 'event' from the wait-set topic_kind CHECK (table rebuild).
--
-- No BEGIN/COMMIT wrapper: the migrator's ApplyOne opens a tx around the
-- script execution, so wrapping here trips SQLite's "cannot start a
-- transaction within a transaction" guard.

-- Drop the named-event ledger entirely.
DROP TABLE IF EXISTS rimsky_node_events;

-- Drop the heartbeat index before the column it covers goes away.
DROP INDEX IF EXISTS rimsky_node_runs_heartbeat_idx;

-- ===== rimsky_node_runs rebuild =====
--
-- Drops columns: parked_payload_inline, parked_payload_handle,
-- parked_payload_handle_backend, session_token, wake_reason, last_heartbeat_at.
-- Adds columns: async_ack_id, async_ack_registered_at, last_progress_at, tags
-- (TEXT, JSON-encoded array since SQLite has no native array type).
-- Rewrites prior_dispatch_disposition CHECK to use 'stale_recovery' in place
-- of 'heartbeat_stale'.

CREATE TABLE rimsky_node_runs_new (
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
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    run_scope_id                        TEXT NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    prior_dispatch_id                   TEXT NULL REFERENCES rimsky_node_runs_new(id) ON DELETE SET NULL,
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

INSERT INTO rimsky_node_runs_new
    (id, node_id, executor_name, required_stores, enqueued_at, claimed_by,
     claimed_at, phase, active_terminal_at, frame_id, parked_at, resume_at,
     parked_reason, parked_reason_label, parked_reason_note, parked_resume_at,
     consecutive_retries_no_progress, max_park_duration_seconds,
     max_retries_without_progress, aggregation_policy, state, run_scope_id,
     prior_dispatch_id, prior_dispatch_disposition, settling_signal_type,
     scratch_inline, scratch_handle, scratch_handle_backend)
SELECT
    id, node_id, executor_name, required_stores, enqueued_at, claimed_by,
    claimed_at, phase, active_terminal_at, frame_id, parked_at, resume_at,
    parked_reason, parked_reason_label, parked_reason_note, parked_resume_at,
    consecutive_retries_no_progress, max_park_duration_seconds,
    max_retries_without_progress, aggregation_policy, state, run_scope_id,
    prior_dispatch_id,
    CASE prior_dispatch_disposition
        WHEN 'heartbeat_stale' THEN 'stale_recovery'
        ELSE prior_dispatch_disposition
    END,
    settling_signal_type,
    scratch_inline, scratch_handle, scratch_handle_backend
FROM rimsky_node_runs;

DROP TABLE rimsky_node_runs;

ALTER TABLE rimsky_node_runs_new RENAME TO rimsky_node_runs;

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
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
    ON rimsky_node_runs (async_ack_id)
    WHERE async_ack_id IS NOT NULL;

-- ===== rimsky_supervisors / rimsky_claim_handles heartbeat columns =====
--
-- SQLite supports ALTER TABLE DROP COLUMN since 3.35 (2021-03);
-- modernc.org/sqlite ships 3.42+. The drop is straightforward.

DROP INDEX IF EXISTS rimsky_supervisors_last_heartbeat_at_idx;
ALTER TABLE rimsky_supervisors DROP COLUMN last_heartbeat_at;
ALTER TABLE rimsky_claim_handles DROP COLUMN last_heartbeat_at;

-- ===== rimsky_wait_set rebuild to drop 'event' from topic_kind CHECK =====

CREATE TABLE rimsky_wait_set_new (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','transient','terminal')),
    subscription_scope  TEXT NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    drained_at          TEXT,
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);

INSERT INTO rimsky_wait_set_new
    (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope,
     topic_filter, inserted_at, drained_at)
SELECT
    frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope,
    topic_filter, inserted_at, drained_at
FROM rimsky_wait_set
WHERE topic_kind <> 'event';

DROP TABLE rimsky_wait_set;

ALTER TABLE rimsky_wait_set_new RENAME TO rimsky_wait_set;

CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);
