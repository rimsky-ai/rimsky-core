-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Run-row lifecycle flip (SQLite mirror of postgres 003-run-row-lifecycle.sql).
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
--
-- Pre-v1 break-freely + SQLite-is-dev-only: we do a full table rebuild
-- (SQLite has no `ALTER TABLE … DROP CONSTRAINT` and no `ALTER TABLE …
-- ADD CONSTRAINT`). The rebuild drops the table-level UNIQUE(node_id)
-- and widens the phase CHECK to admit 'failed'. A partial unique
-- index on (node_id) WHERE phase IN ('pending','active','held','parked')
-- enforces the runtime "at most one in-flight row per node" invariant
-- without capping terminal rows.

PRAGMA foreign_keys = OFF;

CREATE TABLE rimsky_node_runs__new (
    id                                  TEXT PRIMARY KEY,
    node_id                             TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT NOT NULL DEFAULT '[]',
    enqueued_at                         TEXT NOT NULL DEFAULT (datetime('now')),
    claimed_by                          TEXT,
    claimed_at                          TEXT,
    last_heartbeat_at                   TEXT,
    phase                               TEXT NOT NULL DEFAULT 'pending'
                                        CHECK (phase IN ('pending','active','held','parked','completed','failed')),
    active_terminal_at                  TEXT,
    frame_id                            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TEXT,
    resume_at                           TEXT,
    parked_payload_inline               BLOB,
    parked_payload_handle               TEXT,
    parked_payload_handle_backend       TEXT,
    session_token                       TEXT,
    parked_reason                       TEXT,
    parked_reason_note                  TEXT,
    wake_reason                         TEXT,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds           INTEGER,
    max_retries_without_progress        INTEGER,
    -- Columns added by migration 002 (B2 run-tree + state lift)
    parent_run_id                       TEXT REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    child_key                           TEXT,
    aggregation_policy                  TEXT,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    last_outcome                        TEXT NOT NULL DEFAULT 'fresh_unchanged'
                                        CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed')),
    parked_reason_label                 TEXT,
    parked_resume_at                    TIMESTAMP
);

-- Copy all columns from the old table.
INSERT INTO rimsky_node_runs__new
SELECT
    id, node_id, executor_name, required_stores, enqueued_at,
    claimed_by, claimed_at, last_heartbeat_at, phase, active_terminal_at,
    frame_id, parked_at, resume_at, parked_payload_inline,
    parked_payload_handle, parked_payload_handle_backend, session_token,
    parked_reason, parked_reason_note, wake_reason,
    consecutive_retries_no_progress, max_park_duration_seconds,
    max_retries_without_progress, parent_run_id, child_key,
    aggregation_policy, state, last_outcome, parked_reason_label,
    parked_resume_at
FROM rimsky_node_runs;

DROP TABLE rimsky_node_runs;
ALTER TABLE rimsky_node_runs__new RENAME TO rimsky_node_runs;

-- Recreate the indexes that lived on the original table.
CREATE INDEX IF NOT EXISTS rimsky_node_runs_pending_idx
    ON rimsky_node_runs (enqueued_at) WHERE phase = 'pending';
CREATE INDEX IF NOT EXISTS rimsky_node_runs_claimed_idx
    ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_node_runs_heartbeat_idx
    ON rimsky_node_runs (last_heartbeat_at) WHERE phase = 'active';
CREATE INDEX IF NOT EXISTS rimsky_node_runs_phase_idx
    ON rimsky_node_runs (phase);
CREATE INDEX IF NOT EXISTS idx_rimsky_node_runs_frame
    ON rimsky_node_runs (frame_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_node_runs_frame_claimed
    ON rimsky_node_runs (frame_id) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_node_runs_parent_run_id
    ON rimsky_node_runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

-- Partial unique indexes enforce "at most one in-flight row" per node,
-- split by parent_run_id IS NULL / NOT NULL so root runs keep the
-- original (node_id) guarantee while fan-out / sub-graph children
-- coexist under their parent. Mirrors the postgres migration; see the
-- block-comment there for the rationale.
CREATE UNIQUE INDEX IF NOT EXISTS uq_node_runs_in_flight_per_root_node
    ON rimsky_node_runs (node_id)
    WHERE parent_run_id IS NULL
      AND phase IN ('pending','active','held','parked');

CREATE UNIQUE INDEX IF NOT EXISTS uq_node_runs_in_flight_per_child
    ON rimsky_node_runs (parent_run_id, child_key)
    WHERE parent_run_id IS NOT NULL
      AND phase IN ('pending','active','held','parked');

PRAGMA foreign_keys = ON;
