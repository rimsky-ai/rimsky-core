-- Rimsky initial schema (stores-redesign end state)
--
-- This file defines the full v1 schema in a single migration. The
-- stores-redesign restructured the resource abstraction into "stores" with a
-- unified claim/lock/commit/resolve vocabulary; rimsky is pre-v1 with no
-- production data, so the previous `001-initial.sql` and `002-data-ref-jsonb.sql`
-- are replaced wholesale rather than evolved through ALTER migrations. The
-- spec's §9 is authoritative for the schema; this file mirrors it in full.
--
-- Key shape vs. the pre-redesign schema:
--   * `rimsky_resources` and `rimsky_resource_versions` are gone. Versioned
--     data lives in store-owned backends; rimsky tracks claims and locks, not
--     resource bytes.
--   * New tables: `rimsky_node_attributes` (per-node attribute snapshot used
--     for substitution + executor I/O), `rimsky_lock_holders` (named/region/
--     claim lock ledger), and `rimsky_claim_holders` (held-claim ledger).
--   * `rimsky_nodes.concurrency_tags` is dropped — concurrency control is
--     expressed as named locks declared in templates and recorded in
--     `rimsky_lock_holders`.
--   * `rimsky_supervisors.accepted_stores TEXT[]` is added so the
--     pool-specialization predicate (§14.2) can route dispatches to a
--     supervisor that has the right local stores wired up.
--   * `rimsky_dispatch.concurrency_tags` is dropped, `required_stores TEXT[]`
--     is added (denormalized at enqueue), `last_heartbeat_at TIMESTAMPTZ` is
--     added (the orphan sweep predicate moves from `claimed_at` to
--     `last_heartbeat_at`), and `executor_name` becomes nullable for native
--     (claim-only) nodes.
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
    spec        JSONB NOT NULL,                       -- parsed template YAML (stores/locks/attributes/claim_resolutions)
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
-- `executor` is the supervisor executor name (null for native, claim-only
-- nodes). `schedule_cron` is the optional cron expression; when non-null a
-- matching row exists in `rimsky_schedules`. `concurrency_tags` is gone —
-- per-node concurrency control now lives in `locks: [...]` template
-- declarations enforced via `rimsky_lock_holders`.
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                    UUID PRIMARY KEY,
    instance_id           UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type             TEXT NOT NULL,             -- template-declared type name
    executor              TEXT,                      -- supervisor executor name; null for native nodes
    schedule_cron         TEXT,                      -- cron expr if node is scheduled; null otherwise
    state                 TEXT NOT NULL,             -- fresh | stale | running | failed
    dependencies          UUID[] NOT NULL,           -- resolved to node ids at instantiation
    current_error_class   TEXT,
    retry_counter         INT NOT NULL DEFAULT 0,
    action_index          INT NOT NULL DEFAULT 0,
    last_heartbeat_at     TIMESTAMPTZ,               -- set while running; null otherwise
    assigned_supervisor_id TEXT,                     -- TEXT, matches rimsky_supervisors.id; null if not running
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_nodes_state_updated_at_idx ON rimsky_nodes (state, updated_at);
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);

-- Supervisor registry (heartbeat + callback endpoints + local capability advertisement).
-- `accepted_executors` lists executor names this supervisor handles;
-- `accepted_stores` lists store names this supervisor has wired up locally
-- and is therefore eligible to dispatch into (§14.2 pool specialization).
CREATE TABLE IF NOT EXISTS rimsky_supervisors (
    id                  TEXT PRIMARY KEY,            -- supervisor_id from config
    accepted_executors  TEXT[] NOT NULL,             -- executor names this supervisor handles
    accepted_stores     TEXT[] NOT NULL DEFAULT '{}',-- store names locally available on this supervisor
    concurrency         INT NOT NULL,
    callback_host       TEXT,                        -- null for deterministic-only supervisors
    callback_port       INT,
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_node_count   INT NOT NULL DEFAULT 0,
    registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);

