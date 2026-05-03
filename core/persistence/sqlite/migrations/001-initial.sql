-- 001-initial.sql — SQLite initial schema (Task 32).
--
-- Hand-written SQLite-dialect translation of the post-003 Postgres
-- schema (the union of postgres/migrations/001-initial.sql,
-- 002-frame-resolution.sql, and 003-template-registry-and-lifecycle.sql
-- collapsed into a single migration). Per spec §5.1 SQLite ships one
-- consolidated init file rather than mirroring the Postgres migration
-- tree — there is no need to replay schema history against a fresh
-- SQLite DB.
--
-- Dialect drift rules applied (per spec §5.4):
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
-- Foreign-key enforcement requires `PRAGMA foreign_keys=ON` per
-- connection; the driver sets it via _foreign_keys=ON in the DSN.

CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Templates (control-plane v1: TEXT id = "sha256-<hex>" content-addressed hash).
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
    id              TEXT PRIMARY KEY,
    template_hash   TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key    TEXT,
    params          TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at   TEXT,
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- Frames (frame-resolution semantics).
CREATE TABLE IF NOT EXISTS rimsky_frames (
    frame_id          TEXT PRIMARY KEY,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    mode              TEXT NOT NULL CHECK (mode IN ('coalesce','serial_queue')),
    state             TEXT NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids   TEXT NOT NULL,                       -- JSON array of node-id strings
    queued_at         TEXT NOT NULL DEFAULT (datetime('now')),
    started_at        TEXT,
    ended_at          TEXT,
    frame_timeout_ms  INTEGER NOT NULL CHECK (frame_timeout_ms >= 60000),
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
    WHERE state = 'queued' AND mode = 'coalesce';

-- Nodes (one per template node, per instance).
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                      TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    schedule_cron           TEXT,
    state                   TEXT NOT NULL,
    dependencies            TEXT NOT NULL DEFAULT '[]',     -- JSON array of node-id strings
    current_error_class     TEXT,
    retry_counter           INTEGER NOT NULL DEFAULT 0,
    action_index            INTEGER NOT NULL DEFAULT 0,
    last_heartbeat_at       TEXT,
    assigned_supervisor_id  TEXT,
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
    accepted_executors  TEXT NOT NULL,                      -- JSON array of strings
    accepted_stores     TEXT NOT NULL DEFAULT '[]',         -- JSON array of strings
    concurrency         INTEGER NOT NULL,
    callback_host       TEXT,
    callback_port       INTEGER,
    last_heartbeat_at   TEXT NOT NULL DEFAULT (datetime('now')),
    active_node_count   INTEGER NOT NULL DEFAULT 0,
    registered_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- Dispatch queue.
CREATE TABLE IF NOT EXISTS rimsky_dispatch (
    id                  TEXT PRIMARY KEY,
    node_id             TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name       TEXT,
    required_stores     TEXT NOT NULL DEFAULT '[]',         -- JSON array of strings
    enqueued_at         TEXT NOT NULL DEFAULT (datetime('now')),
    claimed_by          TEXT,
    claimed_at          TEXT,
    last_heartbeat_at   TEXT,
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    UNIQUE (node_id)
);
CREATE INDEX IF NOT EXISTS rimsky_dispatch_pending_idx   ON rimsky_dispatch (enqueued_at) WHERE claimed_by IS NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_claimed_idx   ON rimsky_dispatch (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_heartbeat_idx ON rimsky_dispatch (last_heartbeat_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame
    ON rimsky_dispatch (frame_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame_claimed
    ON rimsky_dispatch (frame_id) WHERE claimed_by IS NOT NULL;

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

-- Per-node attribute snapshot.
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id     TEXT PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt INTEGER NOT NULL DEFAULT 0,
    data        TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Lock ledger (named + region kinds).
CREATE TABLE IF NOT EXISTS rimsky_lock_holders (
    id                   TEXT PRIMARY KEY,
    lock_kind            TEXT NOT NULL CHECK (lock_kind IN ('named','region')),
    lock_name            TEXT,
    store_name           TEXT,
    region_data          TEXT,
    address              TEXT,
    intent               TEXT,
    holder_supervisor_id TEXT NOT NULL,
    holder_node_id       TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at           TEXT NOT NULL DEFAULT (datetime('now')),
    last_heartbeat_at    TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at           TEXT NOT NULL,
    frame_id             TEXT,
    CHECK (
        (lock_kind = 'named'  AND lock_name IS NOT NULL AND store_name IS NULL     AND region_data IS NULL     AND intent IS NULL) OR
        (lock_kind = 'region' AND lock_name IS NULL     AND store_name IS NOT NULL AND region_data IS NOT NULL AND intent IN ('r','rw'))
    )
);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_supervisor ON rimsky_lock_holders (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_node       ON rimsky_lock_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_named      ON rimsky_lock_holders (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_region     ON rimsky_lock_holders (store_name) WHERE lock_kind = 'region';
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_expires    ON rimsky_lock_holders (expires_at) WHERE expires_at IS NOT NULL;

-- Held-claim ledger.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id              TEXT PRIMARY KEY,
    lock_holder_id  TEXT NOT NULL REFERENCES rimsky_lock_holders(id) ON DELETE CASCADE,
    holder_node_id  TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    state           TEXT NOT NULL CHECK (state IN ('active','completed','failed')),
    completed_at    TEXT,
    frame_id        TEXT,
    UNIQUE (lock_holder_id, holder_node_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_lock_holder    ON rimsky_claim_holders (lock_holder_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_node           ON rimsky_claim_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (lock_holder_id) WHERE state = 'active';

-- Store lifecycle bookkeeping.
CREATE TABLE IF NOT EXISTS rimsky_store_lifecycle (
    store_registration_name TEXT NOT NULL,
    scope_kind              TEXT NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT NOT NULL,
    state                   TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_store_lifecycle_scope
    ON rimsky_store_lifecycle (scope_kind, scope_id);
