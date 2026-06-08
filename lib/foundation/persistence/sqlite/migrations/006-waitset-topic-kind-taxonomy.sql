-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 006-waitset-topic-kind-taxonomy.sql — parallel to postgres 006. Broaden the
-- rimsky_wait_set.topic_kind CHECK to the full 5-value signal taxonomy
-- ('state','attribute','event','transient','message','terminal') per spec
-- 2026-06-06-comprehensive-gap-closure-design (story
-- S-cascade-waitset-topic-taxonomy). This is the CHECK-broadening migration
-- deferred from the 2026-05-23 signal-taxonomy reshape.
--
-- SQLite cannot ALTER a CHECK constraint in place, so the leaf table is
-- rebuilt: create rimsky_wait_set_new with the broadened CHECK and an
-- otherwise identical column set / PK, copy every row over, drop the old
-- table, rename the new one into place, and recreate the two read indexes.
-- This is safe because rimsky_wait_set is a leaf table — no FOREIGN KEY in
-- any other table references it — so dropping it touches nothing downstream.
-- The migration runs inside the migration transaction; the indexes are
-- restored before commit so the access surface is unchanged on the far side.
--
-- 'state' stays admitted — back-compat for any existing 'state' rows, the
-- empty/unrecognized fallback in waitSetTopicKindFor, and the conformance
-- fixtures.

CREATE TABLE rimsky_wait_set_new (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','event','transient','message','terminal')),
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
FROM rimsky_wait_set;

DROP TABLE rimsky_wait_set;

ALTER TABLE rimsky_wait_set_new RENAME TO rimsky_wait_set;

CREATE INDEX idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);
