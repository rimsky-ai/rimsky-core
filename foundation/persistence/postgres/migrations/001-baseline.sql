-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Baseline migration. Reflects post-2026-05-12 nomenclature resolution.
-- Pre-v1; no rolling-deploy compat. Dev DBs require
-- DROP SCHEMA public CASCADE; CREATE SCHEMA public; before applying.
--
-- Schema names reflect:
--   * Plural tables throughout (rimsky_claim_handles, rimsky_lifecycle_idempotencies).
--   * rimsky_node_runs replaces rimsky_worker_request (cross-layer #14).
--   * rimsky_frames.frame_resolution_mode replaces rimsky_frames.mode (cross-layer #4).
--   * rimsky_claim_handles.node_run_id FK replaces worker_request_id.
--   * rimsky_instances.instance_key (instance_key rename history erased).
--
-- All column-level renames are absorbed; no ALTER history remains.

CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Templates: content-addressed deploy targets (sha256-<hex> id).
CREATE TABLE IF NOT EXISTS rimsky_templates (
    id              TEXT        PRIMARY KEY,
    spec            JSONB       NOT NULL,
    state           TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    source          TEXT        NOT NULL DEFAULT 'direct'
);

CREATE TABLE IF NOT EXISTS rimsky_template_tags (
    tag             TEXT        PRIMARY KEY,
    template_id     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

-- Graph instances (one per consumer registration).
CREATE TABLE IF NOT EXISTS rimsky_instances (
    id                  UUID        PRIMARY KEY,
    template_hash       TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key        TEXT,
    params              JSONB       NOT NULL DEFAULT '{}',
    userdata_overrides  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    terminated_at       TIMESTAMPTZ,
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- Frames (frame-resolution semantics; one run of the cascade).
CREATE TABLE IF NOT EXISTS rimsky_frames (
    frame_id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id             UUID         NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_resolution_mode   TEXT         NOT NULL CHECK (frame_resolution_mode IN ('coalesce','serial_queue')),
    state                   TEXT         NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids         UUID[]       NOT NULL CHECK (array_length(source_node_ids, 1) >= 1),
    queued_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at              TIMESTAMPTZ,
    ended_at                TIMESTAMPTZ,
    frame_timeout_ms        BIGINT       NOT NULL CHECK (frame_timeout_ms >= 60000),
    last_progress_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT chk_running_has_started CHECK (state != 'running' OR started_at IS NOT NULL),
    CONSTRAINT chk_terminal_has_ended  CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
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

-- Node instances (one per node declared in a template, per graph instance).
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                      UUID PRIMARY KEY,
    instance_id             UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    schedule_cron           TEXT,
    state                   TEXT NOT NULL,                  -- fresh | stale | running | failed | parked
    dependencies            UUID[] NOT NULL,
    current_error_class     TEXT,
    retry_counter           INT NOT NULL DEFAULT 0,
    action_index            INT NOT NULL DEFAULT 0,
    last_heartbeat_at       TIMESTAMPTZ,
    assigned_supervisor_id  TEXT,
    last_outcome            TEXT,
    frame_id                UUID REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_nodes_state_updated_at_idx ON rimsky_nodes (state, updated_at);
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);
CREATE INDEX IF NOT EXISTS idx_rimsky_nodes_frame_state
    ON rimsky_nodes (frame_id, state)
    WHERE state IN ('stale','running');

-- Supervisor registry.
CREATE TABLE IF NOT EXISTS rimsky_supervisors (
    id                  TEXT PRIMARY KEY,
    accepted_executors  TEXT[] NOT NULL,
    accepted_stores     TEXT[] NOT NULL DEFAULT '{}',
    concurrency         INT NOT NULL,
    callback_host       TEXT,
    callback_port       INT,
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_node_count   INT NOT NULL DEFAULT 0,
    registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- Node-run queue (one execution of one node within a frame; parent of claim handles).
-- phase column drives the active+held+parked lifecycle; claimed_by carries the
-- supervisor id while phase='active'. The orphan reaper covers phase='active'
-- rows with stale heartbeat. Held claim handles persist past the active
-- terminal until auto-terminal resolution fires.
CREATE TABLE IF NOT EXISTS rimsky_node_runs (
    id                                  UUID PRIMARY KEY,
    node_id                             UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT[] NOT NULL DEFAULT '{}',
    enqueued_at                         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_by                          TEXT,
    claimed_at                          TIMESTAMPTZ,
    last_heartbeat_at                   TIMESTAMPTZ,
    phase                               TEXT NOT NULL DEFAULT 'pending'
                                        CHECK (phase IN ('pending','active','held','parked','completed')),
    active_terminal_at                  TIMESTAMPTZ,
    frame_id                            UUID NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TIMESTAMPTZ,
    resume_at                           TIMESTAMPTZ,
    parked_payload_inline               BYTEA,
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

-- Schedule registry (one row per scheduled node).
CREATE TABLE IF NOT EXISTS rimsky_schedules (
    node_id        UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    cron_expr      TEXT NOT NULL,
    next_fire_at   TIMESTAMPTZ NOT NULL,
    last_fired_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS rimsky_schedules_next_fire_at_idx ON rimsky_schedules (next_fire_at);

-- Event log (single append-only; JSONB payload).
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

-- Per-node attribute snapshot (with optional blob spill).
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id              UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt          INT NOT NULL DEFAULT 0,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Claim handles (named + scope kinds). FK-cascade child of rimsky_node_runs
-- (ON DELETE SET NULL so held claim handles outlive their parent's active
-- terminal until auto-terminal explicitly removes them via
-- ResolveClaimHandleTerminal).
CREATE TABLE IF NOT EXISTS rimsky_claim_handles (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_run_id                 UUID REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL CHECK (lock_kind IN ('named', 'scope')),
    lock_name                   TEXT,
    producer_name               TEXT,
    scope_data                  JSONB,
    address                     JSONB,
    intent                      TEXT,
    realized_write_semantics    TEXT,
    is_held                     BOOLEAN NOT NULL DEFAULT FALSE,
    holder_supervisor_id        TEXT NOT NULL,
    holder_node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                  TIMESTAMPTZ NOT NULL,
    frame_id                    UUID,
    CONSTRAINT claim_handle_kind_fields CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND producer_name IS NULL     AND scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND scope_data IS NOT NULL AND intent IN ('r', 'rw'))
    )
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_scope        ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'scope';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = TRUE;

-- Held-claim subgraph state ledger.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id               UUID PRIMARY KEY,
    claim_handle_id  UUID NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_node_id   UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at     TIMESTAMPTZ,
    frame_id         UUID,
    UNIQUE (claim_handle_id, holder_node_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_node         ON rimsky_claim_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- Lifecycle idempotency ledger (one row per (producer, scope_kind, scope_id)).
CREATE TABLE IF NOT EXISTS rimsky_lifecycle_idempotencies (
    store_registration_name TEXT        NOT NULL,
    scope_kind              TEXT        NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT        NOT NULL,
    state                   TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_lifecycle_idempotencies_scope
    ON rimsky_lifecycle_idempotencies (scope_kind, scope_id);

-- Orphan-blob tracking. When an attribute or event row's value_handle is
-- overwritten or deleted, the old handle is recorded here for the
-- SweepOrphanedBlobs sweep to clean up via BlobBackend.Delete.
CREATE TABLE IF NOT EXISTS rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reap_after  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- Named-event ledger (executor-emitted NamedEvent emissions for
-- substitution via nodes.<emitter_node>.event.<event_name>.<json_path>).
-- Append-only.
CREATE TABLE IF NOT EXISTS rimsky_node_events (
    id                     BIGSERIAL PRIMARY KEY,
    instance_id            UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    emitter_node_id        TEXT NOT NULL,
    event_name             TEXT NOT NULL,
    payload_inline         BYTEA,
    payload_handle         TEXT,
    payload_handle_backend TEXT,
    emitted_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    frame_id               UUID
);
CREATE INDEX IF NOT EXISTS idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);
