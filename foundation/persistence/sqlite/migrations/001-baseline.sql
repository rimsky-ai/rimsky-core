-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
--
-- Pre-v1 flattened baseline (SQLite). Expresses the final desired schema
-- as of 2026-05-17 — the cumulative effect of every prior migration
-- collapsed into a single file. Dev DBs must be wiped (`rm
-- /var/lib/rimsky/state.db`) before re-applying. SQLite is dev-only;
-- multi-host deployments must use Postgres.
--
-- Dialect drift rules (persistence-pluggable spec §6.3):
--   JSONB         → TEXT (caller marshals JSON)
--   UUID          → TEXT (caller stringifies)
--   gen_random_uuid() default → no default; app generates with uuid.New()
--   TIMESTAMPTZ + NOW() default → TEXT default (datetime('now')) (RFC3339)
--   BIGSERIAL     → INTEGER PRIMARY KEY AUTOINCREMENT
--   UUID[] / TEXT[] → TEXT (JSON array)
--   ON DELETE CASCADE / SET NULL preserved
--   Partial indexes preserved (SQLite 3.8+ supports the WHERE syntax)
--   CHECK constraints preserved
--
-- Foreign-key enforcement requires `PRAGMA foreign_keys=ON` per connection;
-- the driver sets it via _foreign_keys=ON in the DSN.

-- Note: rimsky_migrations is created by the driver's Bootstrap step
-- (see foundation/persistence/{postgres,sqlite}/migrate.go); it is
-- intentionally NOT declared here so a re-run that skips this file
-- (idempotency via the rimsky_migrations row) and a fresh run both
-- find the table in the same state.

-- =====  rimsky_templates  =====
CREATE TABLE rimsky_templates (
    id              TEXT PRIMARY KEY,
    spec            TEXT NOT NULL,
    state           TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TEXT NOT NULL DEFAULT (datetime('now')),
    source          TEXT NOT NULL DEFAULT 'direct'
);

