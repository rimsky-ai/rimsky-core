-- 2026-05-08 Platform extensions: parked node lifecycle + blob spill +
-- named-event ledger. Bundles sections C1–C7 of
-- .ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers.md.
--
-- BRITTLENESS NOTE (idempotency vs. postgres mirror):
--   The postgres mirror (004 in foundation/persistence/postgres/migrations/)
--   uses ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS / CREATE
--   INDEX IF NOT EXISTS exclusively, so re-running it after a wiped
--   rimsky_migrations table is a no-op.
--
--   SQLite does NOT support ADD COLUMN IF NOT EXISTS. The bare
--   `ALTER TABLE ... ADD COLUMN` statements below will fail with
--   "duplicate column name" if this file is re-applied (e.g. because
--   rimsky_migrations was dropped or restored from a snapshot taken
--   before this migration was recorded). The migration runner relies on
--   the rimsky_migrations row to dedupe; lose that row and re-running is
--   a hard failure.
--
--   Recovery if rimsky_migrations is lost: do NOT just re-run the SQL.
--   Either (a) restore rimsky_migrations from a backup, or (b) manually
--   INSERT a row into rimsky_migrations with this file's name to mark
--   it applied (assuming the columns are present), or (c) drop the
--   per-column ALTER TABLE statements that have already landed before
--   re-applying.
--
--   Multi-host deployments must use the postgres driver per the
--   project-level CLAUDE.md note; this migration's brittleness is a
--   dev-only concern in practice.
--
-- The rimsky_worker_request CHECK widening (phase += 'parked') uses
-- SQLite's writable_schema mechanism rather than the rebuild dance:
--   1. ALTER TABLE ADD COLUMN handles the seven new park-state columns,
--      the consecutive_retries_no_progress counter, and the two
--      denormalized DSL fields. SQLite supports ADD COLUMN cleanly
--      without rebuild as long as the new columns have NULL or a
--      constant default — but it does NOT honor an
--      "ADD COLUMN IF NOT EXISTS" clause. See the brittleness note
--      above.
--   2. PRAGMA writable_schema rewrites the existing CREATE TABLE text
--      to widen the phase CHECK constraint to include 'parked'. We do
--      not change the column structure; only the constraint string.
--      This avoids the rename + recreate dance that SQLite's
--      "ALTER TABLE RENAME rewrites FK references in other tables"
--      behavior makes brittle (and which legacy_alter_table only
--      partially mitigates inside a transaction).
--
-- The writable_schema PRAGMA is inert until any DML against
-- sqlite_schema runs; the schema_version PRAGMA bump at the end forces
-- SQLite to reparse on the next connection.

-- ---------------------------------------------------------------------------
-- C2 + C5 + C7: ADD COLUMN for the new park-state, retry-cap, and
-- denormalized DSL fields.
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_worker_request ADD COLUMN parked_at TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN resume_at TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN parked_payload_inline BLOB;
ALTER TABLE rimsky_worker_request ADD COLUMN parked_payload_handle TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN parked_payload_handle_backend TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN session_token TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN parked_reason TEXT;
-- wake_reason carries the WakeReason enum value set by ResumeParkedInTx so
-- the resume-dispatch path populates ResumeContext.resume_reason. See the
-- postgres mirror for the rationale.
ALTER TABLE rimsky_worker_request ADD COLUMN wake_reason TEXT;
ALTER TABLE rimsky_worker_request ADD COLUMN consecutive_retries_no_progress INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rimsky_worker_request ADD COLUMN max_park_duration_seconds INTEGER;
ALTER TABLE rimsky_worker_request ADD COLUMN max_retries_without_progress INTEGER;

CREATE INDEX IF NOT EXISTS idx_worker_request_parked_resume
    ON rimsky_worker_request(resume_at)
    WHERE phase = 'parked' AND resume_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- C1: widen the phase CHECK constraint via direct schema rewrite.
-- The new CREATE TABLE text reflects the present-day column layout
-- (post-ADD-COLUMN above) plus the widened phase CHECK.
-- ---------------------------------------------------------------------------
PRAGMA writable_schema = ON;

UPDATE sqlite_schema
SET sql = 'CREATE TABLE rimsky_worker_request (
    id                                TEXT PRIMARY KEY,
    node_id                           TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name                     TEXT,
    required_stores                   TEXT NOT NULL DEFAULT ''[]'',
    enqueued_at                       TEXT NOT NULL DEFAULT (datetime(''now'')),
    claimed_by                        TEXT,
    claimed_at                        TEXT,
    last_heartbeat_at                 TEXT,
    phase                             TEXT NOT NULL DEFAULT ''pending''
                                       CHECK (phase IN (''pending'',''active'',''held'',''parked'',''completed'')),
    active_terminal_at                TEXT,
    frame_id                          TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    parked_at                         TEXT,
    resume_at                         TEXT,
    parked_payload_inline             BLOB,
    parked_payload_handle             TEXT,
    parked_payload_handle_backend     TEXT,
    session_token                     TEXT,
    parked_reason                     TEXT,
    wake_reason                       TEXT,
    consecutive_retries_no_progress   INTEGER NOT NULL DEFAULT 0,
    max_park_duration_seconds         INTEGER,
    max_retries_without_progress      INTEGER,
    UNIQUE (node_id)
)'
WHERE type = 'table' AND name = 'rimsky_worker_request';

PRAGMA writable_schema = OFF;

-- Bumping schema_version forces SQLite to reparse the schema on the
-- next connection so the widened CHECK takes effect for subsequent
-- INSERTs and UPDATEs.
--
-- WORKAROUND: PRAGMA schema_version is a literal write (no arithmetic),
-- and the SQL-level migration runner has no way to read-modify-write
-- the value. Setting it to a known-large constant guarantees the value
-- is at least as large as it was before the rewrite for any plausible
-- pre-existing migration count (DDL ops typically bump it by 1 each).
-- The truly idiomatic approach (read the current value, increment, write
-- it back) requires a host-language hook that the runner does not
-- expose. Pre-v1 we accept this as a one-shot quirk; if a future
-- migration does the same writable_schema dance against this DB it
-- MUST also bump schema_version to a value strictly greater than this
-- constant or to a fresh higher value computed in Go. See
-- https://sqlite.org/lang_altertable.html#otheralter and
-- .ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers-notes.md
-- for context.
PRAGMA schema_version = 1000000;

-- ---------------------------------------------------------------------------
-- C3. Blob handle columns on rimsky_node_attributes.
-- ---------------------------------------------------------------------------
ALTER TABLE rimsky_node_attributes ADD COLUMN value_handle TEXT;
ALTER TABLE rimsky_node_attributes ADD COLUMN value_handle_backend TEXT;

-- ---------------------------------------------------------------------------
-- C4. Orphan-blob tracking.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rimsky_blob_orphans (
    handle      TEXT PRIMARY KEY,
    backend     TEXT NOT NULL,
    orphaned_at TEXT NOT NULL DEFAULT (datetime('now')),
    reap_after  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blob_orphans_reap
    ON rimsky_blob_orphans(reap_after);

-- ---------------------------------------------------------------------------
-- C6. rimsky_node_events ledger.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rimsky_node_events (
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
CREATE INDEX IF NOT EXISTS idx_node_events_lookup
    ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);
