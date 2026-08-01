-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
--
-- 036-normalize-timestamp-column-dialect.sql
--
-- Two dialect cleanups on columns the app always writes explicitly
-- (never via a schema default):
--
--   1. rimsky_messages.received_at/delivered_at, rimsky_claim_handles.
--      resolved_at, rimsky_lineage.observed_at, rimsky_publisher_
--      subscriptions.started_at were declared TIMESTAMP and/or
--      CURRENT_TIMESTAMP-defaulted, breaking from every other timestamp
--      column's TEXT + datetime('now') dialect declared in the
--      001-initial.sql header. Now TEXT.
--   2. rimsky_events.occurred_at and rimsky_claim_handles.claimed_at
--      defaulted to datetime('now'), which emits 'YYYY-MM-DD HH:MM:SS'
--      (space-separated, no T/Z) — a different, lexicographically-
--      earlier format than the fixed-nanos 'T...Z' layout every writer
--      actually stamps. Both columns stay NOT NULL; the DEFAULT is
--      dropped rather than reformatted so an insert that ever forgets
--      to supply the column fails loudly instead of silently writing a
--      value the sort order can't compare against.
--
-- SQLite's dynamic typing makes (1) inert at runtime (TEXT values
-- round-trip through a NUMERIC-affinity column unchanged) and the app
-- always supplying the column makes (2)'s default dead code, so no data
-- migration is needed beyond the table rebuild SQLite requires for a
-- column type/default change (inline CHECK/type is immutable via plain
-- ALTER TABLE). All indexes on the rebuilt tables are recreated below
-- under their post-034/035 names and definitions (a DROP TABLE discards
-- every index the earlier migrations left on it); the migrator runs
-- PRAGMA foreign_key_check before commit.

CREATE TABLE rimsky_messages_new (
    id                     TEXT PRIMARY KEY,
    instance_id            TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    type                   TEXT NOT NULL,
    sender                 TEXT NOT NULL,
    sender_kind            TEXT NOT NULL CHECK (sender_kind IN ('operator','publisher','instance')),
    payload                BLOB,
    received_at            TEXT NOT NULL DEFAULT (datetime('now')),
    delivered_at           TEXT,
    frame_id               TEXT REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL,
    cancelled              INTEGER NOT NULL DEFAULT 0
);

INSERT INTO rimsky_messages_new (
    id, instance_id, type, sender, sender_kind, payload,
    received_at, delivered_at, frame_id, cancelled
)
SELECT
    id, instance_id, type, sender, sender_kind, payload,
    received_at, delivered_at, frame_id, cancelled
FROM rimsky_messages;

DROP TABLE rimsky_messages;

ALTER TABLE rimsky_messages_new RENAME TO rimsky_messages;

CREATE INDEX idx_rimsky_messages_instance_received
    ON rimsky_messages(instance_id, received_at);
CREATE INDEX idx_rimsky_messages_pending
    ON rimsky_messages(instance_id, received_at)
    WHERE delivered_at IS NULL AND cancelled = 0;
CREATE INDEX idx_rimsky_messages_frame_id
    ON rimsky_messages(frame_id)
    WHERE frame_id IS NOT NULL;

