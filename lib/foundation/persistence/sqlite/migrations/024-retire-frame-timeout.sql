-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: frame
--
-- 024-retire-frame-timeout.sql
--
-- Retire rimsky_frames.frame_timeout_ms and its >=60000 CHECK. See the
-- postgres sibling migration for rationale. last_progress_at stays as
-- the progress clock for orphan detection and quiet-period reaping.
--
-- SQLite CHECK constraints on frame_timeout_ms block a plain DROP
-- COLUMN and require the same table-rebuild-under-foreign_keys-OFF
-- approach as migration 021 (see that migration's comment for why the
-- rebuild preserves child rows under the migrator's FK-off connection
-- and the pre-commit PRAGMA foreign_key_check).

DROP INDEX IF EXISTS uq_rimsky_frames_open;

CREATE TABLE rimsky_frames_new (
    frame_id                TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    triggering_message_id   TEXT NOT NULL REFERENCES rimsky_messages(id) ON DELETE RESTRICT,
    root_run_scope_id       TEXT NOT NULL DEFAULT '',
    started_at              TEXT,
    ended_at                TEXT,
    last_progress_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO rimsky_frames_new (
    frame_id, instance_id, triggering_message_id, root_run_scope_id,
    started_at, ended_at, last_progress_at
)
SELECT
    frame_id, instance_id, triggering_message_id, root_run_scope_id,
    started_at, ended_at, last_progress_at
FROM rimsky_frames;

DROP TABLE rimsky_frames;

ALTER TABLE rimsky_frames_new RENAME TO rimsky_frames;

CREATE UNIQUE INDEX uq_rimsky_frames_open
    ON rimsky_frames (instance_id)
    WHERE ended_at IS NULL;
