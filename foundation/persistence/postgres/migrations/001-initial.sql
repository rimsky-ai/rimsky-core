-- Rimsky initial schema (post-Phase-5 layer-crystallization).
--
-- Source: layer-crystallization design 2026-05-04 §8.4. Phase 5 of
-- the layer-crystallization plan consolidated the legacy split tables
-- into rimsky_worker_request and rimsky_claim_handle. Pre-v1
-- break-freely: this migration was rewritten in place rather than as
-- a successor; dev DB is nuked on adoption (see
-- .claude/rules/rules.md).
--
-- Schema shape:
--   * rimsky_worker_request — the parent run-bookkeeping row. One per
--     dispatched run. phase column drives the active+held lifecycle
--     ('pending', 'active', 'held', 'completed'). claimed_by carries
--     the supervisor id while phase='active'; cleared on entry to
--     'held' or 'pending'. heartbeat_at refreshed each tick by the
--     owning supervisor; the orphan reaper uses it to find stale
--     'active' rows. frame_id is the modeling-side frame this run
--     belongs to.
--   * rimsky_claim_handle — observability child of
--     rimsky_worker_request (ON DELETE SET NULL: held claim handles
--     outlive their parent's active-phase terminal until auto-
--     terminal resolution fires the producer verb and explicitly
--     deletes the row). One row per (worker_request, lock-or-claim
--     acquired). Carries the producer-supplied address + the
--     realized write semantics. is_held=true marks claims that
--     persist past the active terminal (until the holding subgraph
--     completes). Named locks: lock_kind='named'/lock_name set,
--     store_name and scope_data NULL; intent NULL. Scope claims:
--     lock_kind='scope', store_name + scope_data + intent set.
--   * rimsky_claim_holders — held-claim subgraph state tracker. Rows
--     are inserted at acquisition for nodes in the holding subgraph
--     and tracked through their terminal state. Auto-terminal fires
--     when all rows for a claim_handle are non-active.
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

