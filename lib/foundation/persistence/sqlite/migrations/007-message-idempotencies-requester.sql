-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 007-message-idempotencies-requester.sql
--
-- Widen the rimsky_message_idempotencies dedup tuple to include BOTH a
-- structural sender_kind discriminator AND the requester subject.
-- Mirror of the postgres migration; SQLite is dev-only. Pre-v1 there
-- is no data to migrate, so this drops and recreates the table
-- (SQLite doesn't support ALTER TABLE for PK changes).
--
-- See the postgres migration's header for the rationale; in short, the
-- dedup tuple is now
-- (instance_id, sender_kind, sender, sender_subject, idempotency_key)
-- so neither (a) two distinct api-keys posting the same Idempotency-Key
-- nor (b) a publisher named "operator" emitting against the same
-- instance as an operator can cross-collide.

DROP TABLE IF EXISTS rimsky_message_idempotencies;
CREATE TABLE rimsky_message_idempotencies (
    instance_id      TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sender_kind      TEXT NOT NULL DEFAULT 'operator',
    sender           TEXT NOT NULL,
    sender_subject   TEXT NOT NULL DEFAULT '',
    idempotency_key  TEXT NOT NULL,
    message_id       TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (instance_id, sender_kind, sender, sender_subject, idempotency_key)
);
CREATE INDEX idx_message_idempotencies_created_at
    ON rimsky_message_idempotencies(created_at);
