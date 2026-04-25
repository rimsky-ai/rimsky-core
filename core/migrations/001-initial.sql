-- Rimsky Go initial schema
--
-- Schema lineage: this file is a port of the TS v1 migrations
--   * rimsky/src/migrations/001-initial.sql
--   * rimsky/src/migrations/002-produced-by-on-delete-set-null.sql
-- folded into a single file. Go rimsky starts fresh, so no history is
-- preserved; instead, the deltas from spec §11.1 are applied inline:
--
--   * `rimsky_cells` renamed to `rimsky_nodes`; `kind` column dropped; new
--     nullable `executor` and `schedule_cron` columns added. All downstream
--     `cell_id` columns become `node_id`.
--   * `rimsky_timers` replaced by `rimsky_schedules` keyed by `node_id`
--     (schedule is a property of the node that fires, not a separate cell).
--     `target_cell_id` and `reason` are gone, so no DEFERRABLE FK is needed.
--   * `rimsky_dispatch.cell_kind` renamed to `executor_name`; `UNIQUE (cell_id)`
--     becomes `UNIQUE (node_id)`.
--   * `rimsky_supervisors.accepts` renamed to `accepted_executors`;
--     `active_cell_count` renamed to `active_node_count`.
--   * `rimsky_resource_versions.produced_by` is nullable with `ON DELETE SET
--     NULL` (folds in TS migration 002).
--   * `rimsky_events.cell_id` renamed to `node_id`.
--
-- Idempotent: `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`
-- throughout. Belt-and-suspenders with the migration runner's advisory lock
-- and per-file tracking in `rimsky_migrations`.

CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Templates (deploy targets)
CREATE TABLE IF NOT EXISTS rimsky_templates (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    version     TEXT NOT NULL,
    spec        JSONB NOT NULL,           -- parsed template YAML
    deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, version)
);

-- Graph instances (one per consumer registration)
CREATE TABLE IF NOT EXISTS rimsky_instances (
    id           UUID PRIMARY KEY,
    template_id  UUID NOT NULL REFERENCES rimsky_templates(id),
    consumer_key TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, consumer_key)
);

-- Node instances (one per node declared in a template, per graph instance).
-- `executor` is the supervisor executor name (null for pure-schedule nodes
-- that only invalidate on tick). `schedule_cron` is the optional cron
-- expression; when non-null a matching row exists in `rimsky_schedules`.
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                    UUID PRIMARY KEY,
    instance_id           UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type             TEXT NOT NULL,             -- template-declared type name
    executor              TEXT,                      -- supervisor executor name; null for schedule-only nodes
    schedule_cron         TEXT,                      -- cron expr if node is scheduled; null otherwise
    state                 TEXT NOT NULL,             -- fresh | stale | running | failed
    dependencies          UUID[] NOT NULL,           -- resolved to node ids at instantiation
    concurrency_tags      TEXT[] NOT NULL DEFAULT '{}',
    current_error_class   TEXT,
    retry_counter         INT NOT NULL DEFAULT 0,
    action_index          INT NOT NULL DEFAULT 0,
    last_heartbeat_at     TIMESTAMPTZ,               -- set while running; null otherwise
    assigned_supervisor_id TEXT,                     -- null if not running
    kill_requested        BOOLEAN NOT NULL DEFAULT FALSE,  -- operator-set; supervisor polls while running
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_nodes_state_updated_at_idx ON rimsky_nodes (state, updated_at);
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);

-- Resource registry (one row per resource; owned by exactly one node)
CREATE TABLE IF NOT EXISTS rimsky_resources (
    id                  UUID PRIMARY KEY,
    resource_path       TEXT[] NOT NULL,              -- structured ResourceId
    owner_node_id       UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    current_version_id  UUID,                         -- null until first commit
    previous_version_id UUID,
    keep_versions       INT NOT NULL DEFAULT 2,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_node_id, resource_path)
);
CREATE INDEX IF NOT EXISTS rimsky_resources_resource_path_gin_idx ON rimsky_resources USING GIN (resource_path);

-- Resource versions (append-only; GC'd by keep_versions on commit).
-- `produced_by` is nullable with ON DELETE SET NULL so deleting a producing
-- node preserves the historical version row instead of blocking the cascade.
CREATE TABLE IF NOT EXISTS rimsky_resource_versions (
    id              UUID PRIMARY KEY,
    resource_id     UUID NOT NULL REFERENCES rimsky_resources(id) ON DELETE CASCADE,
    produced_by     UUID REFERENCES rimsky_nodes(id) ON DELETE SET NULL,
    data            JSONB,                           -- inline storage; null if using external ResourceStore impl
    data_ref        TEXT,                            -- opaque reference for external impls
    change_summary  TEXT,
    committed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_resource_versions_resource_id_committed_at_idx ON rimsky_resource_versions (resource_id, committed_at DESC);

-- Supervisor registry (heartbeat + callback endpoints)
CREATE TABLE IF NOT EXISTS rimsky_supervisors (
    id                  TEXT PRIMARY KEY,            -- supervisor_id from config
    accepted_executors  TEXT[] NOT NULL,             -- executor names this supervisor handles
    concurrency         INT NOT NULL,
    callback_host       TEXT,                        -- null for deterministic-only supervisors
    callback_port       INT,
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_node_count   INT NOT NULL DEFAULT 0,
    registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- Dispatch queue (nodes ready to run)
CREATE TABLE IF NOT EXISTS rimsky_dispatch (
    id               UUID PRIMARY KEY,
    node_id          UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name    TEXT NOT NULL,                   -- which supervisor executor handles this node
    concurrency_tags TEXT[] NOT NULL DEFAULT '{}',
    enqueued_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- may be future-dated for backoff
    claimed_by       TEXT,                            -- supervisor id; null until claimed
    claimed_at       TIMESTAMPTZ,
    UNIQUE (node_id)                                  -- at most one pending dispatch per node
);
CREATE INDEX IF NOT EXISTS rimsky_dispatch_claim_idx ON rimsky_dispatch (claimed_by, enqueued_at) WHERE claimed_by IS NULL;

-- Event log (single append-only; JSONB payload)
CREATE TABLE IF NOT EXISTS rimsky_events (
    id          BIGSERIAL PRIMARY KEY,
    instance_id UUID REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     UUID REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_events_node_id_occurred_at_idx ON rimsky_events (node_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS rimsky_events_instance_id_occurred_at_idx ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS rimsky_events_kind_occurred_at_idx ON rimsky_events (kind, occurred_at DESC);

-- Schedule registry (one row per scheduled node). Keyed by node_id: when the
-- schedule fires, the node itself is invalidated. No separate target pointer,
-- so no DEFERRABLE FK is required.
CREATE TABLE IF NOT EXISTS rimsky_schedules (
    node_id        UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    cron_expr      TEXT NOT NULL,                    -- standard cron expression, UTC
    next_fire_at   TIMESTAMPTZ NOT NULL,
    last_fired_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS rimsky_schedules_next_fire_at_idx ON rimsky_schedules (next_fire_at);
