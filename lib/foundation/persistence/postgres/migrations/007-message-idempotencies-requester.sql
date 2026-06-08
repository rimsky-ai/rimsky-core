-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 007-message-idempotencies-requester.sql
--
-- Widen the rimsky_message_idempotencies dedup tuple to include BOTH a
-- structural sender_kind discriminator AND the requester subject, so
-- two distinct api-keys posting to the same instance with the same
-- Idempotency-Key can no longer cross-collide (the second caller
-- previously received the first caller's message_id back as a "replay"
-- even though the second caller's payload was materially different),
-- AND a publisher whose operator-chosen publisher_name happens to be
-- the literal string `"operator"` can no longer cross-collide with
-- operator-side emits. The dedup tuple now is
-- (instance_id, sender_kind, sender, sender_subject, idempotency_key).
--
-- `sender_kind` is a structural TEXT discriminator (`'operator'` /
-- `'publisher'` / `'anonymous'`) that namespaces the bare `sender`
-- string by source-of-claim, so two callers landing on the same
-- `sender` value through different code paths can no longer share a
-- dedup tuple. The earlier "publisher named operator" collision (the
-- `sender` column was a flat string with operator-side using the
-- hard-coded literal `"operator"` and publisher-side using the
-- operator-chosen `publisher_name`) is closed by this column.
--
-- `sender_subject` is a TEXT discriminator within sender_kind:
--   - Operator with api-key   → the api-key UUID as a string.
--   - Operator anonymous-mode → 'anonymous'.
--   - Publisher              → '' (empty); the existing `sender` column
--                              already carries the per-publisher
--                              publisher_name and provides isolation.
-- The empty-string form keeps the column NOT NULL so it can sit in
-- the PRIMARY KEY (postgres PK columns reject NULL). Pre-v1 there is
-- no production data to migrate, so this rebuilds the PK rather than
-- threading a compat shim.

ALTER TABLE rimsky_message_idempotencies
    DROP CONSTRAINT rimsky_message_idempotencies_pkey;
ALTER TABLE rimsky_message_idempotencies
    ADD COLUMN sender_kind TEXT NOT NULL DEFAULT 'operator';
ALTER TABLE rimsky_message_idempotencies
    ADD COLUMN sender_subject TEXT NOT NULL DEFAULT '';
ALTER TABLE rimsky_message_idempotencies
    ADD PRIMARY KEY (instance_id, sender_kind, sender, sender_subject, idempotency_key);