CREATE TABLE rimsky_claim_handles_new (
    id                          TEXT PRIMARY KEY,
    node_run_id                 TEXT REFERENCES rimsky_node_runs(id) ON DELETE SET NULL,
    lock_kind                   TEXT NOT NULL CHECK (lock_kind IN ('named','claim_scope')),
    lock_name                   TEXT,
    producer_name               TEXT,
    claim_scope_data            TEXT,
    address                     TEXT,
    intent                      TEXT,
    realized_write_semantics    TEXT,
    is_held                     INTEGER NOT NULL DEFAULT 0,
    holder_supervisor_id        TEXT,
    holder_node_id              TEXT NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at                  TEXT NOT NULL,
    expires_at                  TEXT NOT NULL,
    frame_id                    TEXT,
    parent_claim_handle_id      TEXT REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL,
    lifetime                    TEXT NOT NULL DEFAULT 'subgraph'
                                CHECK (lifetime IN ('subgraph','durable')),
    version_id                  TEXT,
    producer_candidate_handle   BLOB,
    aggregation_policy          TEXT,
    expected_children_count     INTEGER NOT NULL DEFAULT 0,
    committed_children_count    INTEGER NOT NULL DEFAULT 0,
    abandoned_children_count    INTEGER NOT NULL DEFAULT 0,
    state                       TEXT NOT NULL DEFAULT 'active'
                                CHECK (state IN ('active','committed','abandoned')),
    resolved_at                 TEXT,
    payload                     TEXT,
    producer_lease_token        TEXT NOT NULL DEFAULT '',
    CHECK (
        (lock_kind = 'named'       AND lock_name IS NOT NULL AND producer_name IS NULL     AND claim_scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
        (lock_kind = 'claim_scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND claim_scope_data IS NOT NULL AND intent IN ('r','rw'))
    ),
    CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL),
    CHECK (state = 'active' OR holder_supervisor_id IS NULL)
);

INSERT INTO rimsky_claim_handles_new (
    id, node_run_id, lock_kind, lock_name, producer_name, claim_scope_data,
    address, intent, realized_write_semantics, is_held, holder_supervisor_id,
    holder_node_id, claimed_at, expires_at, frame_id, parent_claim_handle_id,
    lifetime, version_id, producer_candidate_handle, aggregation_policy,
    expected_children_count, committed_children_count, abandoned_children_count,
    state, resolved_at, payload, producer_lease_token
)
SELECT
    id, node_run_id, lock_kind, lock_name, producer_name, claim_scope_data,
    address, intent, realized_write_semantics, is_held, holder_supervisor_id,
    holder_node_id, claimed_at, expires_at, frame_id, parent_claim_handle_id,
    lifetime, version_id, producer_candidate_handle, aggregation_policy,
    expected_children_count, committed_children_count, abandoned_children_count,
    state, resolved_at, payload, producer_lease_token
FROM rimsky_claim_handles;

DROP TABLE rimsky_claim_handles;

ALTER TABLE rimsky_claim_handles_new RENAME TO rimsky_claim_handles;

CREATE INDEX idx_rimsky_claim_handles_supervisor   ON rimsky_claim_handles (holder_supervisor_id);
CREATE INDEX idx_rimsky_claim_handles_node         ON rimsky_claim_handles (holder_node_id);
CREATE INDEX idx_rimsky_claim_handles_named        ON rimsky_claim_handles (lock_name)  WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_claim_handles_claim_scope  ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'claim_scope';
CREATE INDEX idx_rimsky_claim_handles_expires      ON rimsky_claim_handles (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_rimsky_claim_handles_node_run     ON rimsky_claim_handles (node_run_id);
CREATE INDEX idx_rimsky_claim_handles_held         ON rimsky_claim_handles (node_run_id) WHERE is_held = 1;
CREATE INDEX idx_rimsky_claim_handles_parent
    ON rimsky_claim_handles(parent_claim_handle_id)
    WHERE parent_claim_handle_id IS NOT NULL;
CREATE INDEX idx_rimsky_claim_handles_active
    ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';
CREATE INDEX idx_rimsky_claim_handles_committed_durable
    ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';

CREATE TABLE rimsky_lineage_new (
    id           TEXT PRIMARY KEY,
    record_kind  TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_terminal')),
    instance_id  TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     TEXT NOT NULL,
    observed_at  TEXT NOT NULL DEFAULT (datetime('now')),
    record       TEXT NOT NULL,
    outcome      TEXT NOT NULL
        CHECK (outcome IN ('','committed','abandoned','force_cancelled'))
);

INSERT INTO rimsky_lineage_new (
    id, record_kind, instance_id, frame_id, observed_at, record, outcome
)
SELECT
    id, record_kind, instance_id, frame_id, observed_at, record, outcome
FROM rimsky_lineage;

DROP TABLE rimsky_lineage;

ALTER TABLE rimsky_lineage_new RENAME TO rimsky_lineage;

CREATE INDEX idx_rimsky_lineage_run
    ON rimsky_lineage(record_kind, json_extract(record, '$.run_id'));
CREATE INDEX idx_rimsky_lineage_claim
    ON rimsky_lineage(record_kind, json_extract(record, '$.claim_handle_id'));

CREATE TABLE rimsky_publisher_subscriptions_new (
    id                TEXT NOT NULL,
    instance_id       TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name    TEXT NOT NULL,
    kind              TEXT NOT NULL,
    resolved_config   TEXT NOT NULL,
    message_type      TEXT NOT NULL,
    started_at        TEXT NOT NULL DEFAULT (datetime('now')),
    state             TEXT NOT NULL DEFAULT 'mounting'
        CHECK (state IN ('mounting','active','failed','stopped')),
    failure_reason    TEXT,
    PRIMARY KEY (publisher_name, id)
);

INSERT INTO rimsky_publisher_subscriptions_new (
    id, instance_id, publisher_name, kind, resolved_config,
    message_type, started_at, state, failure_reason
)
SELECT
    id, instance_id, publisher_name, kind, resolved_config,
    message_type, started_at, state, failure_reason
FROM rimsky_publisher_subscriptions;

DROP TABLE rimsky_publisher_subscriptions;

ALTER TABLE rimsky_publisher_subscriptions_new RENAME TO rimsky_publisher_subscriptions;

CREATE INDEX idx_rimsky_publisher_subscriptions_instance
    ON rimsky_publisher_subscriptions(instance_id);
CREATE INDEX idx_rimsky_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');

CREATE TABLE rimsky_events_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id TEXT REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     TEXT REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);

INSERT INTO rimsky_events_new (
    id, instance_id, node_id, kind, payload, occurred_at
)
SELECT
    id, instance_id, node_id, kind, payload, occurred_at
FROM rimsky_events;

DROP TABLE rimsky_events;

ALTER TABLE rimsky_events_new RENAME TO rimsky_events;

CREATE INDEX idx_rimsky_events_node_id_occurred_at ON rimsky_events (node_id, occurred_at DESC);
CREATE INDEX idx_rimsky_events_instance_id_occurred_at ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX idx_rimsky_events_kind_occurred_at ON rimsky_events (kind, occurred_at DESC);
CREATE INDEX idx_rimsky_events_occurred_at ON rimsky_events (occurred_at);
CREATE INDEX idx_rimsky_events_audit_key_id
    ON rimsky_events (json_extract(payload, '$.key_id'))
    WHERE kind LIKE 'auth.%';
CREATE INDEX idx_rimsky_events_audit_key_name
    ON rimsky_events (json_extract(payload, '$.key_name'))
    WHERE kind LIKE 'auth.%';
CREATE INDEX idx_rimsky_events_audit_action
    ON rimsky_events (json_extract(payload, '$.action'))
    WHERE kind LIKE 'auth.%';
CREATE INDEX idx_rimsky_events_audit_status
    ON rimsky_events (json_extract(payload, '$.response_status'))
    WHERE kind LIKE 'auth.%';
CREATE INDEX idx_rimsky_events_audit_mode
    ON rimsky_events (json_extract(payload, '$.mode'))
    WHERE kind LIKE 'auth.%';
CREATE INDEX idx_rimsky_events_audit_request_path
    ON rimsky_events (json_extract(payload, '$.request_path'))
    WHERE kind LIKE 'auth.%';
