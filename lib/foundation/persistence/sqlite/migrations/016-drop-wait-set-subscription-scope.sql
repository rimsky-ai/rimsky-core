-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: wait-set

-- 016-drop-wait-set-subscription-scope.sql
--
-- Retire the subscription_scope column from rimsky_wait_set. See the postgres
-- sibling migration for rationale. SQLite path uses a rename-copy pattern
-- because subscription_scope was part of the primary key.

DELETE FROM rimsky_wait_set WHERE subscription_scope = 'instance';

DROP INDEX IF EXISTS idx_rimsky_wait_set_receiver;
DROP INDEX IF EXISTS idx_rimsky_wait_set_sender;

ALTER TABLE rimsky_wait_set RENAME TO rimsky_wait_set__old_016;

CREATE TABLE rimsky_wait_set (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','transient','terminal')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    drained_at          TEXT,
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind)
);
CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);

INSERT INTO rimsky_wait_set
    (frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, inserted_at, drained_at)
SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, inserted_at, drained_at
FROM rimsky_wait_set__old_016;

DROP TABLE rimsky_wait_set__old_016;
