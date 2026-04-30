-- Rimsky initial schema (v2-defined; preserved at v3).
--
-- Source: stores-redesign v2 (preserved unchanged at v3 — see
-- docs/specs/2026-04-27-stores-redesign-v3-design.md §14). The schema
-- itself did not change at v3; the v3 cycle moved stores out-of-process
-- but kept rimsky_lock_holders / rimsky_claim_holders / dispatch /
-- nodes / templates exactly as v2 left them.
--
-- This file defines the full v1 schema in a single migration. The
-- stores-redesign v2 (docs/specs/2026-04-27-stores-redesign-v2-design.md)
-- collapsed claim/region into a single store-bound primitive and split
-- named locks out as a non-store primitive. Pre-v1: rewrite in place;
-- dev DB is nuked on adoption (see .claude/rules/rules.md).
--
-- Key shape vs. the v1-prior-rewrite schema:
--   * lock_kind enum reduced from ('named','region','claim') to
--     ('named','region'). The 'claim' kind dissolved — pick-policy claims
--     are just region claims with substrate-chosen region_data.
--   * rimsky_lock_holders.claim_id column dropped. Substrate's identifier
--     lives in region_data.
--   * rimsky_lock_holders.address column added — substrate-supplied
--     address from Open, needed by terminal verbs and the orphan reaper.
--   * rimsky_lock_holders.intent column added ('r' | 'rw' | NULL for
--     named locks).
--   * rimsky_claim_holders.lock_holder_id FK added (ON DELETE CASCADE)
--     so claim-holder rows clean up when the lock-holder row is deleted
--     at auto-terminal.
--   * rimsky_claim_holders: claim_id, store_name, on_commit, on_give_up,
--     actual_action columns dropped. Per the 2026-04-30 stores-protocol
--     cleanup, rimsky carries only a success/failure binary across the
--     wire (success → Commit; failure → Abandon); substrate disposition
--     lives in each store-service's own config, not in template metadata.
--     state enum gains 'failed'.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS
-- throughout. Belt-and-suspenders with the migration runner's advisory
-- lock and per-file tracking in rimsky_migrations.

CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Templates (deploy targets)
CREATE TABLE IF NOT EXISTS rimsky_templates (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    version     TEXT NOT NULL,
    spec        JSONB NOT NULL,                       -- parsed template (stores/locks/inherits/attributes/quality_rules/error_types)
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
CREATE TABLE IF NOT EXISTS rimsky_dispatch (
    id                UUID PRIMARY KEY,
    node_id           UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name     TEXT,                              -- nullable for native nodes
    required_stores   TEXT[] NOT NULL DEFAULT '{}',      -- denormalized at enqueue time
    enqueued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- may be future-dated for backoff
    claimed_by        TEXT,                              -- supervisor id; null until claimed
    claimed_at        TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,                       -- updated by supervisor heartbeat tick
    UNIQUE (node_id)                                     -- at most one pending dispatch per node
);
CREATE INDEX IF NOT EXISTS rimsky_dispatch_pending_idx   ON rimsky_dispatch (enqueued_at) WHERE claimed_by IS NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_claimed_idx   ON rimsky_dispatch (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_heartbeat_idx ON rimsky_dispatch (last_heartbeat_at) WHERE claimed_by IS NOT NULL;

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

-- Per-node attribute snapshot.
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id     UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt INT NOT NULL DEFAULT 0,
    data        JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Lock ledger. One row per held lock across two kinds:
--   * 'named'  — named-lock predicate (lock_name set)
--   * 'region' — store region claim (store_name + region_data + intent set;
--                address populated by Open within the same acquisition tx)
-- Inserted atomically with the dispatch claim (v3 spec §7.3). last_heartbeat_at
-- and expires_at extended on each supervisor heartbeat tick. Removed at
-- node terminal (claimant-guarded) or auto-terminal for held claims.
-- Orphan-reaped at 5x heartbeat_interval.
CREATE TABLE IF NOT EXISTS rimsky_lock_holders (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lock_kind            TEXT NOT NULL CHECK (lock_kind IN ('named', 'region')),
    lock_name            TEXT,                    -- non-null for kind='named'
    store_name           TEXT,                    -- non-null for kind='region'
    region_data          JSONB,                   -- non-null for kind='region'
    address              JSONB,                   -- substrate-supplied address from Open;
                                                  -- needed by Commit/Abandon/Release/Delete at
                                                  -- terminal AND by orphan reaper. Opaque bytes;
                                                  -- inert in Rimsky per invariant 20.
    intent               TEXT,                    -- 'r' | 'rw' for kind='region'; null for kind='named'
    holder_supervisor_id TEXT NOT NULL,           -- TEXT, matches rimsky_supervisors.id
    holder_node_id       UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at           TIMESTAMPTZ NOT NULL,
    -- Note: address may be NULL even for region rows, because Open writes
    -- the address only after a successful return (within the same
    -- acquisition tx). The supervisor inserts the row with NULL address
    -- and updates it after Open returns (per v3 spec §7.3).
    CONSTRAINT lock_kind_fields CHECK (
        (lock_kind = 'named'  AND lock_name IS NOT NULL AND store_name IS NULL     AND region_data IS NULL     AND intent IS NULL) OR
        (lock_kind = 'region' AND lock_name IS NULL     AND store_name IS NOT NULL AND region_data IS NOT NULL AND intent IN ('r', 'rw'))
    )
);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_supervisor ON rimsky_lock_holders (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_node       ON rimsky_lock_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_named      ON rimsky_lock_holders (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_region     ON rimsky_lock_holders (store_name) WHERE lock_kind = 'region';
CREATE INDEX IF NOT EXISTS idx_rimsky_lock_holders_expires    ON rimsky_lock_holders (expires_at) WHERE expires_at IS NOT NULL;

-- Held-claim ledger. One row per (lock_holder, holder_node) pair from the
-- holding subgraph (acquirer + transitive inheritors), inserted at the acquirer's Open call when the
-- claim is held (subgraph size > 1). state flips 'active' -> 'completed'
-- (success) or 'failed' (give-up/failure) per v3 spec §4.10 invariant 13.
-- When all rows for a lock_holder reach a non-active state, auto-terminal
-- fires the aggregate-outcome resolution (v3 spec §4.10 invariant 13) and
-- the lock_holder row is deleted;
-- ON DELETE CASCADE cleans up these rows.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id              UUID PRIMARY KEY,
    lock_holder_id  UUID NOT NULL REFERENCES rimsky_lock_holders(id) ON DELETE CASCADE,
    holder_node_id  UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    state           TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at    TIMESTAMPTZ,
    UNIQUE (lock_holder_id, holder_node_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_lock_holder    ON rimsky_claim_holders (lock_holder_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_node           ON rimsky_claim_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (lock_holder_id) WHERE state = 'active';
