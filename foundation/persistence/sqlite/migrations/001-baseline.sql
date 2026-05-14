-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Baseline migration (SQLite). Reflects post-2026-05-12 nomenclature resolution.
-- Pre-v1; no rolling-deploy compat. Dev DBs require `rm /var/lib/rimsky/state.db`
-- before applying.
--
-- Dialect drift rules (per persistence-pluggable spec §6.3):
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

CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Templates (content-addressed: TEXT id = "sha256-<hex>").
CREATE TABLE IF NOT EXISTS rimsky_templates (
    id              TEXT PRIMARY KEY,
    spec            TEXT NOT NULL,
    state           TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TEXT NOT NULL DEFAULT (datetime('now')),
    source          TEXT NOT NULL DEFAULT 'direct'
);

CREATE TABLE IF NOT EXISTS rimsky_template_tags (
    tag             TEXT PRIMARY KEY,
    template_id     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

-- Instances (one per consumer registration).
CREATE TABLE IF NOT EXISTS rimsky_instances (
    id                  TEXT PRIMARY KEY,
    template_hash       TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key        TEXT,
    params              TEXT NOT NULL DEFAULT '{}',
    userdata_overrides  TEXT NOT NULL DEFAULT '{}',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at       TEXT,
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- Frames (frame-resolution semantics).
CREATE TABLE IF NOT EXISTS rimsky_frames (
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

CREATE INDEX IF NOT EXISTS idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_coalesce_queued
    ON rimsky_frames (instance_id)
    WHERE state = 'queued' AND frame_resolution_mode = 'coalesce';

-- Nodes (one per template node, per instance).
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                      TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    schedule_cron           TEXT,
    state                   TEXT NOT NULL,
    dependencies            TEXT NOT NULL DEFAULT '[]',
    current_error_class     TEXT,
    retry_counter           INTEGER NOT NULL DEFAULT 0,
    action_index            INTEGER NOT NULL DEFAULT 0,
    last_heartbeat_at       TEXT,
    assigned_supervisor_id  TEXT,
    last_outcome            TEXT,
    frame_id                TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    created_at              TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at              TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS rimsky_nodes_state_updated_at_idx ON rimsky_nodes (state, updated_at);
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);
CREATE INDEX IF NOT EXISTS idx_rimsky_nodes_frame_state
    ON rimsky_nodes (frame_id, state)
    WHERE state IN ('stale','running');

-- Supervisor registry.
CREATE TABLE IF NOT EXISTS rimsky_supervisors (
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
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- Node-run queue. phase column drives the active+held+parked lifecycle.
CREATE TABLE IF NOT EXISTS rimsky_node_runs (
    id                                  TEXT PRIMARY KEY,
    node_id                             TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT NOT NULL DEFAULT '[]',
    enqueued_at                         TEXT NOT NULL DEFAULT (datetime('now')),
    claimed_by                          TEXT,
    claimed_at                          TEXT,
    last_heartbeat_at                   TEXT,
    phase                               TEXT NOT NULL DEFAULT 'pending'
                                        CHECK (phase IN ('pending','active','held','parked','completed')),
    active_terminal_at                  TEXT,
    frame_id                            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TEXT,
    resume_at                           TEXT,
    parked_payload_inline               BLOB,
    parked_payload_handle               TEXT,
    parked_payload_handle_backend       TEXT,
    session_token                       TEXT,
    parked_reason                       TEXT,
    wake_reason                         TEXT,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds           INTEGER,
    max_retries_without_progress        INTEGER,
    UNIQUE (node_id)
);
CREATE INDEX IF NOT EXISTS rimsky_node_runs_pending_idx   ON rimsky_node_runs (enqueued_at) WHERE phase = 'pending';
CREATE INDEX IF NOT EXISTS rimsky_node_runs_claimed_idx   ON rimsky_node_runs (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_node_runs_heartbeat_idx ON rimsky_node_runs (last_heartbeat_at) WHERE phase = 'active';
CREATE INDEX IF NOT EXISTS rimsky_node_runs_phase_idx     ON rimsky_node_runs (phase);
CREATE INDEX IF NOT EXISTS idx_rimsky_node_runs_frame
    ON rimsky_node_runs (frame_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_node_runs_frame_claimed
    ON rimsky_node_runs (frame_id) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_node_run_parked_resume
    ON rimsky_node_runs(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;

-- Schedules.
CREATE TABLE IF NOT EXISTS rimsky_schedules (
    node_id        TEXT PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    cron_expr      TEXT NOT NULL,
    next_fire_at   TEXT NOT NULL,
    last_fired_at  TEXT
);
CREATE INDEX IF NOT EXISTS rimsky_schedules_next_fire_at_idx ON rimsky_schedules (next_fire_at);

-- Event log.
CREATE TABLE IF NOT EXISTS rimsky_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id TEXT REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     TEXT REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL,
    occurred_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS rimsky_events_node_id_occurred_at_idx ON rimsky_events (node_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS rimsky_events_instance_id_occurred_at_idx ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS rimsky_events_kind_occurred_at_idx ON rimsky_events (kind, occurred_at DESC);

-- Per-node attribute snapshot (with optional blob spill).
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id              TEXT PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt          INTEGER NOT NULL DEFAULT 0,
    data                 TEXT NOT NULL DEFAULT '{}',
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Claim handles.
CREATE TABLE IF NOT EXISTS rimsky_claim_handles (
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
    holder_supervisor_id        TEXT NOT NULL,
    holder_node_id              TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TEXT NOT NULL DEFAULT (datetime('now')),
    last_heartbeat_at           TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at                  TEXT NOT NULL,
    frame_id                    TEXT,
    CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND producer_name IS NULL     AND scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND scope_data IS NOT NULL AND intent IN ('r','rw'))
    )
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_scope        ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'scope';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = 1;

-- Held-claim subgraph state ledger.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id               TEXT PRIMARY KEY,
    claim_handle_id  TEXT NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_node_id   TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active','completed','failed')),
    completed_at     TEXT,
    frame_id         TEXT,
    UNIQUE (claim_handle_id, holder_node_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_claim_handle  ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_node          ON rimsky_claim_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- Lifecycle idempotency ledger.
CREATE TABLE IF NOT EXISTS rimsky_lifecycle_idempotencies (
    store_registration_name TEXT NOT NULL,
    scope_kind              TEXT NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT NOT NULL,
    state                   TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_lifecycle_idempotencies_scope
    ON rimsky_lifecycle_idempotencies (scope_kind, scope_id);

-- Orphan-blob tracking.
CREATE TABLE IF NOT EXISTS rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TEXT NOT NULL DEFAULT (datetime('now')),
    reap_after  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- Named-event ledger.
CREATE TABLE IF NOT EXISTS rimsky_node_events (
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
CREATE INDEX IF NOT EXISTS idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);
