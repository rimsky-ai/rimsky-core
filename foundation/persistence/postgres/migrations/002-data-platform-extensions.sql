-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Data Platform Extensions schema migration.
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
--
-- Pre-v1 break-freely (per .claude/rules/rules.md). This migration is the
-- additive portion of the spec's persistence shape — new tables and new
-- columns that downstream code can opt into without breaking existing
-- callers. The destructive portions (drop state columns from
-- rimsky_nodes; rename rimsky_claim_holders.holder_node → holder_run_id;
-- drop rimsky_schedules) are deferred to a follow-up migration after
-- the Go-side state-propagation work lands; sequencing is documented in
-- the plan's implementation-notes file.
--
-- Sections below match the plan's task IDs (B2 … B11, B13).

-- ---------------------------------------------------------------------
-- B2. rimsky_node_runs — run-tree + state lifted from rimsky_nodes
-- ---------------------------------------------------------------------

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS parent_run_id UUID NULL
        REFERENCES rimsky_node_runs(id) ON DELETE SET NULL;

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS child_key TEXT NULL;

-- Defense in depth for the partial unique index in 003-run-row-lifecycle.sql
-- (`uq_node_runs_in_flight_per_child` on `(parent_run_id, child_key)`):
-- Postgres treats two rows with the same `parent_run_id` and both
-- `child_key = NULL` as distinct under a multi-column unique index, so a
-- future writer that forgot to set `child_key` would silently bypass the
-- "one in-flight run per (parent, child_key) partition" guarantee. The
-- CHECK makes the schema self-defending: a child row MUST carry a
-- non-NULL `child_key`, while root rows (no parent) carry NULL on both.
ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT IF EXISTS rimsky_node_runs_child_key_check;
ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_child_key_check
    CHECK (parent_run_id IS NULL OR child_key IS NOT NULL);

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS aggregation_policy JSONB NULL;

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'stale'
        CHECK (state IN ('fresh','stale','running','failed','parked'));

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS last_outcome TEXT NOT NULL DEFAULT 'fresh_unchanged'
        CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed'));

-- parked_reason already exists on rimsky_node_runs; add the new label +
-- resume_at columns the spec wants. parked_reason_label is the freeform
-- tag required when parked_reason = 'other'.
ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS parked_reason_label TEXT NULL;

ALTER TABLE rimsky_node_runs
    ADD COLUMN IF NOT EXISTS parked_resume_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_node_runs_parent_run_id
    ON rimsky_node_runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX IF NOT EXISTS idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

-- ---------------------------------------------------------------------
-- B4. rimsky_claim_handles — sub-claim / lifetime / version / candidate
-- ---------------------------------------------------------------------

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS parent_claim_handle_id UUID NULL
        REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS lifetime TEXT NOT NULL DEFAULT 'subgraph'
        CHECK (lifetime IN ('subgraph','durable'));

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS held_durable BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS version_id TEXT NULL;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS producer_candidate_handle BYTEA NULL;

CREATE INDEX IF NOT EXISTS idx_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_claim_handles_held_durable
    ON rimsky_claim_handles(held_durable) WHERE held_durable = TRUE;

-- ---------------------------------------------------------------------
-- B7. rimsky_messages — unified message queue
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_messages (
    id                     UUID PRIMARY KEY,
    instance_id            UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    kind                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','sensor','instance')),
    target                 TEXT NULL,
    payload                BYTEA NULL,
    backfill_operation_id  UUID NULL,
    received_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at           TIMESTAMPTZ NULL,
    frame_id               UUID NULL,
    -- B13. cancelled marks pending messages cancelled by an operator
    -- backfill-cancellation flow. In-flight frames complete normally.
    cancelled              BOOLEAN NOT NULL DEFAULT FALSE
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
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_lineage (
    id           UUID PRIMARY KEY,
    record_kind  TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_commit')),
    instance_id  UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     UUID NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    record       JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lineage_run
    ON rimsky_lineage(record_kind, (record->>'run_id'));
CREATE INDEX IF NOT EXISTS idx_lineage_claim
    ON rimsky_lineage(record_kind, (record->>'claim_handle_id'));
CREATE INDEX IF NOT EXISTS idx_lineage_substitution_refs
    ON rimsky_lineage USING GIN (record);

-- ---------------------------------------------------------------------
-- B9. rimsky_sensor_watches — sensor lifecycle state
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rimsky_sensor_watches (
    id                UUID PRIMARY KEY,
    instance_id       UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sensor_name       TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   JSONB NOT NULL,
    on_observation    JSONB NOT NULL,
    started_at        TIMESTAMPTZ NULL,
    last_observed_at  TIMESTAMPTZ NULL,
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

ALTER TABLE rimsky_instances
    ADD COLUMN IF NOT EXISTS frame_delivery_mode TEXT NOT NULL DEFAULT 'coalesce'
        CHECK (frame_delivery_mode IN ('serial_queue','coalesce'));
