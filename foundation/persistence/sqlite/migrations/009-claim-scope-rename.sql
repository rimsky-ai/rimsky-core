-- =====  Scope → ClaimScope rename  =====
-- SQLite parallel. Recreate rimsky_claim_handles with the renamed
-- column, updated CHECK enum, and renamed index. SQLite cannot alter
-- CHECK constraints; the recreate-table pattern handles it cleanly
-- under pre-v1 break-freely.
-- Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §"Rename 2".
--
-- SQLite FK-reference rewrite gotcha: by default (since 3.25.0)
-- ALTER TABLE ... RENAME TO rewrites every FK reference in dependent
-- tables to the new name. For this rename-then-drop pattern that
-- means rimsky_claim_holders.claim_handle_id (FK → rimsky_claim_handles)
-- would temporarily point at rimsky_claim_handles_old, then be left
-- dangling after the DROP. legacy_alter_table = ON disables the FK
-- rewrite for the duration of this transaction so the dependent FK
-- continues to reference the rimsky_claim_handles symbol we recreate
-- below. (This pragma is tx-scoped on most drivers but we also
-- explicitly set it OFF at the bottom for defensiveness.)
PRAGMA legacy_alter_table = ON;

ALTER TABLE rimsky_claim_handles RENAME COLUMN scope_data TO claim_scope_data;
UPDATE rimsky_claim_handles SET lock_kind = 'claim_scope' WHERE lock_kind = 'scope';

DROP INDEX IF EXISTS idx_rimsky_claim_handles_scope;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_supervisor;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_node;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_named;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_expires;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_node_run;
DROP INDEX IF EXISTS idx_rimsky_claim_handles_held;
DROP INDEX IF EXISTS idx_claim_handles_parent;
DROP INDEX IF EXISTS rimsky_claim_handles_active_idx;
DROP INDEX IF EXISTS rimsky_claim_handles_committed_durable_idx;

-- Rename old table, recreate with the updated CHECKs, copy, drop.
ALTER TABLE rimsky_claim_handles RENAME TO rimsky_claim_handles_old;

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

INSERT INTO rimsky_claim_handles
    (id, node_run_id, lock_kind, lock_name, producer_name, claim_scope_data, address, intent,
     realized_write_semantics, is_held, holder_supervisor_id, holder_node_id, claimed_at,
     last_heartbeat_at, expires_at, frame_id, parent_claim_handle_id, lifetime, version_id,
     producer_candidate_handle, aggregation_policy, expected_children_count,
     committed_children_count, abandoned_children_count, state, resolved_at)
SELECT id, node_run_id, lock_kind, lock_name, producer_name, claim_scope_data, address, intent,
       realized_write_semantics, is_held, holder_supervisor_id, holder_node_id, claimed_at,
       last_heartbeat_at, expires_at, frame_id, parent_claim_handle_id, lifetime, version_id,
       producer_candidate_handle, aggregation_policy, expected_children_count,
       committed_children_count, abandoned_children_count, state, resolved_at
  FROM rimsky_claim_handles_old;

DROP TABLE rimsky_claim_handles_old;

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

-- Restore default. legacy_alter_table is a connection-scoped pragma;
-- migrations run inside a single tx that the runner discards, but
-- belt-and-suspenders for any driver that escalates pragma scope.
PRAGMA legacy_alter_table = OFF;

-- Recreate rimsky_claim_holders. Modern SQLite (≥ 3.25) rewrites every
-- FK reference in dependent tables when ALTER TABLE ... RENAME TO
-- fires (even with legacy_alter_table the modernc.org/sqlite driver
-- may not honor the pragma in all paths). So
-- rimsky_claim_holders.claim_handle_id — originally REFERENCES
-- rimsky_claim_handles(id) — gets temporarily rewritten to point at
-- rimsky_claim_handles_old, then DROP TABLE leaves the FK dangling.
-- The dangling reference is silent until a DELETE cascades into
-- rimsky_claim_holders and FK enforcement chases the stale target,
-- surfacing as "no such table: rimsky_claim_handles_old".
--
-- Recreate the table to re-establish the FK against the real
-- rimsky_claim_handles. Pre-v1 break-freely covers the COPY step —
-- there is no production data; the table starts empty post-migration.
DROP INDEX IF EXISTS idx_rimsky_claim_holders_claim_handle;
DROP INDEX IF EXISTS idx_rimsky_claim_holders_run;
DROP INDEX IF EXISTS idx_rimsky_claim_holders_active_subgraph;

ALTER TABLE rimsky_claim_holders RENAME TO rimsky_claim_holders_old;

CREATE TABLE rimsky_claim_holders (
    id               TEXT PRIMARY KEY,
    claim_handle_id  TEXT NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_run_id    TEXT NOT NULL REFERENCES rimsky_node_runs(id)     ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active','completed','failed')),
    completed_at     TEXT,
    frame_id         TEXT,
    UNIQUE (claim_handle_id, holder_run_id)
);

INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, completed_at, frame_id)
    SELECT id, claim_handle_id, holder_run_id, state, completed_at, frame_id
      FROM rimsky_claim_holders_old;

DROP TABLE rimsky_claim_holders_old;

CREATE INDEX idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX idx_rimsky_claim_holders_run          ON rimsky_claim_holders (holder_run_id);
CREATE INDEX idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';