-- Dispatch queue (nodes ready to run).
-- `executor_name` is nullable for native (claim-only) nodes that need no
-- executor process. `required_stores` is denormalized at enqueue time from
-- the template's per-node-type required-store set; the pool-specialization
-- predicate (§14.2) uses it to route claims to a supervisor whose
-- `accepted_stores` is a superset. The orphan-claim sweep predicate uses
-- `last_heartbeat_at` (not `claimed_at`) so claim age tracks heartbeat
-- liveness.
CREATE TABLE IF NOT EXISTS rimsky_dispatch (
    id                UUID PRIMARY KEY,
    node_id           UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name     TEXT,                              -- nullable for native nodes
    required_stores   TEXT[] NOT NULL DEFAULT '{}',      -- denormalized at enqueue time
    enqueued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- may be future-dated for backoff
    claimed_by        TEXT,                              -- supervisor id; null until claimed
    claimed_at        TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,                       -- updated by supervisor heartbeat tick; orphan-sweep predicate
    UNIQUE (node_id)                                     -- at most one pending dispatch per node
);
CREATE INDEX IF NOT EXISTS rimsky_dispatch_pending_idx   ON rimsky_dispatch (enqueued_at) WHERE claimed_by IS NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_claimed_idx   ON rimsky_dispatch (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_heartbeat_idx ON rimsky_dispatch (last_heartbeat_at) WHERE claimed_by IS NOT NULL;

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

-- Event log (single append-only; JSONB payload). Payload kinds extended by
-- the redesign: lock_acquired/lock_released/lock_orphan_reaped,
-- attributes_substituted/attributes_committed/attributes_validation_failed,
-- claim_acquired/claim_held/claim_resolved, template_resolution_failed.
-- Removed: commit, pure_cascade_commit (folded into attributes_committed).
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

-- Per-node attribute snapshot. Created lazily on first dispatch; populated
-- from source-directive substitutions, updated by executor writeback
-- (incremental or terminal-final), validated against the template's schema
-- on commit. On retry, `run_attempt` increments and `data` is cleared per
-- §5.7.3 (source-driven fields repopulated; executor-populated fields
-- cleared unless the node opts into resume_then_retry). On invalidate, the
-- row is preserved as audit trail; on instance delete, it CASCADE-deletes.
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id     UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt INT NOT NULL DEFAULT 0,
    data        JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Lock ledger. One row per held lock across all three lock kinds:
--   * 'named'  — named-lock predicate (lock_name set)
--   * 'region' — store region lock (store_name + region_data set)
--   * 'claim'  — claim-id lock against a store (store_name + claim_id set)
-- Inserted atomically with the dispatch claim (§13.3). `last_heartbeat_at`
-- and `expires_at` extended on each supervisor heartbeat tick. Removed on
-- ReleaseLock (claimant-guarded). Orphan-reaped at 5x heartbeat_interval.
CREATE TABLE IF NOT EXISTS rimsky_lock_holders (
    id                   UUID PRIMARY KEY,
    lock_kind            TEXT NOT NULL,           -- 'named' | 'region' | 'claim'
    lock_name            TEXT,                    -- non-null for kind='named'
    store_name           TEXT,                    -- non-null for kind in ('region','claim')
    region_data          JSONB,                   -- non-null for kind='region'
    claim_id             TEXT,                    -- non-null for kind='claim'
    holder_supervisor_id TEXT NOT NULL,           -- TEXT, matches rimsky_supervisors.id
    holder_node_id       UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at           TIMESTAMPTZ NOT NULL,
    CONSTRAINT lock_kind_fields CHECK (
        (lock_kind = 'named'  AND lock_name IS NOT NULL AND store_name IS NULL     AND region_data IS NULL     AND claim_id IS NULL) OR
        (lock_kind = 'region' AND lock_name IS NULL     AND store_name IS NOT NULL AND region_data IS NOT NULL AND claim_id IS NULL) OR
        (lock_kind = 'claim'  AND lock_name IS NULL     AND store_name IS NOT NULL AND region_data IS NULL     AND claim_id IS NOT NULL)
    )
);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_named_idx      ON rimsky_lock_holders (lock_name) WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_store_idx      ON rimsky_lock_holders (store_name) WHERE lock_kind IN ('region','claim');
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_supervisor_idx ON rimsky_lock_holders (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_expires_idx    ON rimsky_lock_holders (expires_at);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_node_idx       ON rimsky_lock_holders (holder_node_id);

-- Held-claim ledger. One row per (claim_id, terminal-leaf-node) pair from
-- the §11.4 DAG walk, inserted at commit of the claiming-source node when
-- `hold: true`. `state` flips 'active' -> 'completed' per the §5.6.4
-- algorithm; `actual_action` is null while active and populated at
-- completion ('delete' | 'release_to_back' | 'release_to_head' |
-- 'delete_won', where 'delete_won' marks a sibling row collapsed by another
-- sibling's winning delete). On instance delete, rows CASCADE-delete via
-- holder_node_id FK.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id              UUID PRIMARY KEY,
    claim_id        TEXT NOT NULL,
    store_name      TEXT NOT NULL,
    holder_node_id  UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    on_commit       TEXT NOT NULL,                    -- declared: 'delete' | 'release_to_back' | 'release_to_head'
    on_give_up      TEXT NOT NULL,                    -- declared: same vocabulary
    actual_action   TEXT,                             -- recorded at completion: 'delete' | 'release_to_back' | 'release_to_head' | 'delete_won'
    state           TEXT NOT NULL DEFAULT 'active',   -- 'active' | 'completed'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
-- The (claim_id, holder_node_id) uniqueness is per-active-cycle: ring-buffer
-- claim stores reuse `claim_id` (= `item_id`) across cycles, so once a
-- holder row transitions to 'completed' a fresh 'active' row must be
-- insertable for the next cycle. Restricting the unique index to
-- state='active' enforces "one ACTIVE holder per (claim, leaf) at a time"
-- without forbidding reuse after completion. Without the partial WHERE,
-- the second cycle's insertHeldClaimHolders would conflict against the
-- prior cycle's now-completed row and roll back the entire commit tx,
-- leaving the items-table row stuck in 'in_progress' (the acquisition tx
-- already committed the state=in_progress flip).
CREATE UNIQUE INDEX IF NOT EXISTS rimsky_claim_holders_claim_node_idx
    ON rimsky_claim_holders (claim_id, holder_node_id) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS rimsky_claim_holders_active_idx ON rimsky_claim_holders (claim_id) WHERE state = 'active';
