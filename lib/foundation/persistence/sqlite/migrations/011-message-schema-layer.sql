-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 010-message-schema-layer.sql (SQLite mirror of postgres 010).
--
-- Make messages a typed, schema-declared primitive. See the postgres
-- migration for rationale; SQLite has more limited ALTER TABLE semantics,
-- so columns referenced by CHECK constraints (rimsky_frames.frame_resolution_mode,
-- rimsky_instances.frame_delivery_mode) and columns that participate in a
-- column rename across the suite use the rebuild-table dance for safety:
--   1. CREATE TABLE <name>_new with the new shape.
--   2. INSERT INTO <name>_new SELECT ... FROM <name>.
--   3. DROP TABLE <name>.
--   4. ALTER TABLE <name>_new RENAME TO <name>.
--   5. Recreate indexes.
--
-- The frame→message FK is ON DELETE RESTRICT so frame-origin audit history
-- cannot be silently destroyed: a future message-retention sweep that tries
-- to delete a message row still referenced by a frame fails loudly instead
-- of cascading the deletion through to the dependent frames (and via
-- rimsky_frames → rimsky_node_runs ON DELETE CASCADE, every run row tied to
-- those frames). Instance-wide teardown is unaffected — instance delete
-- fires CASCADE on rimsky_frames.instance_id and rimsky_messages.instance_id
-- in parallel, so both children disappear without crossing the
-- frame→message FK.
--
-- Pre-v1 forward-only — existing dev databases must be nuked, not upgraded;
-- the NOT NULL on rimsky_frames.triggering_message_id has no DEFAULT, so the
-- rebuild SELECT below assumes rimsky_frames is empty. Operators with a
-- non-empty dev db must drop and recreate.
--
-- PRE-CONDITION (loud version, will save you a cryptic FK failure): this
-- script REQUIRES a clean dev DB. Specifically:
--
--   * rimsky_frames is EMPTY (the rebuild SELECT WHERE 1 = 0 copies zero
--     rows; a populated table would fail the NOT NULL constraint).
--   * rimsky_nodes.frame_id and rimsky_node_runs.frame_id are NULL for
--     every row (those FKs point at rimsky_frames; DROP TABLE rimsky_frames
--     leaves dangling references that defer_foreign_keys re-checks at
--     COMMIT, rolling back the whole migration).
--   * rimsky_messages.frame_id IS NULL for every row. The rebuild SELECT
--     below carries the column verbatim into a new table whose FK targets
--     the freshly-rebuilt (empty) rimsky_frames; a row with a non-NULL
--     frame_id would reference a frame that no longer exists, and the
--     deferred FK check at COMMIT rolls the entire migration back. The
--     SELECT therefore filters on `frame_id IS NULL` and any populated
--     dev DB with delivered messages must be nuked.
--
-- If any of those pre-conditions fails, the migration aborts at COMMIT
-- with an opaque FK violation. Drop the dev DB and re-create rather than
-- burning cycles diagnosing it. (Pre-v1 has no backwards-compat duty per
-- .claude/rules/rules.md — clean removal is the bias; this script is the
-- nuke-and-replace path, not an in-place migration.)
--
-- defer_foreign_keys = ON: the frames and messages rebuilds reference each
-- other transitively (rimsky_frames.triggering_message_id → rimsky_messages),
-- and SQLite's rebuild dance walks each table independently — DROP TABLE
-- rimsky_messages would otherwise fail the immediate FK check from the
-- already-rebuilt rimsky_frames. Deferring until COMMIT makes the operation
-- order safe regardless of which side rebuilds first.
--
-- No BEGIN/COMMIT wrapper: the migrator's ApplyOne already opens a tx around
-- the script execution, so wrapping here trips SQLite's "cannot start a
-- transaction within a transaction" guard. The migrator's tx COMMIT is what
-- triggers the deferred FK check.
PRAGMA defer_foreign_keys = ON;

-- LOUD PRE-CONDITION ASSERTIONS (SQLite parity for the Postgres DO-block
-- at lines 76-90 of the sibling migration). Postgres can RAISE EXCEPTION
-- on a non-empty rimsky_frames; SQLite has no top-level raise() outside
-- a trigger-program, so the assertion runs as: create a temporary
-- migration-scoped trigger that fires on a no-op INSERT into a temp
-- assertion table, then drive the INSERT only when the guard predicate
-- is non-empty. SQLite's `RAISE(ABORT, '<message>')` inside the trigger
-- aborts the tx with the named diagnostic, which is exactly the
-- behaviour the Postgres DO block produces. The temp trigger and table
-- evaporate with the connection so subsequent migrations are unaffected.
--
-- The bare migration's rebuild SELECTs use `WHERE 1 = 0` (frames) and
-- `WHERE frame_id IS NULL` (messages); a populated dev DB without these
-- guards would lose rows silently — the WHERE clauses would simply not
-- copy them across. The loud assertion turns that silent loss into a
-- visible failure, matching the Postgres path's behaviour.
CREATE TEMPORARY TABLE rimsky_migration_010_assert (probe INTEGER);
CREATE TEMPORARY TRIGGER rimsky_migration_010_assert_frames
    BEFORE INSERT ON rimsky_migration_010_assert
    WHEN NEW.probe = 1
