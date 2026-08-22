-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

-- 043-message-sender-subject.sql
--
-- Carry the sender-subject on the message envelope.
--
-- The envelope named the sender kind and the sender, so two api-keys
-- both read back as the literal word "operator". The idempotency
-- ledger recorded the subject, but it is a dedup ledger, not an audit
-- surface. The envelope now names the actor behind the send: the
-- api-key of an operator send, the subscription of a publisher send,
-- empty for an instance send.

ALTER TABLE rimsky_messages ADD COLUMN sender_subject TEXT NOT NULL DEFAULT '';
