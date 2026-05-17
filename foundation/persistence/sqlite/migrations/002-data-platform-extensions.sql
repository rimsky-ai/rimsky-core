-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Data Platform Extensions schema migration (SQLite mirror of postgres
-- 002-data-platform-extensions.sql).
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
--
-- Pre-v1 break-freely. SQLite stores BLOB for BYTEA, INTEGER for
-- BOOLEAN, TIMESTAMP for TIMESTAMPTZ, TEXT for JSONB. SQLite supports
-- ALTER TABLE … ADD COLUMN; column-add is the additive shape.

-- ---------------------------------------------------------------------
-- B2. rimsky_node_runs — run-tree + state lifted from rimsky_nodes
-- ---------------------------------------------------------------------

ALTER TABLE rimsky_node_runs ADD COLUMN parent_run_id TEXT NULL
    REFERENCES rimsky_node_runs(id) ON DELETE SET NULL;
-- Defense in depth for the partial unique index in 003-run-row-lifecycle.sql
-- (`uq_node_runs_in_flight_per_child` on `(parent_run_id, child_key)`):
-- SQLite, like Postgres, treats two rows with NULL `child_key` as
-- distinct under a multi-column unique index, so a child row without
-- `child_key` would silently bypass "one in-flight run per (parent,
-- child_key) partition". The column-level CHECK enforces that any row
-- with a non-NULL parent_run_id MUST carry a non-NULL child_key.
-- SQLite does not support adding a table-level CHECK via ALTER TABLE
-- post-creation; the CHECK lives on the column ADD itself and reads
-- the parent_run_id added immediately above.
ALTER TABLE rimsky_node_runs ADD COLUMN child_key TEXT NULL
    CHECK (parent_run_id IS NULL OR child_key IS NOT NULL);
ALTER TABLE rimsky_node_runs ADD COLUMN aggregation_policy TEXT NULL;
ALTER TABLE rimsky_node_runs ADD COLUMN state TEXT NOT NULL DEFAULT 'stale'
    CHECK (state IN ('fresh','stale','running','failed','parked'));
ALTER TABLE rimsky_node_runs ADD COLUMN last_outcome TEXT NOT NULL DEFAULT 'fresh_unchanged'
    CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed'));
ALTER TABLE rimsky_node_runs ADD COLUMN parked_reason_label TEXT NULL;
ALTER TABLE rimsky_node_runs ADD COLUMN parked_resume_at TIMESTAMP NULL;

CREATE INDEX IF NOT EXISTS idx_node_runs_parent_run_id
    ON rimsky_node_runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

-- ---------------------------------------------------------------------
-- B4. rimsky_claim_handles — sub-claim / lifetime / version / candidate
-- ---------------------------------------------------------------------

ALTER TABLE rimsky_claim_handles ADD COLUMN parent_claim_handle_id TEXT NULL
    REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL;
ALTER TABLE rimsky_claim_handles ADD COLUMN lifetime TEXT NOT NULL DEFAULT 'subgraph'
    CHECK (lifetime IN ('subgraph','durable'));
ALTER TABLE rimsky_claim_handles ADD COLUMN held_durable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rimsky_claim_handles ADD COLUMN version_id TEXT NULL;
ALTER TABLE rimsky_claim_handles ADD COLUMN producer_candidate_handle BLOB NULL;

CREATE INDEX IF NOT EXISTS idx_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_claim_handles_held_durable
    ON rimsky_claim_handles(held_durable) WHERE held_durable = 1;

-- ---------------------------------------------------------------------
-- B7. rimsky_messages — unified message queue
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_messages (
    id                     TEXT PRIMARY KEY,
    instance_id            TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    kind                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','sensor','instance')),
    target                 TEXT NULL,
    payload                BLOB NULL,
    backfill_operation_id  TEXT NULL,
    received_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at           TIMESTAMP NULL,
    frame_id               TEXT NULL,
    cancelled              INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_messages_instance_received
    ON rimsky_messages(instance_id, received_at);
CREATE INDEX IF NOT EXISTS idx_messages_backfill
    ON rimsky_messages(backfill_operation_id)
    WHERE backfill_operation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_pending
    ON rimsky_messages(instance_id, delivered_at)
    WHERE delivered_at IS NULL;

-- ---------------------------------------------------------------------
-- B8. rimsky_lineage — content lineage projection
-- (SQLite stores record as TEXT JSON; GIN index dropped, replaced by a
-- broad lookup index on the JSON text for grep-based queries.)
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_lineage (
    id           TEXT PRIMARY KEY,
    record_kind  TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_commit')),
    instance_id  TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     TEXT NOT NULL,
    observed_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    record       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lineage_run
    ON rimsky_lineage(record_kind, json_extract(record, '$.run_id'));
CREATE INDEX IF NOT EXISTS idx_lineage_claim
    ON rimsky_lineage(record_kind, json_extract(record, '$.claim_handle_id'));

-- ---------------------------------------------------------------------
-- B9. rimsky_sensor_watches — sensor lifecycle state
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_sensor_watches (
    id                TEXT PRIMARY KEY,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sensor_name       TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    on_observation    TEXT NOT NULL,
    started_at        TIMESTAMP NULL,
    last_observed_at  TIMESTAMP NULL,
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active','failed','stopped'))
);
CREATE INDEX IF NOT EXISTS idx_sensor_watches_instance
    ON rimsky_sensor_watches(instance_id);
CREATE INDEX IF NOT EXISTS idx_sensor_watches_state
    ON rimsky_sensor_watches(state) WHERE state = 'active';

-- ---------------------------------------------------------------------
-- B11. rimsky_instances.frame_delivery_mode
-- ---------------------------------------------------------------------

ALTER TABLE rimsky_instances ADD COLUMN frame_delivery_mode TEXT NOT NULL DEFAULT 'coalesce'
    CHECK (frame_delivery_mode IN ('serial_queue','coalesce'));
