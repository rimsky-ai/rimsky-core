-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
--
-- Pre-v1 flattened baseline. Expresses the final desired schema as of
-- 2026-05-17 — the cumulative effect of every prior migration collapsed
-- into a single file. The migration runner applies this against an empty
-- Postgres database; dev DBs must be wiped (`DROP SCHEMA public CASCADE;
-- CREATE SCHEMA public;`) before re-applying.
--
-- The schema covers the post-data-platform-extensions shape (run-tree
-- on rimsky_node_runs; state lifted off rimsky_nodes; claim_holders /
-- wait_set keyed on runs; rimsky_schedules retired; rimsky_messages /
-- rimsky_lineage / rimsky_publisher_subscriptions present; rimsky_lineage carrying
-- the outcome column with record_kind ∈ {leaf_run, claim_terminal}) and
-- the post-cleanup state-column refactor on rimsky_claim_handles
-- (binary held_durable replaced by 3-state {active, committed, abandoned}
-- plus resolved_at; holder_supervisor_id nullable, gated by CHECKs).

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
CREATE TABLE rimsky_instances (
    id                  UUID        PRIMARY KEY,
    template_hash       TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key        TEXT,
    params              JSONB       NOT NULL DEFAULT '{}',
    userdata_overrides  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    terminated_at       TIMESTAMPTZ,
    frame_delivery_mode TEXT        NOT NULL DEFAULT 'coalesce'
                        CHECK (frame_delivery_mode IN ('serial_queue','coalesce')),
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
-- Identity + scheduling metadata only. State-machine columns (state,
-- last_outcome, last_heartbeat_at, assigned_supervisor_id) and the
-- retired schedule_cron column have all been lifted off — state lives
-- entirely on rimsky_node_runs; cron firing is owned by sensors/sensor-cron/.
CREATE TABLE rimsky_nodes (
    id                      UUID PRIMARY KEY,
    instance_id             UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    -- Cascade-coupling is declared receiver-side via `subscribes:` in the
    -- template; the per-template subscription-edge inverse map (see
    -- graph/node/subscription_edges.go) drives cascade walks.
    current_error_class     TEXT,
    retry_counter           INT NOT NULL DEFAULT 0,
    action_index            INT NOT NULL DEFAULT 0,
    frame_id                UUID REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);

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

-- =====  rimsky_node_runs  =====
-- Per-run bookkeeping row. Owns the active+held+parked lifecycle (phase)
-- and the state-machine columns (state / last_outcome) that used to live
-- on rimsky_nodes. The orphan reaper covers phase='active' rows with
-- stale heartbeat. Held claim handles persist past the active terminal
-- until auto-terminal resolution fires.
--
-- Run-tree extension (parent_run_id, child_key, aggregation_policy)
-- supports fan-out + sub-graph dispatch: a parent run waits on a
-- partition of child runs each keyed by child_key. Root runs carry
-- parent_run_id IS NULL and child_key IS NULL; child rows MUST carry a
-- non-NULL child_key (enforced by rimsky_node_runs_child_key_check)
-- because Postgres treats two rows with both NULL as distinct under the
-- multi-column partial unique index, and a future writer that forgot to
-- set child_key would silently bypass the "one in-flight run per
-- (parent, child_key) partition" guarantee.
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
    parent_run_id                       UUID REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    child_key                           TEXT,
    aggregation_policy                  JSONB,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    last_outcome                        TEXT NOT NULL DEFAULT 'fresh_unchanged'
                                        CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed')),
    CONSTRAINT rimsky_node_runs_child_key_check
        CHECK (parent_run_id IS NULL OR child_key IS NOT NULL)
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

-- "At most one in-flight run" invariant split by parent_run_id IS NULL /
-- NOT NULL so root runs keep the original (node_id) guarantee while
-- fan-out / sub-graph children coexist under their parent (all share the
-- parent node id; they're disambiguated by child_key).
CREATE UNIQUE INDEX uq_node_runs_in_flight_per_root_node
    ON rimsky_node_runs (node_id)
    WHERE parent_run_id IS NULL
      AND phase IN ('pending','active','held','parked');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_child
    ON rimsky_node_runs (parent_run_id, child_key)
    WHERE parent_run_id IS NOT NULL
      AND phase IN ('pending','active','held','parked');

-- =====  rimsky_wait_set  =====
-- Per-frame wait-set ledger gating dispatch under the subscription-
-- cascade model. Keyed on receiver_run_id / sender_run_id (per-run
-- identity, not per-node) so two in-flight runs of the same node-type
-- don't conflate their wait-sets.
--
-- Cascade walks insert rows when a sender transitions out of a settled
-- state (the "pessimistic invalidate"); the drain rule deletes rows
-- where sender_run_id = S in bulk when the sender reaches any settled
-- state (fresh / failed / parked). Eligibility predicate: a stale run
-- is dispatch-eligible iff no wait-set rows exist for it in the current
-- frame.
--
-- subscription_scope distinguishes per-node ('direct') from
-- cross-cutting ('instance') subscriptions so a receiver subscribed to
-- a sender via BOTH a direct and a cross-cutting subscription gets two
-- distinct rows that both must drain.
CREATE TABLE rimsky_wait_set (
    frame_id            UUID        NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT        NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT        NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        JSONB,
    inserted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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

-- =====  rimsky_node_attributes  =====
-- Per-node attribute snapshot (with optional blob spill).
CREATE TABLE rimsky_node_attributes (
    node_id              UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt          INT NOT NULL DEFAULT 0,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====  rimsky_claim_handles  =====
-- Named + scope-kind claim handles. FK-cascade child of rimsky_node_runs
-- (ON DELETE SET NULL so held claim handles outlive their parent's
-- active terminal until auto-terminal explicitly removes them via
-- ResolveClaimHandleTerminal).
--
-- The state column (active | committed | abandoned) replaces the prior
-- binary held_durable model. Active rows MUST have a holder; non-active
-- rows MUST NOT (enforced by the two named CHECKs below). resolved_at
-- is set when a row transitions out of 'active'.
--
-- Recursive scope-partitioning (sub-claim chains) hangs off
-- parent_claim_handle_id; the parent carries aggregation_policy +
-- expected/committed/abandoned children counters that the recursive
-- terminal walker reads inside SELECT … FOR UPDATE to decide
-- Commit vs Abandon per the policy.
CREATE TABLE rimsky_claim_handles (
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
    CONSTRAINT claim_handle_kind_fields CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND producer_name IS NULL     AND scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND scope_data IS NOT NULL AND intent IN ('r', 'rw'))
    ),
    CONSTRAINT rimsky_claim_handles_active_has_holder
        CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL),
    CONSTRAINT rimsky_claim_handles_inactive_has_no_holder
        CHECK (state = 'active' OR holder_supervisor_id IS NULL)
);
CREATE INDEX idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_claim_handles_scope        ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'scope';
CREATE INDEX idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = TRUE;
CREATE INDEX idx_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;
-- State-based partial indexes: the active index supports the orphan
-- reaper + heartbeat-extend predicates; the committed-durable index
-- supports the asset query (ListByInstanceAndState).
CREATE INDEX rimsky_claim_handles_active_idx
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';
CREATE INDEX rimsky_claim_handles_committed_durable_idx
    ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';

-- =====  rimsky_claim_holders  =====
-- Held-claim subgraph state ledger. Keyed on holder_run_id (per-run
-- identity, not per-node) so multi-frame instances and run-tree
-- retention disambiguate naturally.
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
-- ('coalesce' default; 'serial_queue' opt-in). The `cancelled` flag
-- marks pending undelivered messages cancelled by a backfill-cancellation
-- flow; in-flight frames complete normally.
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
-- Append-only lineage projection. Two record_kinds:
--   * 'leaf_run'        — per leaf-run terminal (outcome stays empty).
--   * 'claim_terminal'  — per ClaimProducer.{Commit, Abandon} resolution
--                         (outcome ∈ {committed, abandoned, force_cancelled}).
-- Source of truth is rimsky_events + rimsky_claim_handles; this is a
-- materialized projection rebuildable from them.
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
-- A publisher_subscription is one publisher peer's commitment to publish
-- messages for one instance. Publisher-side state can be reconstructed
-- via ListSubscriptions resync (rimsky compares its expected set against
-- the publisher's reported set and re-issues Subscribe for any missing).
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
-- The Idempotency-Key HTTP header carries a caller-supplied token; rimsky
-- INSERTs (instance_id, sender, idempotency_key) → message_id, and on
-- conflict returns the original message_id with 200 OK rather than
-- inserting a duplicate envelope. Rows older than the configured TTL are
-- swept by runtime/sweep_message_idempotencies.go.
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
