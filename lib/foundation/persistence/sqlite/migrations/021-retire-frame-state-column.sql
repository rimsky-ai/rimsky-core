-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: frame
-- @concept: instance
-- @decision: frame-isolation-is-structural
--
-- 021-retire-frame-state-column.sql
--
-- Retire rimsky_frames.state. See the postgres sibling migration for
-- rationale. ended_at stays as the scheduler-written one-shot gate for
-- the frame-end reaper.
--
-- SQLite CHECK constraints reference state (chk_running_has_started,
-- chk_terminal_has_ended) and can only be dropped via a table rebuild.
-- The rebuild preserves all frame rows AND all child rows because the
-- migrator applies every migration on a connection with foreign_keys
-- OFF (see migrate.go): with foreign_keys ON, DROP TABLE performs an
-- implicit DELETE that would fire ON DELETE CASCADE into
-- rimsky_node_runs and the wait tables. DROP TABLE + RENAME under
-- sqlite's modern ALTER TABLE semantics automatically updates FK
-- references in child tables (rimsky_messages, rimsky_node_runs,
-- rimsky_breakpoint_hits, rimsky_wait_sets) to point at the renamed
-- table, and the migrator runs PRAGMA foreign_key_check before commit.

DROP INDEX IF EXISTS uq_rimsky_frames_running;

CREATE TABLE rimsky_frames_new (
    frame_id                TEXT PRIMARY KEY,
    instance_id             TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    triggering_message_id   TEXT NOT NULL REFERENCES rimsky_messages(id) ON DELETE RESTRICT,
    root_run_scope_id       TEXT NOT NULL DEFAULT '',
    started_at              TEXT,
    ended_at                TEXT,
    frame_timeout_ms        INTEGER NOT NULL CHECK (frame_timeout_ms >= 60000),
    last_progress_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO rimsky_frames_new (
    frame_id, instance_id, triggering_message_id, root_run_scope_id,
    started_at, ended_at, frame_timeout_ms, last_progress_at
)
SELECT
    frame_id, instance_id, triggering_message_id, root_run_scope_id,
    started_at, ended_at, frame_timeout_ms, last_progress_at
FROM rimsky_frames;

DROP TABLE rimsky_frames;

ALTER TABLE rimsky_frames_new RENAME TO rimsky_frames;

CREATE UNIQUE INDEX uq_rimsky_frames_open
    ON rimsky_frames (instance_id)
    WHERE ended_at IS NULL;
