-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 016-drop-wait-set-subscription-scope.sql
--
-- Retire the subscription_scope column from rimsky_wait_set alongside the
-- cross-cutting-subscription (instance: true) feature retirement. The column
-- distinguished 'direct' vs 'instance' scope; with instance-scope subscriptions
-- gone from the surface, only 'direct' can ever be written, so the column
-- becomes a constant. The wait-set PK previously included the column to allow
-- both scopes to coexist for the same (frame, receiver, sender, topic_kind);
-- after retirement the PK collapses to that four-tuple.
--
-- Any pre-migration rows carrying scope 'instance' are dropped — they are
-- gate-rows for the retired feature, semantically dead after this migration.

DELETE FROM rimsky_wait_set WHERE subscription_scope = 'instance';

ALTER TABLE rimsky_wait_set DROP CONSTRAINT rimsky_wait_set_pkey;
ALTER TABLE rimsky_wait_set DROP COLUMN subscription_scope;
ALTER TABLE rimsky_wait_set ADD PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind);
