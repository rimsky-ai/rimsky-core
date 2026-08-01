-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: run-scope
-- @concept: frame
--
-- 029-frame-root-run-scope-fk.sql
--
-- See the postgres sibling migration for rationale. SQLite has no
-- ALTER TABLE ADD CONSTRAINT and requires the same table-rebuild-under-
-- foreign_keys-OFF approach as migrations 021/024 (see 021's comment
-- for why the rebuild preserves child rows under the migrator's FK-off
-- connection and the pre-commit PRAGMA foreign_key_check). The prior
-- DEFAULT '' is dropped along with the new NOT NULL REFERENCES: every
-- rimsky_frames insert since 015 supplies a real root run scope id.

CREATE TABLE rimsky_frames_new (
    frame_id                TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    triggering_message_id   TEXT NOT NULL REFERENCES rimsky_messages(id) ON DELETE RESTRICT,
    root_run_scope_id       TEXT NOT NULL REFERENCES rimsky_run_scopes(id),
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

DROP INDEX IF EXISTS uq_rimsky_frames_open;

DROP TABLE rimsky_frames;

ALTER TABLE rimsky_frames_new RENAME TO rimsky_frames;

CREATE UNIQUE INDEX uq_rimsky_frames_open
    ON rimsky_frames (instance_id)
    WHERE ended_at IS NULL;
