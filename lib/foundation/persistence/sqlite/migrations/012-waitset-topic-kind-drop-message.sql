-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 011-waitset-topic-kind-drop-message.sql (SQLite mirror of postgres 011).
--
-- Drop 'message' from the rimsky_wait_set.topic_kind CHECK admitted set.
-- See the postgres migration for rationale; SQLite cannot ALTER a CHECK
-- constraint in place, so the leaf table is rebuilt: create
-- rimsky_wait_set_new with the tightened CHECK and an otherwise identical
-- column set / PK, copy every row over EXCEPT any with topic_kind='message'
-- (pre-v1: any stale 'message' rows would fail the tightened CHECK on
-- replay and indicate broken upstream code, so dropping them here keeps
-- the migration deterministic for clean dev databases), drop the old
-- table, rename the new one into place, and recreate the two read
-- indexes. The wait_set is a leaf table — no FOREIGN KEY in any other
-- table references it — so dropping it touches nothing downstream.
--
-- No BEGIN/COMMIT wrapper: the migrator's ApplyOne already opens a tx
-- around the script execution, so wrapping here trips SQLite's "cannot
-- start a transaction within a transaction" guard.

CREATE TABLE rimsky_wait_set_new (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','event','transient','terminal')),
    subscription_scope  TEXT NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    drained_at          TEXT,
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);

INSERT INTO rimsky_wait_set_new
    (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope,
     topic_filter, inserted_at, drained_at)
SELECT
    frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope,
    topic_filter, inserted_at, drained_at
FROM rimsky_wait_set
WHERE topic_kind <> 'message';

DROP TABLE rimsky_wait_set;

ALTER TABLE rimsky_wait_set_new RENAME TO rimsky_wait_set;

CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);

-- Migration scope footer (pre-v1 forward-only): the WHERE clause filters
-- only `topic_kind = 'message'`. Any row carrying a different stale
-- topic_kind value (e.g. one that does not satisfy the tightened CHECK)
-- would fail the deferred CHECK at COMMIT and roll the migration back.
-- The Postgres counterpart (sibling 011) deletes `topic_kind = 'message'`
-- in place and then swaps the CHECK; same scope on the same clean-dev-db
-- assumption. Pre-v1 operators run against clean dev databases — no
-- backwards-compat duty per `.claude/rules/rules.md`.