CREATE TABLE rimsky_template_tags (
    tag             TEXT PRIMARY KEY,
    template_id     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

-- =====  rimsky_instances  =====
CREATE TABLE rimsky_instances (
    id                  TEXT PRIMARY KEY,
    template_hash       TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key        TEXT,
    params              TEXT NOT NULL DEFAULT '{}',
    userdata_overrides  TEXT NOT NULL DEFAULT '{}',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at       TEXT,
    frame_delivery_mode TEXT NOT NULL DEFAULT 'coalesce'
                        CHECK (frame_delivery_mode IN ('serial_queue','coalesce')),
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- =====  rimsky_frames  =====
CREATE TABLE rimsky_frames (
    frame_id                TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_resolution_mode   TEXT NOT NULL CHECK (frame_resolution_mode IN ('coalesce','serial_queue')),
    state                   TEXT NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids         TEXT NOT NULL,
    queued_at               TEXT NOT NULL DEFAULT (datetime('now')),
    started_at              TEXT,
    ended_at                TEXT,
    frame_timeout_ms        INTEGER NOT NULL CHECK (frame_timeout_ms >= 60000),
    last_progress_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (state != 'running' OR started_at IS NOT NULL),
    CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
);

CREATE INDEX idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';

CREATE UNIQUE INDEX uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

CREATE UNIQUE INDEX uq_rimsky_frames_coalesce_queued
    ON rimsky_frames (instance_id)
    WHERE state = 'queued' AND frame_resolution_mode = 'coalesce';

-- =====  rimsky_nodes  =====
-- Identity + scheduling metadata only. State-machine columns (state,
-- last_outcome, last_heartbeat_at, assigned_supervisor_id) and the
-- retired schedule_cron column live on rimsky_node_runs / sensors now.
CREATE TABLE rimsky_nodes (
    id                      TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    current_error_class     TEXT,
    retry_counter           INTEGER NOT NULL DEFAULT 0,
    action_index            INTEGER NOT NULL DEFAULT 0,
    frame_id                TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    created_at              TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at              TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);

-- =====  rimsky_supervisors  =====
CREATE TABLE rimsky_supervisors (
    id                  TEXT PRIMARY KEY,
    accepted_executors  TEXT NOT NULL,
    accepted_stores     TEXT NOT NULL DEFAULT '[]',
    concurrency         INTEGER NOT NULL,
    callback_host       TEXT,
    callback_port       INTEGER,
    last_heartbeat_at   TEXT NOT NULL DEFAULT (datetime('now')),
    active_node_count   INTEGER NOT NULL DEFAULT 0,
    registered_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- =====  rimsky_node_runs  =====
-- Per-run bookkeeping. Owns the phase + state-machine columns. See the
-- postgres baseline for the run-tree rationale; child rows must carry a
-- non-NULL child_key (CHECK constraint at the column level).
CREATE TABLE rimsky_node_runs (
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
    parked_reason_label                 TEXT,
    parked_reason_note                  TEXT,
    parked_resume_at                    TIMESTAMP,
    wake_reason                         TEXT,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds           INTEGER,
    max_retries_without_progress        INTEGER,
    parent_run_id                       TEXT REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    child_key                           TEXT
                                        CHECK (parent_run_id IS NULL OR child_key IS NOT NULL),
    aggregation_policy                  TEXT,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    last_outcome                        TEXT NOT NULL DEFAULT 'fresh_unchanged'
                                        CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed'))
);
CREATE INDEX rimsky_node_runs_pending_idx   ON rimsky_node_runs (enqueued_at) WHERE phase = 'pending';
CREATE INDEX rimsky_node_runs_claimed_idx   ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX rimsky_node_runs_heartbeat_idx ON rimsky_node_runs (last_heartbeat_at) WHERE phase = 'active';
CREATE INDEX rimsky_node_runs_phase_idx     ON rimsky_node_runs (phase);
CREATE INDEX idx_rimsky_node_runs_frame
    ON rimsky_node_runs (frame_id);
CREATE INDEX idx_rimsky_node_runs_frame_claimed
    ON rimsky_node_runs (frame_id) WHERE claimed_by IS NOT NULL;
CREATE INDEX idx_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;
CREATE INDEX idx_node_runs_parent_run_id
    ON rimsky_node_runs(parent_run_id);
CREATE INDEX idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_root_node
    ON rimsky_node_runs (node_id)
    WHERE parent_run_id IS NULL
      AND phase IN ('pending','active','held','parked');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_child
    ON rimsky_node_runs (parent_run_id, child_key)
    WHERE parent_run_id IS NOT NULL
      AND phase IN ('pending','active','held','parked');

-- =====  rimsky_wait_set  =====
-- Per-frame wait-set ledger keyed on receiver_run_id / sender_run_id
-- (per-run identity). See the postgres baseline for the cascade-walk
-- semantics.
CREATE TABLE rimsky_wait_set (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);
CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);

-- =====  rimsky_events  =====
CREATE TABLE rimsky_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id TEXT REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     TEXT REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL,
    occurred_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX rimsky_events_node_id_occurred_at_idx ON rimsky_events (node_id, occurred_at DESC);
CREATE INDEX rimsky_events_instance_id_occurred_at_idx ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX rimsky_events_kind_occurred_at_idx ON rimsky_events (kind, occurred_at DESC);

-- =====  rimsky_node_attributes  =====
CREATE TABLE rimsky_node_attributes (
    node_id              TEXT PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt          INTEGER NOT NULL DEFAULT 0,
    data                 TEXT NOT NULL DEFAULT '{}',
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

-- =====  rimsky_claim_handles  =====
-- Named + scope claim handles. state ∈ {active, committed, abandoned}
-- replaces the prior binary held_durable model; the two CHECK
-- constraints below bind holder presence to state.
CREATE TABLE rimsky_claim_handles (
    id                          TEXT PRIMARY KEY,
    node_run_id                 TEXT REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL CHECK (lock_kind IN ('named','scope')),
    lock_name                   TEXT,
    producer_name               TEXT,
    scope_data                  TEXT,
    address                     TEXT,
    intent                      TEXT,
    realized_write_semantics    TEXT,
    is_held                     INTEGER NOT NULL DEFAULT 0,
    holder_supervisor_id        TEXT,
    holder_node_id              TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TEXT NOT NULL DEFAULT (datetime('now')),
    last_heartbeat_at           TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at                  TEXT NOT NULL,
    frame_id                    TEXT,
    parent_claim_handle_id      TEXT REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL,
    lifetime                    TEXT NOT NULL DEFAULT 'subgraph'
                                CHECK (lifetime IN ('subgraph','durable')),
    version_id                  TEXT,
    producer_candidate_handle   BLOB,
    aggregation_policy          TEXT,
    expected_children_count     INTEGER NOT NULL DEFAULT 0,
    committed_children_count    INTEGER NOT NULL DEFAULT 0,
    abandoned_children_count    INTEGER NOT NULL DEFAULT 0,
    state                       TEXT NOT NULL DEFAULT 'active'
                                CHECK (state IN ('active','committed','abandoned')),
    resolved_at                 TIMESTAMP,
    CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND producer_name IS NULL     AND scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND scope_data IS NOT NULL AND intent IN ('r','rw'))
    ),
    CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL),
    CHECK (state = 'active' OR holder_supervisor_id IS NULL)
);
CREATE INDEX idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_claim_handles_scope        ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'scope';
CREATE INDEX idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = 1;
CREATE INDEX idx_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;
CREATE INDEX rimsky_claim_handles_active_idx
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';
CREATE INDEX rimsky_claim_handles_committed_durable_idx
    ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';