BEGIN
    SELECT RAISE(ABORT, 'migration 010-message-schema-layer: rimsky_frames is not empty. Pre-v1 forward-only: drop and recreate the dev database before running this migration. The NOT NULL on rimsky_frames.triggering_message_id has no DEFAULT, and the rebuild SELECT below copies zero rows from a populated table — running it as-is would silently drop those rows.');
END;
CREATE TEMPORARY TRIGGER rimsky_migration_010_assert_messages
    BEFORE INSERT ON rimsky_migration_010_assert
    WHEN NEW.probe = 2
BEGIN
    SELECT RAISE(ABORT, 'migration 010-message-schema-layer: rimsky_messages has rows with non-NULL frame_id. Pre-v1 forward-only: drop and recreate the dev database before running this migration. The rebuild SELECT below filters on frame_id IS NULL — running it as-is would silently drop delivered messages.');
END;

INSERT INTO rimsky_migration_010_assert (probe)
    SELECT 1 WHERE (SELECT COUNT(*) FROM rimsky_frames) > 0;
INSERT INTO rimsky_migration_010_assert (probe)
    SELECT 2 WHERE (SELECT COUNT(*) FROM rimsky_messages WHERE frame_id IS NOT NULL) > 0;

DROP TRIGGER rimsky_migration_010_assert_messages;
DROP TRIGGER rimsky_migration_010_assert_frames;
DROP TABLE rimsky_migration_010_assert;

-- rimsky_frames: rebuild — drop source_node_ids + frame_resolution_mode,
-- add triggering_message_id, drop the coalesce-queued partial unique index.
-- `IF EXISTS` matches the convention used by the other DROP INDEX
-- statements in this file (lines 149, 160-162, 208-209) so a partial
-- migration set (or a future migration that adjusts the lineage) does
-- not trip a hard error at the first DROP.
DROP INDEX IF EXISTS uq_rimsky_frames_coalesce_queued;
DROP INDEX IF EXISTS uq_rimsky_frames_running;
DROP INDEX IF EXISTS idx_rimsky_frames_queued;

CREATE TABLE rimsky_frames_new (
    frame_id                TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    -- ON DELETE RESTRICT: a frame's triggering message is load-bearing
    -- audit history (the frame-origin-audit story depends on every frame
    -- retaining its triggering message). RESTRICT refuses a destructive
    -- ordering and surfaces the bug; CASCADE would silently obliterate the
    -- audit data and every dependent run row. Instance-wide teardown still
    -- works because instance delete CASCADEs both sides in parallel.
    triggering_message_id   TEXT NOT NULL REFERENCES rimsky_messages(id) ON DELETE RESTRICT,
    state                   TEXT NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    queued_at               TEXT NOT NULL DEFAULT (datetime('now')),
    started_at              TEXT,
    ended_at                TEXT,
    frame_timeout_ms        INTEGER NOT NULL CHECK (frame_timeout_ms >= 60000),
    last_progress_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (state != 'running' OR started_at IS NOT NULL),
    CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
);

-- Pre-v1 forward-only: any existing rimsky_frames rows lack a triggering
-- message id; the rebuild SELECT therefore intentionally only copies rows
-- in a normalised state that doesn't exist on a populated dev db (an empty
-- table copies zero rows; a populated table would fail the NOT NULL constraint
-- and the migration rolls back — operator nukes the dev db).
INSERT INTO rimsky_frames_new
    (frame_id, instance_id, triggering_message_id, state, queued_at,
     started_at, ended_at, frame_timeout_ms, last_progress_at)
SELECT frame_id, instance_id, '' AS triggering_message_id, state, queued_at,
       started_at, ended_at, frame_timeout_ms, last_progress_at
  FROM rimsky_frames
 WHERE 1 = 0;

DROP TABLE rimsky_frames;
ALTER TABLE rimsky_frames_new RENAME TO rimsky_frames;

CREATE INDEX idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';
CREATE UNIQUE INDEX uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

-- rimsky_instances: rebuild — drop frame_delivery_mode. We rebuild so we
-- can drop the column AND its CHECK constraint cleanly. The mutual FK with
-- rimsky_run_scopes is preserved via the rebuilt main_run_scope_id column.
CREATE TABLE rimsky_instances_new (
    id                                TEXT PRIMARY KEY,
    template_hash                     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key                      TEXT,
    params                            TEXT NOT NULL DEFAULT '{}',
    attribute_overrides               TEXT NOT NULL DEFAULT '{}',
    attribute_overrides_match_counts  TEXT NOT NULL DEFAULT '[]',
    created_at                        TEXT NOT NULL DEFAULT (datetime('now')),
    terminated_at                     TEXT,
    paused                            INTEGER NOT NULL DEFAULT 0,
    main_run_scope_id                 TEXT NOT NULL REFERENCES rimsky_run_scopes(id) DEFERRABLE INITIALLY DEFERRED DEFAULT '',
    terminate_after_run               INTEGER NOT NULL DEFAULT 0,
    service_bindings                  TEXT,
    created_by_api_key_id             TEXT,
    UNIQUE (template_hash, instance_key)
);

