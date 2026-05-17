-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- SQLite mirror of `005-claim-holders-wait-set-run-level.sql` (postgres).
-- See the postgres version for the full rationale. Pre-v1 break-freely:
-- both tables are DROP + CREATE rather than ALTER + rename. Existing
-- rows are not preserved.

-- ---------------------------------------------------------------------
-- B5. rimsky_claim_holders — holder_node_id → holder_run_id
-- ---------------------------------------------------------------------

DROP TABLE IF EXISTS rimsky_claim_holders;
CREATE TABLE rimsky_claim_holders (
    id               TEXT PRIMARY KEY,
    claim_handle_id  TEXT NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_run_id    TEXT NOT NULL REFERENCES rimsky_node_runs(id)     ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active','completed','failed')),
    completed_at     TEXT,
    frame_id         TEXT,
    UNIQUE (claim_handle_id, holder_run_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_run         ON rimsky_claim_holders (holder_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- ---------------------------------------------------------------------
-- B6. rimsky_wait_set — receiver_node_id → receiver_run_id;
--                       sender_node_id   → sender_run_id
-- ---------------------------------------------------------------------

DROP TABLE IF EXISTS rimsky_wait_set;
CREATE TABLE rimsky_wait_set (
    frame_id            TEXT NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       TEXT NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        TEXT,
    inserted_at         TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);
