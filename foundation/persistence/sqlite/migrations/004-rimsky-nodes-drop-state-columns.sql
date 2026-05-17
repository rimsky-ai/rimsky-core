-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Stage-3 of the run-row lifecycle cutover (SQLite mirror of postgres
-- 004-rimsky-nodes-drop-state-columns.sql).
--
-- SQLite-is-dev-only + pre-v1 break-freely: full table rebuild because
-- SQLite's ALTER TABLE DROP COLUMN was added in 3.35 but works only for
-- columns without constraints. Doing the rebuild is the conservative
-- choice and parallels the migration-003 pattern.
--
-- Drops the state / last_outcome / last_heartbeat_at /
-- assigned_supervisor_id columns from rimsky_nodes; keeps identity +
-- scheduling metadata (id, instance_id, node_type, executor,
-- schedule_cron, current_error_class, retry_counter, action_index,
-- frame_id, created_at, updated_at).

PRAGMA foreign_keys = OFF;

CREATE TABLE rimsky_nodes__new (
    id                      TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    schedule_cron           TEXT,
    current_error_class     TEXT,
    retry_counter           INTEGER NOT NULL DEFAULT 0,
    action_index            INTEGER NOT NULL DEFAULT 0,
    frame_id                TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    created_at              TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at              TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO rimsky_nodes__new
    (id, instance_id, node_type, executor, schedule_cron,
     current_error_class, retry_counter, action_index, frame_id,
     created_at, updated_at)
SELECT
    id, instance_id, node_type, executor, schedule_cron,
    current_error_class, retry_counter, action_index, frame_id,
    created_at, updated_at
FROM rimsky_nodes;

DROP TABLE rimsky_nodes;
ALTER TABLE rimsky_nodes__new RENAME TO rimsky_nodes;

-- Recreate the surviving indexes (the state-bearing
-- rimsky_nodes_state_updated_at_idx + idx_rimsky_nodes_frame_state are
-- gone with the columns; the run-table now indexes the same lookups).
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx
    ON rimsky_nodes (instance_id, node_type);

PRAGMA foreign_keys = ON;
