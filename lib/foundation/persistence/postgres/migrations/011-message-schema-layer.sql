-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 010-message-schema-layer.sql
--
-- Make messages a typed, schema-declared primitive: collapse coalesce and
-- the per-subscription frame: modifier, route every frame creation through
-- one message-delivery path, retire backfill, and collapse legacy envelope /
-- publisher-routing fields into typed-message vocabulary.
--
-- Schema-shape changes (pre-v1 forward-only — existing dev databases must
-- be nuked, not upgraded; the NOT NULL on rimsky_frames.triggering_message_id
-- has no DEFAULT, so existing rows would fail the alter):
--
--   rimsky_frames:
--     + triggering_message_id UUID NOT NULL REFERENCES rimsky_messages(id)
--       (every frame carries the message that opened it; FK ON DELETE RESTRICT
--       so a message cannot disappear from under a frame that points at it).
--     - source_node_ids                (cross-frame-coupling rides on emit
--       nodes + cascade-emit, not on per-frame source-node lists).
--     - frame_resolution_mode          (coalesce retires; one path).
--     - uq_rimsky_frames_coalesce_queued index (predicate referenced the
--       dropped frame_resolution_mode column).
--
--   rimsky_instances:
--     - frame_delivery_mode            (per-instance coalesce/serial_queue
--       toggle retires; delivery is always one-message-per-frame).
--
--   rimsky_messages:
--     - backfill_operation_id          (backfill retires as a first-class
--       subsystem; the typed-message machinery covers its use case).
--     - target                         (envelope routing collapses to type
--       discriminator + subscriber-driven cascade).
--     - kind  → renamed type           (envelope type-path discriminator).
--     - idx_messages_backfill          (predicate referenced the dropped
--       backfill_operation_id column).
--
--   rimsky_publisher_subscriptions:
--     - message_kind → renamed message_type (matches the envelope rename).
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
-- No BEGIN/COMMIT wrapper: the migrator's ApplyOne already opens a tx
-- around the script execution, so wrapping here would nest transactions.
--
-- No `INSERT INTO rimsky_migrations` footer: this file matches the
-- 008/009 convention of ending at the last DDL statement. The migrator
-- tracks application externally (it records the file id when ApplyOne
-- returns; the script itself does NOT register itself in
-- rimsky_migrations). A future side-by-side compare that says "008/009
-- registered themselves at the bottom, why doesn't this one?" should
-- read 008-error-class-strings.sql / 009-subscription-mounting.sql
-- first — neither carries the footer either.

-- PRE-CONDITION (loud version, will save you a cryptic NOT NULL failure):
-- this script REQUIRES a clean dev DB. Specifically rimsky_frames must be
-- empty AND no rimsky_wait_set / rimsky_node_runs row may still hold a
-- non-NULL frame_id (those FKs target the soon-to-be-rebuilt rimsky_frames).
-- A populated table would fail the NOT NULL constraint on the column-add
-- below; without this DO block the operator sees an opaque Postgres NOT
-- NULL violation with no hint that the script EXPECTED a clean db. Mirrors
-- the SQLite migration 010's documented pre-condition; raises a typed
-- exception naming the gate so the diagnostic is self-evident. Pre-v1
-- has no backwards-compat duty per .claude/rules/rules.md — clean removal
-- is the bias; this script is the nuke-and-replace path, not an in-place
-- migration.
DO $$
DECLARE
    frame_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO frame_count FROM rimsky_frames;
    IF frame_count > 0 THEN
        RAISE EXCEPTION
            'migration 010-message-schema-layer: rimsky_frames is not empty '
            '(% rows). Pre-v1 forward-only: drop and recreate the dev '
            'database before running this migration. The NOT NULL on '
            'rimsky_frames.triggering_message_id has no DEFAULT, so '
            'existing rows would fail the column-add and roll the '
            'migration back.', frame_count;
    END IF;
END $$;

-- rimsky_frames: add triggering_message_id, drop retired columns + index.
-- `IF EXISTS` matches the convention used by the migration suite (e.g.
-- 008/009 baselines) and survives a partial-replay scenario: the
-- pre-condition DO block above checks rimsky_frames is empty but does
-- not check the index exists, so a partially-applied prior run would
-- otherwise wedge at this DROP rather than continuing forward.
DROP INDEX IF EXISTS uq_rimsky_frames_coalesce_queued;

ALTER TABLE rimsky_frames
    DROP COLUMN source_node_ids,
    DROP COLUMN frame_resolution_mode;

-- ON DELETE RESTRICT: a frame's triggering message is load-bearing audit
-- history (the frame-origin-audit story depends on every frame retaining
-- its triggering message). RESTRICT refuses a destructive ordering and
-- surfaces the bug; CASCADE would silently obliterate the audit data and
-- every dependent run row. Instance-wide teardown still works because
-- instance delete CASCADEs both sides in parallel.
ALTER TABLE rimsky_frames
    ADD COLUMN triggering_message_id UUID NOT NULL
        REFERENCES rimsky_messages(id) ON DELETE RESTRICT;

-- rimsky_instances: drop the per-instance delivery-mode column.
ALTER TABLE rimsky_instances
    DROP COLUMN frame_delivery_mode;

-- rimsky_messages: drop legacy routing/backfill columns, rename kind→type.
-- `IF EXISTS` per the migration-suite convention (see the uq_rimsky_frames
-- DROP above); a partially-replayed run does not wedge at this DROP.
DROP INDEX IF EXISTS idx_messages_backfill;

ALTER TABLE rimsky_messages
    DROP COLUMN backfill_operation_id,
    DROP COLUMN target;

ALTER TABLE rimsky_messages
    RENAME COLUMN kind TO type;

-- Add the rimsky_messages.frame_id → rimsky_frames(frame_id) FK that
-- the 001 baseline shipped without (the column existed but with no
-- REFERENCES). The SQLite 010 migration introduces the equivalent
-- constraint as part of its rimsky_messages rebuild; without the
-- matching declaration here, cross-backend scenarios passing on
-- SQLite (FK enforced) would silently fail on Postgres (FK absent).
-- ON DELETE SET NULL so a future PruneTraceForRetention sweep
-- deleting an old terminal frame NULLs the message's frame_id rather
-- than leaving a dangling reference; instance-wide teardown still
-- works because rimsky_messages.instance_id CASCADEs in parallel.
ALTER TABLE rimsky_messages
    ADD CONSTRAINT rimsky_messages_frame_id_fkey
        FOREIGN KEY (frame_id) REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL;

-- rimsky_publisher_subscriptions: message_kind → message_type. The legacy
-- DEFAULT 'invalidate' (baked into 001-schema.sql) retires alongside the
-- envelope's kind→type rename; publishers declare an explicit message_type
-- validated against the target template's messages-schema registry. An
-- omitted message_type must fail loudly, not silently take a stale default.
ALTER TABLE rimsky_publisher_subscriptions
    RENAME COLUMN message_kind TO message_type;
ALTER TABLE rimsky_publisher_subscriptions
    ALTER COLUMN message_type DROP DEFAULT;
