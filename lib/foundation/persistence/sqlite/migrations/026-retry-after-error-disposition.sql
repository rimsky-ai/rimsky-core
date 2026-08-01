-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: node-run
--
-- 026-retry-after-error-disposition.sql
--
-- Extend the recovery-aware dispatch disposition vocabulary to the full
-- ruled set {stale_recovery, retry_after_error, recalculate}. See the
-- postgres sibling migration for rationale.
--
-- The disposition CHECK is an inline column constraint, so SQLite requires
-- a table rebuild (the migration-021 pattern): the migrator applies every
-- migration on a connection with foreign_keys OFF, so DROP TABLE performs
-- no implicit cascade, and DROP + RENAME under modern ALTER TABLE semantics
-- keeps child-table FK references (rimsky_wait_set, rimsky_breakpoint_hits,
-- claim tables) resolving by name against the rebuilt table. All indexes on
-- the table are recreated below; the migrator runs PRAGMA foreign_key_check
-- before commit.

CREATE TABLE rimsky_node_runs_new (
    id                                  TEXT PRIMARY KEY,
    node_id                             TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT NOT NULL DEFAULT '[]',
    enqueued_at                         TEXT NOT NULL DEFAULT (datetime('now')),
    claimed_by                          TEXT,
    claimed_at                          TEXT,
    active_terminal_at                  TEXT,
    frame_id                            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TEXT,
    resume_at                           TEXT,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_retries_without_progress        INTEGER,
    aggregation_policy                  TEXT,
    sequence                            INTEGER NOT NULL,
    creation_reason                     TEXT NOT NULL DEFAULT 'cascade'
                                        CHECK (creation_reason IN ('cascade','operator_invalidate','recalculate','message_delivery')),
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('pending','stale','running','held','parked','fresh','failed')),
    run_scope_id                        TEXT NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    prior_dispatch_id                   TEXT NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    prior_dispatch_disposition          TEXT NULL
                                        CHECK (prior_dispatch_disposition IS NULL
                                               OR prior_dispatch_disposition IN ('stale_recovery','retry_after_error','recalculate')),
    retry_counter                       INTEGER NOT NULL DEFAULT 0,
    settling_signal_type                TEXT,
    scratch_inline                      BLOB,
    scratch_handle                      TEXT,
    scratch_handle_backend              TEXT,
    async_ack_id                        TEXT,
    async_ack_registered_at             TEXT,
    last_progress_at                    TEXT,
    tags                                TEXT NOT NULL DEFAULT '[]',
    effective_max_quiet_period_seconds  INTEGER,
    effective_max_runtime_seconds       INTEGER,
    async_ack_principal                 TEXT
);

INSERT INTO rimsky_node_runs_new (
    id, node_id, executor_name, required_stores, enqueued_at,
    claimed_by, claimed_at, active_terminal_at, frame_id,
    parked_at, resume_at, consecutive_retries_no_progress,
    max_retries_without_progress, aggregation_policy, sequence,
    creation_reason, state, run_scope_id,
    prior_dispatch_id, prior_dispatch_disposition,
    retry_counter, settling_signal_type,
    scratch_inline, scratch_handle, scratch_handle_backend,
    async_ack_id, async_ack_registered_at, last_progress_at, tags,
    effective_max_quiet_period_seconds, effective_max_runtime_seconds,
    async_ack_principal
)
SELECT
    id, node_id, executor_name, required_stores, enqueued_at,
    claimed_by, claimed_at, active_terminal_at, frame_id,
    parked_at, resume_at, consecutive_retries_no_progress,
    max_retries_without_progress, aggregation_policy, sequence,
    creation_reason, state, run_scope_id,
    prior_dispatch_id, prior_dispatch_disposition,
    retry_counter, settling_signal_type,
    scratch_inline, scratch_handle, scratch_handle_backend,
    async_ack_id, async_ack_registered_at, last_progress_at, tags,
    effective_max_quiet_period_seconds, effective_max_runtime_seconds,
    async_ack_principal
FROM rimsky_node_runs;

DROP TABLE rimsky_node_runs;

ALTER TABLE rimsky_node_runs_new RENAME TO rimsky_node_runs;

CREATE INDEX idx_node_runs_dispatch_order
    ON rimsky_node_runs (node_id, run_scope_id, frame_id, sequence);

CREATE INDEX rimsky_node_runs_stale_idx
    ON rimsky_node_runs (enqueued_at) WHERE state = 'stale';

CREATE INDEX rimsky_node_runs_claimed_idx
    ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX rimsky_node_runs_state_idx
    ON rimsky_node_runs (state);
CREATE INDEX idx_rimsky_node_runs_frame
    ON rimsky_node_runs (frame_id);
CREATE INDEX idx_rimsky_node_runs_frame_claimed
    ON rimsky_node_runs (frame_id) WHERE claimed_by IS NOT NULL;
CREATE INDEX idx_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE state = 'parked' AND resume_at IS NOT NULL;
CREATE INDEX idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX idx_node_runs_state_eligible
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

CREATE UNIQUE INDEX uq_node_runs_serialization_gate
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE claimed_by IS NOT NULL
       OR state IN ('held','parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
    ON rimsky_node_runs (async_ack_id)
    WHERE async_ack_id IS NOT NULL;
