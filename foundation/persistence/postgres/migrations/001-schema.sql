-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Rimsky consolidated schema baseline.
-- Created 2026-05-24 by spec .ok-planner/specs/2026-05-24-instance-debugger-design.md.
-- Replaces the prior 14-migration sequence (001-baseline through 014-drop-last-outcome).
-- Pre-v1 break-freely operation per .claude/rules/rules.md — operators with existing
-- dev databases drop and recreate; this is NOT an upgrade path.
--
-- Note: rimsky_migrations is created by the driver's Bootstrap step
-- (see foundation/persistence/{postgres,sqlite}/migrate.go); it is
-- intentionally NOT declared here so a re-run that skips this file
-- (idempotency via the rimsky_migrations row) and a fresh run both
-- find the table in the same state.

-- =====  rimsky_templates  =====
-- Content-addressed deploy targets (sha256-<hex> id).
CREATE TABLE rimsky_templates (
    id              TEXT        PRIMARY KEY,
    spec            JSONB       NOT NULL,
    state           TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    source          TEXT        NOT NULL DEFAULT 'direct'
);

CREATE TABLE rimsky_template_tags (
    tag             TEXT        PRIMARY KEY,
    template_id     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

-- =====  rimsky_instances  =====
-- Graph instances (one per consumer registration).
--
-- attribute_overrides (post-migration-005 rename of userdata_overrides) carries
-- optional per-instance JSON overrides that rimsky deep-merges into per-node
-- attributes at dispatch time. attribute_overrides_match_counts (post-migration-006)
-- is the per-entry counter for the by_match overlay surface.
-- main_run_scope_id (post-migration-010) is the instance's main RunScope
-- (mutual FK with rimsky_run_scopes.instance_id; DEFERRABLE INITIALLY DEFERRED
-- so both rows can insert in a single tx).
-- paused (new in this consolidation, per concept:breakpoint) is the soft-pause
-- flag toggled by the debugger control-API surface. Default false; runtime
-- skips dispatch when true.
-- rimsky_instances.main_run_scope_id is added below, after rimsky_run_scopes
-- exists. The mutual FK between the two tables is DEFERRABLE INITIALLY
-- DEFERRED so a single tx can INSERT both the instance row and its main
-- RunScope row.
CREATE TABLE rimsky_instances (
    id                                UUID        PRIMARY KEY,
    template_hash                     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key                      TEXT,
    params                            JSONB       NOT NULL DEFAULT '{}',
    attribute_overrides               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    attribute_overrides_match_counts  JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at                        TIMESTAMPTZ NOT NULL DEFAULT now(),
    terminated_at                     TIMESTAMPTZ,
    frame_delivery_mode               TEXT        NOT NULL DEFAULT 'coalesce'
                                      CHECK (frame_delivery_mode IN ('serial_queue','coalesce')),
    paused                            BOOLEAN     NOT NULL DEFAULT FALSE,
    UNIQUE (template_hash, instance_key)
);

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- =====  rimsky_frames  =====
-- Frame-resolution semantics; one run of the cascade.
CREATE TABLE rimsky_frames (
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
-- Identity + scheduling metadata only. State-machine columns and
-- schedule_cron have all been lifted off — state lives entirely on
-- rimsky_node_runs; cron firing is owned by sensors/sensor-cron/.
-- tags (post-migration-002) is the operator-facing tag array with a GIN index.
CREATE TABLE rimsky_nodes (
    id                      UUID PRIMARY KEY,
    instance_id             UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    current_error_class     TEXT,
    retry_counter           INT  NOT NULL DEFAULT 0,
    action_index            INT  NOT NULL DEFAULT 0,
    frame_id                UUID REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    tags                    TEXT[] NOT NULL DEFAULT '{}',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);
CREATE INDEX rimsky_nodes_tags_idx ON rimsky_nodes USING GIN (tags);

-- =====  rimsky_supervisors  =====
CREATE TABLE rimsky_supervisors (
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
CREATE INDEX rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- =====  rimsky_run_scopes  =====
-- First-class execution context per concept:run-scope. Hosts the set
-- of rimsky_node_runs rows for one graph instantiation (main /
-- subgraph / fanout_partition). Tree shape via parent_run_scope_id.
-- Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
--
-- This table is declared BEFORE rimsky_instances would otherwise require it,
-- but the mutual FK between rimsky_instances.main_run_scope_id and
-- rimsky_run_scopes.instance_id is DEFERRABLE INITIALLY DEFERRED, so the
-- consolidated schema can declare them in either order; rimsky_run_scopes
-- depends only on rimsky_node_runs (via parent_run_id) and rimsky_instances
-- (via instance_id). Both forward references are deferred at insert time.
CREATE TABLE rimsky_run_scopes (
    id                  UUID PRIMARY KEY,
    -- ON DELETE CASCADE on both parent_* FKs so dropping the parent
    -- RunScope (or its parent node_run row) walks the run-scope tree
    -- automatically.
    parent_run_scope_id UUID NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    parent_run_id       UUID NULL,
    graph_name          TEXT NOT NULL,
    partition_key       TEXT NOT NULL DEFAULT '',
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_instances.main_run_scope_id can be created in a single tx.
    -- ON DELETE CASCADE so deleting the instance walks the run-scope tree.
    instance_id         UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at           TIMESTAMPTZ NULL,

    CONSTRAINT run_scope_main_has_no_parents CHECK (
      (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
      OR
      (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
    )
);

-- At most one open fan-out partition RunScope per (parent_run_id, partition_key).
CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
    ON rimsky_run_scopes (parent_run_id, partition_key)
    WHERE partition_key != '' AND closed_at IS NULL;

-- Tree-walk index: parent_chain navigation for depth-gating + aggregation.
CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);

-- Mutual FK to rimsky_run_scopes (deferred — see comment on rimsky_instances).
ALTER TABLE rimsky_instances
    ADD COLUMN main_run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id) DEFERRABLE INITIALLY DEFERRED;

-- =====  rimsky_node_runs  =====
-- Per-run bookkeeping row. Owns the active+held+parked lifecycle (phase)
-- and the state column that used to live on rimsky_nodes. Migration 014
-- dropped the last_outcome column; settling_signal_type (migration 013)
-- is the strictly-more-expressive replacement.
--
-- Migration 008 replaced inline (parent_run_id, child_key) with non-null
-- run_scope_id FK to rimsky_run_scopes; the in-flight uniqueness invariant
-- is now keyed on (node_id, run_scope_id).
--
-- Migration 011 collapsed parked_reason to the closed two-value set
-- {await_callback, snooze}; migration 012 added prior_dispatch_id /
-- prior_dispatch_disposition for recovery-aware executor protocol.
CREATE TABLE rimsky_node_runs (
    id                                  UUID PRIMARY KEY,
    node_id                             UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                       TEXT,
    required_stores                     TEXT[] NOT NULL DEFAULT '{}',
    enqueued_at                         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_by                          TEXT,
    claimed_at                          TIMESTAMPTZ,
    last_heartbeat_at                   TIMESTAMPTZ,
    phase                               TEXT NOT NULL DEFAULT 'pending'
                                        CHECK (phase IN ('pending','active','held','parked','completed','failed')),
    active_terminal_at                  TIMESTAMPTZ,
    frame_id                            UUID NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                           TIMESTAMPTZ,
    resume_at                           TIMESTAMPTZ,
    parked_payload_inline               BYTEA,
    parked_payload_handle               TEXT,
    parked_payload_handle_backend       TEXT,
    session_token                       TEXT,
    parked_reason                       TEXT,
    parked_reason_label                 TEXT,
    parked_reason_note                  TEXT,
    parked_resume_at                    TIMESTAMPTZ,
    wake_reason                         TEXT,
    consecutive_retries_no_progress     INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds           INTEGER,
    max_retries_without_progress        INTEGER,
    aggregation_policy                  JSONB,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    -- ON DELETE CASCADE so dropping a RunScope (which cascades from the
    -- instance row) walks the dispatch rows it owns automatically.
    run_scope_id                        UUID NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    prior_dispatch_id                   UUID NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    prior_dispatch_disposition          TEXT NULL
                                        CHECK (prior_dispatch_disposition IS NULL
                                               OR prior_dispatch_disposition IN ('heartbeat_stale', 'retry_after_error', 'recalculate')),
    -- settling_signal_type carries the canonical signal type-path
    -- (concept:signal) of the run's settling resolution. NULL while
    -- the run is in-flight. Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling.
    settling_signal_type                TEXT,
    CONSTRAINT rimsky_node_runs_parked_reason_check
        CHECK (parked_reason IS NULL OR parked_reason IN ('await_callback', 'snooze'))
);

-- Add the rimsky_run_scopes.parent_run_id FK now that rimsky_node_runs exists.
ALTER TABLE rimsky_run_scopes
    ADD CONSTRAINT rimsky_run_scopes_parent_run_id_fkey
    FOREIGN KEY (parent_run_id) REFERENCES rimsky_node_runs(id) ON DELETE CASCADE;

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
CREATE INDEX idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

-- =====  rimsky_wait_set  =====
-- Per-frame wait-set ledger gating dispatch under the subscription-
-- cascade model. Keyed on receiver_run_id / sender_run_id (per-run
-- identity, not per-node). drained_at (migration 004) marks drained
-- rows instead of deleting them; eligibility predicate is
-- "no rows with drained_at IS NULL."
CREATE TABLE rimsky_wait_set (
    frame_id            UUID        NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT        NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT        NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        JSONB,
    inserted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    drained_at          TIMESTAMPTZ,
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);
CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);

-- =====  rimsky_events  =====
-- Single append-only event log; JSONB payload.
CREATE TABLE rimsky_events (
    id          BIGSERIAL PRIMARY KEY,
    instance_id UUID REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     UUID REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX rimsky_events_node_id_occurred_at_idx ON rimsky_events (node_id, occurred_at DESC);
CREATE INDEX rimsky_events_instance_id_occurred_at_idx ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX rimsky_events_kind_occurred_at_idx ON rimsky_events (kind, occurred_at DESC);

-- =====  rimsky_api_keys  =====
-- API keys for Bearer-token auth. Hashed at rest (SHA-256); plaintext
-- is surfaced once at mint (and once per rotation). The partial
-- unique-name index excludes revoked + rotation-grace rows.
CREATE TABLE rimsky_api_keys (
    id                 UUID         NOT NULL PRIMARY KEY,
    key_hash           BYTEA        NOT NULL,
    name               TEXT         NOT NULL,
    permissions        JSONB        NOT NULL,
    created_at         TIMESTAMPTZ  NOT NULL,
    created_by_key_id  UUID         NULL,
    last_used_at       TIMESTAMPTZ  NULL,
    expires_at         TIMESTAMPTZ  NULL,
    revoke_at          TIMESTAMPTZ  NULL,
    revoked_at         TIMESTAMPTZ  NULL,
    CONSTRAINT rimsky_api_keys_key_hash_unique UNIQUE (key_hash),
    CONSTRAINT rimsky_api_keys_created_by_fk
        FOREIGN KEY (created_by_key_id) REFERENCES rimsky_api_keys(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX rimsky_api_keys_active_name_idx
    ON rimsky_api_keys (name)
    WHERE revoked_at IS NULL AND revoke_at IS NULL;

CREATE INDEX rimsky_api_keys_revoke_at_pending_idx
    ON rimsky_api_keys (revoke_at)
    WHERE revoke_at IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX rimsky_api_keys_active_status_idx
    ON rimsky_api_keys (revoked_at, expires_at, revoke_at);

-- =====  rimsky_node_attributes  =====
-- Per-run attribute snapshot (migration 003 re-keyed from per-node to
-- per-run). node_id is denormalized for forensic queries.
CREATE TABLE rimsky_node_attributes (
    node_run_id          UUID PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);

-- =====  rimsky_claim_handles  =====
-- Named + claim_scope handles (post-migration-009 rename of 'scope' → 'claim_scope'
-- across the lock_kind enum, scope_data → claim_scope_data column rename, and
-- index rename).
CREATE TABLE rimsky_claim_handles (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_run_id                 UUID REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL,
    lock_name                   TEXT,
    producer_name               TEXT,
    claim_scope_data            JSONB,
    address                     JSONB,
    intent                      TEXT,
    realized_write_semantics    TEXT,
    is_held                     BOOLEAN NOT NULL DEFAULT FALSE,
    holder_supervisor_id        TEXT,
    holder_node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                  TIMESTAMPTZ NOT NULL,
    frame_id                    UUID,
    parent_claim_handle_id      UUID REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL,
    lifetime                    TEXT NOT NULL DEFAULT 'subgraph'
                                CHECK (lifetime IN ('subgraph','durable')),
    version_id                  TEXT,
    producer_candidate_handle   BYTEA,
    aggregation_policy          JSONB,
    expected_children_count     INTEGER NOT NULL DEFAULT 0,
    committed_children_count    INTEGER NOT NULL DEFAULT 0,
    abandoned_children_count    INTEGER NOT NULL DEFAULT 0,
    state                       TEXT NOT NULL DEFAULT 'active'
                                CHECK (state IN ('active','committed','abandoned')),
    resolved_at                 TIMESTAMPTZ,
    CONSTRAINT rimsky_claim_handles_lock_kind_check
        CHECK (lock_kind IN ('named', 'claim_scope')),
    CONSTRAINT claim_handle_kind_fields CHECK (
        (lock_kind = 'named'       AND lock_name IS NOT NULL AND producer_name IS NULL     AND claim_scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'claim_scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND claim_scope_data IS NOT NULL AND intent IN ('r', 'rw'))
    ),
    CONSTRAINT rimsky_claim_handles_active_has_holder
        CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL),
    CONSTRAINT rimsky_claim_handles_inactive_has_no_holder
        CHECK (state = 'active' OR holder_supervisor_id IS NULL)
);
CREATE INDEX idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)     WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_claim_handles_claim_scope  ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'claim_scope';
CREATE INDEX idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = TRUE;
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
    id               UUID PRIMARY KEY,
    claim_handle_id  UUID NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_run_id    UUID NOT NULL REFERENCES rimsky_node_runs(id)     ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at     TIMESTAMPTZ,
    frame_id         UUID,
    UNIQUE (claim_handle_id, holder_run_id)
);
CREATE INDEX idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX idx_rimsky_claim_holders_run          ON rimsky_claim_holders (holder_run_id);
CREATE INDEX idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- =====  rimsky_lifecycle_idempotencies  =====
-- One row per (producer, scope_kind, scope_id) for LifecycleSubscriber
-- event idempotency.
CREATE TABLE rimsky_lifecycle_idempotencies (
    store_registration_name TEXT        NOT NULL,
    scope_kind              TEXT        NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT        NOT NULL,
    state                   TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);
CREATE INDEX idx_rimsky_lifecycle_idempotencies_scope
    ON rimsky_lifecycle_idempotencies (scope_kind, scope_id);

-- =====  rimsky_blob_orphans  =====
-- Orphan-blob tracking. When an attribute or event row's value_handle is
-- overwritten or deleted, the old handle is recorded here for the
-- SweepOrphanedBlobs sweep to clean up via BlobBackend.Delete.
CREATE TABLE rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reap_after  TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- =====  rimsky_node_events  =====
-- Named-event ledger (executor-emitted NamedEvent emissions for
-- substitution via nodes.<emitter_node>.event.<event_name>.<json_path>).
-- Append-only.
CREATE TABLE rimsky_node_events (
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
CREATE INDEX idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);

-- =====  rimsky_messages  =====
-- Boundary-crossing message envelopes. V1 kind: 'invalidate'. Delivered
-- at frame boundary per the per-instance frame_delivery_mode
-- ('coalesce' default; 'serial_queue' opt-in).
CREATE TABLE rimsky_messages (
    id                     UUID PRIMARY KEY,
    instance_id            UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    kind                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','publisher','instance')),
    target                 TEXT,
    payload                BYTEA,
    backfill_operation_id  UUID,
    received_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at           TIMESTAMPTZ,
    frame_id               UUID,
    cancelled              BOOLEAN NOT NULL DEFAULT FALSE
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
-- Append-only lineage projection.
CREATE TABLE rimsky_lineage (
    id           UUID PRIMARY KEY,
    record_kind  TEXT NOT NULL
                 CONSTRAINT rimsky_lineage_record_kind_check
                 CHECK (record_kind IN ('leaf_run','claim_terminal')),
    instance_id  UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     UUID NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    record       JSONB NOT NULL,
    outcome      TEXT NOT NULL
                 CHECK (outcome IN ('','committed','abandoned','force_cancelled'))
);
CREATE INDEX idx_lineage_run
    ON rimsky_lineage(record_kind, (record->>'run_id'));
CREATE INDEX idx_lineage_claim
    ON rimsky_lineage(record_kind, (record->>'claim_handle_id'));
CREATE INDEX idx_lineage_substitution_refs
    ON rimsky_lineage USING GIN (record);

-- =====  rimsky_publisher_subscriptions  =====
-- Rimsky-side binding state per (publisher_name, publisher_subscription_id).
CREATE TABLE rimsky_publisher_subscriptions (
    id                UUID NOT NULL,
    instance_id       UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   JSONB NOT NULL,
    target_node       TEXT NOT NULL,
    message_kind      TEXT NOT NULL DEFAULT 'invalidate',
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
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
CREATE TABLE rimsky_message_idempotencies (
    instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sender           TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    message_id       UUID NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, sender, idempotency_key)
);
CREATE INDEX idx_message_idempotencies_created_at
    ON rimsky_message_idempotencies(created_at);

-- =====  rimsky_instance_breakpoints  =====
-- Runtime-installed pause/notify breakpoints per concept:breakpoint
-- (introduced by spec .ok-planner/specs/2026-05-24-instance-debugger-design.md).
-- One row per active breakpoint; matched at supervisor checkpoints
-- (before_dispatch / after_terminal) against the dispatch context.
CREATE TABLE rimsky_instance_breakpoints (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    matcher          JSONB NOT NULL,
    checkpoint       TEXT NOT NULL
                     CHECK (checkpoint IN ('before_dispatch','after_terminal')),
    signal_type      TEXT,
    mode             TEXT NOT NULL DEFAULT 'pause'
                     CHECK (mode IN ('pause','notify_only')),
    overflow_policy  TEXT NOT NULL
                     CHECK (overflow_policy IN ('drop_oldest','block_dispatch','auto_resume_after_ttl')),
    hit_ttl_seconds  INT NOT NULL DEFAULT 300,
    ttl_seconds      INT,
    dropped_count    BIGINT NOT NULL DEFAULT 0,
    created_by_key   TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ,
    -- signal_type is only meaningful when checkpoint='after_terminal'
    -- (the type-path prefix filter matches against the post-terminal
    -- signal). The HTTP create handler enforces this with a 400; the
    -- CHECK is defense-in-depth so a future code path bypassing the
    -- handler (e.g. test fixture, migration, ad-hoc INSERT) can't
    -- land a row that the matcher would silently skip.
    CHECK (signal_type IS NULL OR checkpoint = 'after_terminal')
);

-- Partial index predicate is column-only (Postgres requires IMMUTABLE
-- functions in index predicates; NOW() is STABLE). Active filtering is
-- the combination of (expires_at IS NULL) ∪ (expires_at > now() at read
-- time) — the latter is applied in the query WHERE, the former is what
-- the partial index narrows on. The expires_at index below catches the
-- time-based path for the sweeper.
CREATE INDEX idx_breakpoints_instance_active
    ON rimsky_instance_breakpoints (instance_id)
    WHERE expires_at IS NULL;

CREATE INDEX idx_breakpoints_expires
    ON rimsky_instance_breakpoints (expires_at)
    WHERE expires_at IS NOT NULL;

-- =====  rimsky_breakpoint_hits  =====
-- Append-only ledger of breakpoint matches. seq is the monotonic cursor
-- consumed by MCP resources/read polling.
CREATE TABLE rimsky_breakpoint_hits (
    seq             BIGSERIAL PRIMARY KEY,
    id              UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    breakpoint_id   UUID NOT NULL REFERENCES rimsky_instance_breakpoints(id) ON DELETE CASCADE,
    instance_id     UUID NOT NULL REFERENCES rimsky_instances(id),
    node_run_id     UUID,
    frame_id        UUID,
    checkpoint      TEXT NOT NULL,
    mode            TEXT NOT NULL,
    snapshot        JSONB NOT NULL,
    hit_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resumed_at      TIMESTAMPTZ,
    resumed_by_key  TEXT,
    resume_overlay  JSONB
);

CREATE INDEX idx_bp_hits_breakpoint_unresumed
    ON rimsky_breakpoint_hits (breakpoint_id, hit_at)
    WHERE resumed_at IS NULL;

CREATE INDEX idx_bp_hits_instance_seq
    ON rimsky_breakpoint_hits (instance_id, seq);

CREATE INDEX idx_bp_hits_breakpoint_seq
    ON rimsky_breakpoint_hits (breakpoint_id, seq);