-- =====  rimsky_claim_holders  =====
-- Held-claim subgraph state ledger keyed on holder_run_id.
CREATE TABLE rimsky_claim_holders (
    id               TEXT PRIMARY KEY,
    claim_handle_id  TEXT NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_run_id    TEXT NOT NULL REFERENCES rimsky_node_runs(id)     ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active','completed','failed')),
    completed_at     TEXT,
    frame_id         TEXT,
    UNIQUE (claim_handle_id, holder_run_id)
);
CREATE INDEX idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX idx_rimsky_claim_holders_run          ON rimsky_claim_holders (holder_run_id);
CREATE INDEX idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- =====  rimsky_lifecycle_idempotencies  =====
CREATE TABLE rimsky_lifecycle_idempotencies (
    store_registration_name TEXT NOT NULL,
    scope_kind              TEXT NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT NOT NULL,
    state                   TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);
CREATE INDEX idx_rimsky_lifecycle_idempotencies_scope
    ON rimsky_lifecycle_idempotencies (scope_kind, scope_id);

-- =====  rimsky_blob_orphans  =====
CREATE TABLE rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TEXT NOT NULL DEFAULT (datetime('now')),
    reap_after  TEXT NOT NULL
);
CREATE INDEX idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- =====  rimsky_node_events  =====
CREATE TABLE rimsky_node_events (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id            TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    emitter_node_id        TEXT NOT NULL,
    event_name             TEXT NOT NULL,
    payload_inline         BLOB,
    payload_handle         TEXT,
    payload_handle_backend TEXT,
    emitted_at             TEXT NOT NULL DEFAULT (datetime('now')),
    frame_id               TEXT
);
CREATE INDEX idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);

-- =====  rimsky_messages  =====
-- Boundary-crossing message envelopes. V1 kind: 'invalidate'.
CREATE TABLE rimsky_messages (
    id                     TEXT PRIMARY KEY,
    instance_id            TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    kind                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','publisher','instance')),
    target                 TEXT,
    payload                BLOB,
    backfill_operation_id  TEXT,
    received_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at           TIMESTAMP,
    frame_id               TEXT,
    cancelled              INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_messages_instance_received
    ON rimsky_messages(instance_id, received_at);
CREATE INDEX idx_messages_backfill
    ON rimsky_messages(backfill_operation_id)
    WHERE backfill_operation_id IS NOT NULL;
CREATE INDEX idx_messages_pending
    ON rimsky_messages(instance_id, delivered_at)
    WHERE delivered_at IS NULL;

-- =====  rimsky_lineage  =====
-- Append-only lineage projection. record_kind ∈ {leaf_run, claim_terminal}
-- with outcome ∈ {'' (leaf_run), committed | abandoned | force_cancelled
-- (claim_terminal)}.
CREATE TABLE rimsky_lineage (
    id           TEXT PRIMARY KEY,
    record_kind  TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_terminal')),
    instance_id  TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     TEXT NOT NULL,
    observed_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    record       TEXT NOT NULL,
    outcome      TEXT NOT NULL
        CHECK (outcome IN ('','committed','abandoned','force_cancelled'))
);
CREATE INDEX idx_lineage_run
    ON rimsky_lineage(record_kind, json_extract(record, '$.run_id'));
CREATE INDEX idx_lineage_claim
    ON rimsky_lineage(record_kind, json_extract(record, '$.claim_handle_id'));

-- =====  rimsky_publisher_subscriptions  =====
-- Rimsky-side binding state per (publisher_name, publisher_subscription_id).
-- See foundation/persistence/postgres/migrations/001-baseline.sql for the
-- canonical column docstring.
CREATE TABLE rimsky_publisher_subscriptions (
    id                TEXT NOT NULL,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    target_node       TEXT NOT NULL,
    message_kind      TEXT NOT NULL DEFAULT 'invalidate',
    started_at        TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active','failed','stopped')),
    PRIMARY KEY (publisher_name, id)
);
CREATE INDEX idx_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);
CREATE INDEX idx_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state) WHERE state = 'active';

-- =====  rimsky_message_idempotencies  =====
-- Universal idempotency dedup table for POST /instances/{id}/messages.
-- See foundation/persistence/postgres/migrations/001-baseline.sql for the
-- canonical docstring.
CREATE TABLE rimsky_message_idempotencies (
    instance_id      TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sender           TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    message_id       TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (instance_id, sender, idempotency_key)
);
CREATE INDEX idx_message_idempotencies_created_at
    ON rimsky_message_idempotencies(created_at);
