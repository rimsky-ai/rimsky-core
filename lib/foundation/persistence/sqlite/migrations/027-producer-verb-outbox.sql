-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: terminal-resolution
--
-- 027-producer-verb-outbox.sql
--
-- Durable per-producer outbox for claim terminal verbs (commit / abandon /
-- release). The claim-handle disposition is decided and recorded at
-- settlement; the verb row here is the undelivered notification, retried
-- with backoff in strict per-(producer, scope) seq order. Rows carry no
-- foreign keys: their lifetime is tied to the producer relationship, not
-- the instance or the claim-handle row.

CREATE TABLE rimsky_producer_verb_outbox (
    seq              INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    claim_handle_id  TEXT    NOT NULL,
    producer_name    TEXT    NOT NULL,
    verb             TEXT    NOT NULL CHECK (verb IN ('commit','abandon','release')),
    claim_scope_data BLOB,
    address          BLOB,
    supervisor_id    TEXT    NOT NULL DEFAULT '',
    instance_id      TEXT,
    parent_claim_handle_id TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  TEXT    NOT NULL,
    last_error       TEXT    NOT NULL DEFAULT '',
    enqueued_at      TEXT    NOT NULL,
    UNIQUE (claim_handle_id, verb)
);

CREATE INDEX rimsky_producer_verb_outbox_producer_seq
    ON rimsky_producer_verb_outbox (producer_name, seq);