-- Worker-request queue (nodes ready to run; consolidated parent of claim handles).
CREATE TABLE IF NOT EXISTS rimsky_worker_request (
    id                UUID PRIMARY KEY,
    node_id           UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name     TEXT,                              -- nullable for native nodes
    required_stores   TEXT[] NOT NULL DEFAULT '{}',      -- denormalized at enqueue time
    enqueued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- may be future-dated for backoff
    claimed_by        TEXT,                              -- supervisor id; null when phase ∉ {'active'}
    claimed_at        TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,                       -- updated by supervisor heartbeat tick
    phase             TEXT NOT NULL DEFAULT 'pending'    -- 'pending' | 'active' | 'held' | 'completed'
                      CHECK (phase IN ('pending','active','held','completed')),
    active_terminal_at TIMESTAMPTZ,                      -- when active phase ended (entry to 'held' or 'completed')
    UNIQUE (node_id)                                     -- at most one live worker-request per node
);
CREATE INDEX IF NOT EXISTS rimsky_worker_request_pending_idx   ON rimsky_worker_request (enqueued_at) WHERE phase = 'pending';
CREATE INDEX IF NOT EXISTS rimsky_worker_request_claimed_idx   ON rimsky_worker_request (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_worker_request_heartbeat_idx ON rimsky_worker_request (last_heartbeat_at) WHERE phase = 'active';
CREATE INDEX IF NOT EXISTS rimsky_worker_request_phase_idx     ON rimsky_worker_request (phase);

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

-- Claim handles. One row per held lock or scope-claim across two kinds:
--   * 'named' — named-lock predicate (lock_name set)
--   * 'scope' — store scope claim (store_name + scope_data + intent set;
--               address populated by ClaimProducer.Open within the same
--               acquisition tx)
--
-- worker_request_id is the FK-cascade child of rimsky_worker_request
-- (per the layer-crystallization Phase-5 consolidation): when the
-- worker-request is deleted, every claim handle of that worker-request
-- is removed. is_held=true marks claims that persist past the active
-- terminal (until the holding-subgraph completes — see auto-terminal).
-- holder_node_id is preserved for observability and the named-lock
-- counting predicate. Inserted atomically with the worker-request's
-- transition into 'active' (foundation contract §5.4 / spec §7.3).
-- last_heartbeat_at and expires_at extended on each supervisor
-- heartbeat tick. Removed at terminal (claimant-guarded) or
-- auto-terminal for held claims. Orphan-reaped at 5x heartbeat_interval
-- through the worker-request parent.
CREATE TABLE IF NOT EXISTS rimsky_claim_handle (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ON DELETE SET NULL (NOT cascade) so held claim handles outlive
    -- their owning worker-request's active-phase terminal until auto-
    -- terminal resolution fires the producer verb and explicitly
    -- deletes the row. Cascade would race against held-claim
    -- resolution.
    worker_request_id           UUID REFERENCES rimsky_worker_request(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL CHECK (lock_kind IN ('named', 'scope')),
    lock_name                   TEXT,                    -- non-null for kind='named'
    store_name                  TEXT,                    -- non-null for kind='scope'
    scope_data                  JSONB,                   -- non-null for kind='scope'
    address                     JSONB,                   -- producer-supplied address from Open;
                                                         -- needed by Commit/Abandon/Release/Delete at
                                                         -- terminal AND by orphan reaper. Opaque bytes;
                                                         -- inert in Rimsky per invariant 20.
    intent                      TEXT,                    -- 'r' | 'rw' for kind='scope'; null for kind='named'
    realized_write_semantics    TEXT,                    -- per-claim ClaimProducer.Open verdict; null for named-lock rows
    is_held                     BOOLEAN NOT NULL DEFAULT FALSE,  -- true when claim persists past active terminal
    holder_supervisor_id        TEXT NOT NULL,           -- TEXT, matches rimsky_supervisors.id
    holder_node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                  TIMESTAMPTZ NOT NULL,
    -- Note: address and realized_write_semantics may be NULL even for
    -- scope rows, because Open writes them only after a successful
    -- return (within the same acquisition tx). The supervisor inserts
    -- the row with NULL address and updates it after Open returns.
    CONSTRAINT claim_handle_kind_fields CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND store_name IS NULL     AND scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'scope' AND lock_name IS NULL     AND store_name IS NOT NULL AND scope_data IS NOT NULL AND intent IN ('r', 'rw'))
    )
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_supervisor    ON rimsky_claim_handle (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_node          ON rimsky_claim_handle (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_named         ON rimsky_claim_handle (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_scope         ON rimsky_claim_handle (store_name) WHERE lock_kind = 'scope';
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_expires       ON rimsky_claim_handle (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_worker_req    ON rimsky_claim_handle (worker_request_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_handle_held          ON rimsky_claim_handle (worker_request_id) WHERE is_held = TRUE;

-- Held-claim subgraph state ledger. One row per
-- (claim_handle, holder_node) pair from the holding subgraph (acquirer
-- + transitive inheritors), inserted at the acquirer's Open call when
-- the claim is held (claim_handle.is_held=TRUE; subgraph size > 1).
-- state flips 'active' -> 'completed' (success) or 'failed'
-- (give-up/failure) per the held-claim resolution mechanism (foundation
-- contract §5.5 / spec §4.10 invariant 13). When all rows for a
-- claim_handle reach a non-active state, auto-terminal fires the
-- aggregate-outcome resolution and the worker-request row is deleted;
-- ON DELETE CASCADE through claim_handle cleans up these rows.
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id               UUID PRIMARY KEY,
    claim_handle_id  UUID NOT NULL REFERENCES rimsky_claim_handle(id) ON DELETE CASCADE,
    holder_node_id   UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at     TIMESTAMPTZ,
    UNIQUE (claim_handle_id, holder_node_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_claim_handle  ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_node          ON rimsky_claim_holders (holder_node_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';
