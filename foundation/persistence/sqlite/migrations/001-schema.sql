-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Rimsky consolidated schema baseline (SQLite).
-- Created 2026-05-24 by spec .ok-planner/specs/2026-05-24-instance-debugger-design.md.
-- Replaces the prior 14-migration sequence (001-baseline through 014-drop-last-outcome).
-- Pre-v1 break-freely operation per .claude/rules/rules.md — operators with existing
-- dev databases drop and recreate; this is NOT an upgrade path.
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
--
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
-- main_run_scope_id is added by ALTER below, after rimsky_run_scopes exists.
-- attribute_overrides (rename of userdata_overrides per migration 005);
-- attribute_overrides_match_counts (per migration 006); paused (new in this
-- consolidation, per concept:breakpoint).
CREATE TABLE rimsky_instances (
    id                                TEXT PRIMARY KEY,
    template_hash                     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key                      TEXT,
    params                            TEXT NOT NULL DEFAULT '{}',
    attribute_overrides               TEXT NOT NULL DEFAULT '{}',
    attribute_overrides_match_counts  TEXT NOT NULL DEFAULT '[]',
    created_at                        TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at                     TEXT,
    frame_delivery_mode               TEXT NOT NULL DEFAULT 'coalesce'
                                      CHECK (frame_delivery_mode IN ('serial_queue','coalesce')),
    paused                            INTEGER NOT NULL DEFAULT 0,
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
-- tags (post-migration-002) is a JSON-encoded array stored as TEXT.
CREATE TABLE rimsky_nodes (
    id                      TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type               TEXT NOT NULL,
    executor                TEXT,
    current_error_class     TEXT,
    retry_counter           INTEGER NOT NULL DEFAULT 0,
    action_index            INTEGER NOT NULL DEFAULT 0,
    frame_id                TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    tags                    TEXT NOT NULL DEFAULT '[]',
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

-- =====  rimsky_run_scopes  =====
-- parent_run_id (FK → rimsky_node_runs) is added by ALTER below, after
-- rimsky_node_runs exists. The mutual FK between rimsky_instances and
-- rimsky_run_scopes (DEFERRABLE INITIALLY DEFERRED) is resolved by the
-- ALTER below that adds rimsky_instances.main_run_scope_id.
CREATE TABLE rimsky_run_scopes (
    id                  TEXT PRIMARY KEY,
    -- ON DELETE CASCADE on both parent_* FKs (mirror of postgres).
    parent_run_scope_id TEXT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    parent_run_id       TEXT NULL,
    graph_name          TEXT NOT NULL,
    partition_key       TEXT NOT NULL DEFAULT '',
    -- DEFERRABLE INITIALLY DEFERRED so the mutual FK with
    -- rimsky_instances.main_run_scope_id can be created in a single tx.
    -- ON DELETE CASCADE so deleting the instance walks the scope tree.
    instance_id         TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    closed_at           TEXT NULL,
    CHECK (
      (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
      OR
      (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
    ON rimsky_run_scopes (parent_run_id, partition_key)
    WHERE partition_key != '' AND closed_at IS NULL;

CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);

-- Mutual FK to rimsky_run_scopes (deferred — see comment on rimsky_instances).
-- DEFAULT '' satisfies NOT NULL for the (empty) table; the value is replaced
-- at the first INSERT, which is wrapped by the create-instance flow that
-- INSERTs both rows in one tx.
ALTER TABLE rimsky_instances
    ADD COLUMN main_run_scope_id TEXT NOT NULL REFERENCES rimsky_run_scopes(id) DEFERRABLE INITIALLY DEFERRED DEFAULT '';

-- =====  rimsky_node_runs  =====
-- Per-run bookkeeping. Owns the phase + state-machine columns.
-- run_scope_id (migration 008) replaces the prior parent_run_id / child_key.
-- parked_reason values are constrained at the application layer to the
-- closed two-value set {await_callback, snooze} (the baseline + migration 011
-- combined did not add a storage-level CHECK on SQLite — it remains a
-- Postgres-only refinement).
-- prior_dispatch_* (migration 012) and settling_signal_type (migration 013)
-- are post-migration-014 (no last_outcome column).
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
    aggregation_policy                  TEXT,
    state                               TEXT NOT NULL DEFAULT 'stale'
                                        CHECK (state IN ('fresh','stale','running','failed','parked')),
    -- ON DELETE CASCADE so dropping a RunScope walks the dispatch rows it owns.
    run_scope_id                        TEXT NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE,
    prior_dispatch_id                   TEXT NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    prior_dispatch_disposition          TEXT NULL
                                        CHECK (prior_dispatch_disposition IS NULL
                                               OR prior_dispatch_disposition IN ('heartbeat_stale', 'retry_after_error', 'recalculate')),
    settling_signal_type                TEXT
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
CREATE INDEX idx_node_runs_node_frame
    ON rimsky_node_runs(node_id, frame_id);
CREATE INDEX idx_node_runs_state
    ON rimsky_node_runs(state) WHERE state IN ('stale','running');

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);

-- =====  rimsky_wait_set  =====
-- Per-frame wait-set ledger. drained_at (migration 004) marks drained
-- rows instead of deleting them.
CREATE TABLE rimsky_wait_set (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    drained_at          TEXT,
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

-- =====  rimsky_api_keys  =====
CREATE TABLE rimsky_api_keys (
    id                 TEXT     NOT NULL PRIMARY KEY,
    key_hash           BLOB     NOT NULL,
    name               TEXT     NOT NULL,
    permissions        TEXT     NOT NULL,
    created_at         TEXT     NOT NULL,
    created_by_key_id  TEXT     NULL REFERENCES rimsky_api_keys(id) ON DELETE SET NULL,
    last_used_at       TEXT     NULL,
    expires_at         TEXT     NULL,
    revoke_at          TEXT     NULL,
    revoked_at         TEXT     NULL,
    CONSTRAINT rimsky_api_keys_key_hash_unique UNIQUE (key_hash)
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
-- Re-keyed per-run (migration 003) with node_id denormalized.
CREATE TABLE rimsky_node_attributes (
    node_run_id          TEXT PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 TEXT NOT NULL DEFAULT '{}',
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);

-- =====  rimsky_claim_handles  =====
-- Post-migration-009 (rename of 'scope' → 'claim_scope' across lock_kind
-- enum, scope_data → claim_scope_data column, index rename).
CREATE TABLE rimsky_claim_handles (
    id                          TEXT PRIMARY KEY,
    node_run_id                 TEXT REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL CHECK (lock_kind IN ('named','claim_scope')),
    lock_name                   TEXT,
    producer_name               TEXT,
    claim_scope_data            TEXT,
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
        (lock_kind = 'named'       AND lock_name IS NOT NULL AND producer_name IS NULL     AND claim_scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'claim_scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND claim_scope_data IS NOT NULL AND intent IN ('r','rw'))
    ),
    CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL),
    CHECK (state = 'active' OR holder_supervisor_id IS NULL)
);
CREATE INDEX idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_claim_handles_claim_scope  ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'claim_scope';
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

-- =====  rimsky_instance_breakpoints  =====
-- Per concept:breakpoint (spec .ok-planner/specs/2026-05-24-instance-debugger-design.md).
-- SQLite parallel of the postgres baseline. JSONB → TEXT (caller marshals);
-- UUID id has no DB default (app generates).
CREATE TABLE rimsky_instance_breakpoints (
    id               TEXT PRIMARY KEY,
    instance_id      TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    matcher          TEXT NOT NULL,
    checkpoint       TEXT NOT NULL
                     CHECK (checkpoint IN ('before_dispatch','after_terminal')),
    signal_type      TEXT,
    mode             TEXT NOT NULL DEFAULT 'pause'
                     CHECK (mode IN ('pause','notify_only')),
    overflow_policy  TEXT NOT NULL
                     CHECK (overflow_policy IN ('drop_oldest','block_dispatch','auto_resume_after_ttl')),
    hit_ttl_seconds  INTEGER NOT NULL DEFAULT 300,
    ttl_seconds      INTEGER,
    dropped_count    INTEGER NOT NULL DEFAULT 0,
    created_by_key   TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at       TEXT,
    -- Mirror of the postgres CHECK: signal_type is only meaningful when
    -- checkpoint='after_terminal'. Defense-in-depth against code paths
    -- that bypass the HTTP create handler's 400 (test fixture, migration,
    -- ad-hoc INSERT).
    CHECK (signal_type IS NULL OR checkpoint = 'after_terminal')
);

-- Mirror of the postgres partial-index predicate: column-only (postgres
-- requires IMMUTABLE functions in index predicates). Active filtering is
-- the union of (expires_at IS NULL) ∪ (expires_at > now() at read time);
-- the latter is applied in the query WHERE, the former is what this
-- partial index narrows on.
CREATE INDEX idx_breakpoints_instance_active
    ON rimsky_instance_breakpoints (instance_id)
    WHERE expires_at IS NULL;

CREATE INDEX idx_breakpoints_expires
    ON rimsky_instance_breakpoints (expires_at)
    WHERE expires_at IS NOT NULL;

-- =====  rimsky_breakpoint_hits  =====
-- seq is the monotonic cursor consumed by MCP resources/read polling.
-- id is the stable UUID for the resume API (app-generated; no DB default).
CREATE TABLE rimsky_breakpoint_hits (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    id              TEXT NOT NULL UNIQUE,
    breakpoint_id   TEXT NOT NULL REFERENCES rimsky_instance_breakpoints(id) ON DELETE CASCADE,
    instance_id     TEXT NOT NULL REFERENCES rimsky_instances(id),
    node_run_id     TEXT,
    frame_id        TEXT,
    checkpoint      TEXT NOT NULL,
    mode            TEXT NOT NULL,
    snapshot        TEXT NOT NULL,
    hit_at          TEXT NOT NULL DEFAULT (datetime('now')),
    resumed_at      TEXT,
    resumed_by_key  TEXT,
    resume_overlay  TEXT
);

CREATE INDEX idx_bp_hits_breakpoint_unresumed
    ON rimsky_breakpoint_hits (breakpoint_id, hit_at)
    WHERE resumed_at IS NULL;

CREATE INDEX idx_bp_hits_instance_seq
    ON rimsky_breakpoint_hits (instance_id, seq);

CREATE INDEX idx_bp_hits_breakpoint_seq
    ON rimsky_breakpoint_hits (breakpoint_id, seq);