INSERT INTO rimsky_instances_new
    (id, template_hash, instance_key, params, attribute_overrides,
     attribute_overrides_match_counts, created_at, terminated_at, paused,
     main_run_scope_id, terminate_after_run, service_bindings,
     created_by_api_key_id)
SELECT id, template_hash, instance_key, params, attribute_overrides,
       attribute_overrides_match_counts, created_at, terminated_at, paused,
       main_run_scope_id, terminate_after_run, service_bindings,
       created_by_api_key_id
  FROM rimsky_instances;

DROP INDEX IF EXISTS idx_rimsky_instances_terminated;
DROP TABLE rimsky_instances;
ALTER TABLE rimsky_instances_new RENAME TO rimsky_instances;

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;

-- rimsky_messages: rebuild — drop target + backfill_operation_id, rename
-- kind → type. The pending-message index gets recreated against the new
-- type column; the backfill index is dropped entirely.
DROP INDEX IF EXISTS idx_messages_instance_received;
DROP INDEX IF EXISTS idx_messages_backfill;
DROP INDEX IF EXISTS idx_messages_pending;

CREATE TABLE rimsky_messages_new (
    id                     TEXT PRIMARY KEY,
    instance_id            TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    type                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','publisher','instance')),
    payload                BLOB,
    received_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at           TIMESTAMP,
    -- ON DELETE SET NULL is added here as part of the 010 rebuild —
    -- neither 001-schema.sql (sqlite) nor 001-schema.sql (postgres)
    -- declared a REFERENCES clause on rimsky_messages.frame_id; both
    -- baselines shipped the column as plain UUID/TEXT. The postgres
    -- 010-message-schema-layer.sql adds the equivalent FK via
    -- `ADD CONSTRAINT rimsky_messages_frame_id_fkey` so cross-backend
    -- scenarios stay in lockstep (a row passing on one backend with
    -- the FK enforced cannot silently slip past on the other). Under
    -- a future PruneTraceForRetention deleting an old terminal frame,
    -- the message's frame_id NULLs out instead of leaving a dangling
    -- reference. defer_foreign_keys = ON at the top of this script
    -- handles the chicken-and-egg with the frames rebuild.
    frame_id               TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    cancelled              INTEGER NOT NULL DEFAULT 0
);

-- WHERE frame_id IS NULL: the FK on the rebuilt table targets the
-- freshly-rebuilt (empty) rimsky_frames. Any old row whose frame_id
-- pointed at a now-dropped frame would fail the deferred FK check at
-- COMMIT. Pre-v1 nuke-and-replace: only undelivered messages survive
-- the rebuild; populated dev DBs with delivered messages must drop.
INSERT INTO rimsky_messages_new
    (id, instance_id, type, sender, sender_kind, payload, received_at,
     delivered_at, frame_id, cancelled)
SELECT id, instance_id, kind, sender, sender_kind, payload, received_at,
       delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE frame_id IS NULL;

DROP TABLE rimsky_messages;
ALTER TABLE rimsky_messages_new RENAME TO rimsky_messages;

CREATE INDEX idx_messages_instance_received
    ON rimsky_messages(instance_id, received_at);
CREATE INDEX idx_messages_pending
    ON rimsky_messages(instance_id, delivered_at)
    WHERE delivered_at IS NULL;

-- rimsky_publisher_subscriptions: rebuild — rename message_kind → message_type.
DROP INDEX IF EXISTS idx_publisher_subscriptions_instance;
DROP INDEX IF EXISTS idx_publisher_subscriptions_state;

CREATE TABLE rimsky_publisher_subscriptions_new (
    id                TEXT NOT NULL,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    target_node       TEXT NOT NULL,
    message_type      TEXT NOT NULL,
    started_at        TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    state             TEXT NOT NULL DEFAULT 'mounting'
        CHECK (state IN ('mounting','active','failed','stopped')),
    failure_reason    TEXT,
    PRIMARY KEY (publisher_name, id)
);

INSERT INTO rimsky_publisher_subscriptions_new
    (id, instance_id, publisher_name, kind, resolved_config, target_node,
     message_type, started_at, state, failure_reason)
SELECT id, instance_id, publisher_name, kind, resolved_config, target_node,
       message_kind, started_at, state, failure_reason
  FROM rimsky_publisher_subscriptions;

DROP TABLE rimsky_publisher_subscriptions;
ALTER TABLE rimsky_publisher_subscriptions_new RENAME TO rimsky_publisher_subscriptions;

CREATE INDEX idx_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);
CREATE INDEX idx_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');
