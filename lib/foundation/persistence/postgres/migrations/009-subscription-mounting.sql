-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 009-subscription-mounting.sql
--
-- Publisher subscriptions become desired-state rows with an observable
-- mounting lifecycle (concept:publisher-subscription). Instance-create
-- inserts rows in state='mounting' and returns immediately; a
-- reconciliation worker drives the publisher Subscribe handshake and
-- flips rows to 'active' on success. 'failed' is reserved for
-- non-retryable errors (e.g. a publisher name not present in the
-- registry) and now carries an operator-readable reason.
--
-- Three changes:
--   1. The state CHECK gains 'mounting'.
--   2. A nullable failure_reason column so a 'failed' row can say why
--      (surfaced on the instance-detail API).
--   3. The partial state index covers 'mounting' too — the reconciler
--      selects mounting rows every tick, the resync sweep selects
--      mounting + active.
--
-- Default flips to 'mounting': rows are born unmounted by design.
-- Pre-v1 there is no production data to migrate.

ALTER TABLE rimsky_publisher_subscriptions
    DROP CONSTRAINT rimsky_publisher_subscriptions_state_check;
ALTER TABLE rimsky_publisher_subscriptions
    ADD CONSTRAINT rimsky_publisher_subscriptions_state_check
        CHECK (state IN ('mounting','active','failed','stopped'));
ALTER TABLE rimsky_publisher_subscriptions
    ALTER COLUMN state SET DEFAULT 'mounting';

ALTER TABLE rimsky_publisher_subscriptions
    ADD COLUMN failure_reason TEXT;

DROP INDEX idx_publisher_subscriptions_state;
CREATE INDEX idx_publisher_subscriptions_state
    ON rimsky_publisher_subscriptions(state)
    WHERE state IN ('mounting','active');
