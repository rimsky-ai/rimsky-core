-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 009-subscription-mounting.sql — parallel to postgres 009. Publisher
-- subscriptions become desired-state rows with an observable mounting
-- lifecycle (concept:publisher-subscription): the state CHECK gains
-- 'mounting' (now the default — rows are born unmounted), a nullable
-- failure_reason column records why a non-retryable 'failed' row
-- failed, and the partial state index covers the states the
-- reconciler / resync sweeps select ('mounting' + 'active').
--
-- SQLite cannot ALTER a CHECK constraint in place, so the table is
-- rebuilt (same idiom as migration 006): create the _new table with the
-- widened CHECK + new column, copy every row, drop the old table,
-- rename, recreate the indexes. rimsky_publisher_subscriptions is a
-- leaf table — no FOREIGN KEY in any other table references it — so
-- dropping it touches nothing downstream. The migration runs inside the
-- migration transaction; the indexes are restored before commit.

CREATE TABLE rimsky_publisher_subscriptions_new (
    id                TEXT NOT NULL,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    target_node       TEXT NOT NULL,
    message_kind      TEXT NOT NULL DEFAULT 'invalidate',
    started_at        TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    state             TEXT NOT NULL DEFAULT 'mounting'
        CHECK (state IN ('mounting','active','failed','stopped')),
    failure_reason    TEXT,
    PRIMARY KEY (publisher_name, id)
);

INSERT INTO rimsky_publisher_subscriptions_new
    (id, instance_id, publisher_name, kind, resolved_config,
     target_node, message_kind, started_at, state)
SELECT
    id, instance_id, publisher_name, kind, resolved_config,
    target_node, message_kind, started_at, state
FROM rimsky_publisher_subscriptions;

DROP TABLE rimsky_publisher_subscriptions;

ALTER TABLE rimsky_publisher_subscriptions_new RENAME TO rimsky_publisher_subscriptions;

CREATE INDEX idx_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);
CREATE INDEX idx_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');
